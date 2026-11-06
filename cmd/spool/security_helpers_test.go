package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/orrn/spool/internal/ai"
	"github.com/orrn/spool/internal/api/handlers"
	apimw "github.com/orrn/spool/internal/api/middleware"
	"github.com/orrn/spool/internal/config"
	"github.com/orrn/spool/internal/core"
	"github.com/orrn/spool/internal/db"
)

const (
	testMachineSecret = "test-node-secret"
	testAdminPassword = "admin-pass-123"
	testHost          = "spool.test:8080"
)

var (
	testDBOnce sync.Once
	testDBErr  error
	testSeq    int64
	printerSeq int64
)

// testSpool is the spool's real route table over a temporary database, with a
// machine secret configured, exactly as registerRoutes mounts it for main.
type testSpool struct {
	r    *gin.Engine
	auth *apimw.AuthMiddleware
}

func newTestSpool(t *testing.T) *testSpool {
	t.Helper()
	testDBOnce.Do(func() {
		dir, err := os.MkdirTemp("", "spool-routes-test")
		if err != nil {
			testDBErr = err
			return
		}
		testDBErr = db.Init(db.Config{Path: filepath.Join(dir, "spool.db")})
	})
	if testDBErr != nil {
		t.Fatal(testDBErr)
	}
	database := db.GetDB()
	auth, err := apimw.NewAuthMiddleware(database)
	if err != nil {
		t.Fatal(err)
	}
	auth.SetORRNVerifier(apimw.NewORRNVerifier(testMachineSecret))
	queue := core.NewQueue(database, nil, nil, nil, nil)
	gen := core.NewTSPL2Generator()
	cfg := &config.Config{}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.SetFuncMap(handlers.TemplateFuncs())
	r.LoadHTMLGlob(webTemplateGlob)
	registerRoutes(r, routeDeps{
		auth:      auth,
		webUI:     handlers.NewWebUIHandler(database, queue, nil).WithDeps(handlers.WebUIDeps{Config: cfg}),
		printers:  handlers.NewPrinterHandler(database, nil),
		jobs:      handlers.NewJobHandler(database, queue, gen),
		templates: handlers.NewTemplateHandler(database, gen, queue),
		webhooks:  handlers.NewWebhookHandler(database, nil),
		ai:        handlers.NewAIHandler(ai.NewGeminiClient(), database, nil),
		archive:   handlers.NewArchiveHandler(nil, database),
		settings:  handlers.NewSettingsHandler(database, cfg),
		scale:     handlers.NewScaleHandler(nil),
		version:   "test",
	})
	return &testSpool{r: r, auth: auth}
}

// do sends req to the spool with a short deadline (the scale stream only
// returns when its request context ends) and the test host.
func (s *testSpool) do(req *http.Request) *httptest.ResponseRecorder {
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	req.Host = testHost
	w := httptest.NewRecorder()
	s.r.ServeHTTP(w, req)
	return w
}

// machine sends a request signed with the node's shared secret, as ORRN does.
func (s *testSpool) machine(method, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	// A distinct millisecond per call keeps the replay cache from refusing a
	// legitimate second request to the same path.
	at := time.Now().Add(time.Duration(atomic.AddInt64(&testSeq, 1)) * time.Millisecond)
	req.Header.Set("Authorization", "Bearer "+apimw.SignORRN(testMachineSecret, method, path, body, at))
	return s.do(req)
}

// ensurePassword stores the test admin password directly, so tests that need a
// session do not depend on how first-boot setup works.
func ensurePassword(t *testing.T) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testAdminPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Settings.SetSetting(context.Background(), "admin_password", string(hash), false); err != nil {
		t.Fatal(err)
	}
}

// session signs in through the JSON login and returns the session cookie.
func (s *testSpool) session(t *testing.T) *http.Cookie {
	t.Helper()
	ensurePassword(t)
	body, _ := json.Marshal(map[string]string{"password": testAdminPassword})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", "http://"+testHost)
	w := s.do(req)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "spool_auth" && c.Value != "" {
			return c
		}
	}
	t.Fatal("login set no session cookie")
	return nil
}

// browser sends a cookie-authenticated request as a same-origin page would.
func (s *testSpool) browser(cookie *http.Cookie, method, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.AddCookie(cookie)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Origin", "http://"+testHost)
	}
	return s.do(req)
}

func countJobs(t *testing.T) int {
	t.Helper()
	var n int
	if err := db.GetDB().QueryRow("SELECT COUNT(*) FROM print_jobs").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func setting(t *testing.T, key string) string {
	t.Helper()
	s, err := db.Settings.GetSetting(context.Background(), key)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return s.Value
}
