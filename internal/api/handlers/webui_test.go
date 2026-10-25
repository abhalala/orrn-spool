package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/api/middleware"
	"github.com/orrn/spool/internal/config"
	"github.com/orrn/spool/internal/core"
	"github.com/orrn/spool/internal/db"
)

// uiServer wires the web UI the way cmd/spool/main.go does: real templates, real
// auth middleware, the page routes plus the job API behind the HTMX fragment
// middleware.
type uiServer struct {
	r      *gin.Engine
	fix    *keyFixture
	cookie *http.Cookie
}

func newUIServer(t *testing.T) *uiServer {
	t.Helper()
	f := newKeyFixture(t)

	auth, err := middleware.NewAuthMiddleware(db.GetDB())
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.SetFuncMap(TemplateFuncs())
	r.LoadHTMLGlob("../../../web/templates/*/*")
	r.GET("/login", auth.LoginPage)
	r.POST("/login", auth.LoginForm)
	r.POST("/logout", auth.LogoutRedirect)

	ui := NewWebUIHandler(db.GetDB(), f.queue, nil).WithDeps(WebUIDeps{Config: &config.Config{Server: config.ServerConfig{Port: 8080}}})
	pages := r.Group("/")
	pages.Use(auth.RequirePageAuth())
	ui.RegisterPages(pages)

	api := r.Group("/api")
	api.Use(auth.RequireAuth())
	api.Use(ui.HTMXFragments())
	NewJobHandler(db.GetDB(), f.queue, core.NewTSPL2Generator()).RegisterRoutes(api)

	s := &uiServer{r: r, fix: f}
	s.signIn(t)
	return s
}

// signIn sets the first password through the login form, as a person would.
func (s *uiServer) signIn(t *testing.T) {
	t.Helper()
	form := url.Values{"password": {"secret123"}, "confirm_password": {"secret123"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.r.ServeHTTP(w, req)
	if w.Code == http.StatusBadRequest || w.Code == http.StatusUnauthorized {
		// The shared test database already has a password from an earlier test.
		form = url.Values{"password": {"secret123"}}
		req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		s.r.ServeHTTP(w, req)
	}
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("sign in: %d -> %q: %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "spool_auth" {
			s.cookie = c
		}
	}
	if s.cookie == nil {
		t.Fatal("sign in set no session cookie")
	}
	if s.cookie.Secure {
		t.Fatal("session cookie is Secure on a plain-http request; browsers would drop it on the LAN")
	}
}

func (s *uiServer) get(path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(s.cookie)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.r.ServeHTTP(w, req)
	return w
}

func TestBrowserWithoutSessionIsSentToLogin(t *testing.T) {
	s := newUIServer(t)
	for _, path := range []string{"/", "/printers", "/queue", "/history", "/templates", "/settings"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		w := httptest.NewRecorder()
		s.r.ServeHTTP(w, req)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
			t.Errorf("%s without a session: %d -> %q, want 303 -> /login", path, w.Code, w.Header().Get("Location"))
		}
	}
	// An API client still gets the 401 JSON, not a redirect.
	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	w := httptest.NewRecorder()
	s.r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("/api/jobs without a session: %d, want 401", w.Code)
	}
}

// Every page and fragment answers 200 with a real body once signed in, against
// a database that holds a printer, a template and jobs in several states.
func TestEveryUIRouteRendersABody(t *testing.T) {
	s := newUIServer(t)
	f := s.fix
	for i := 0; i < 3; i++ {
		if code, out := f.post(t, f.job(fmt.Sprintf("UI-%d", i), fmt.Sprintf("ui:%s:%d", t.Name(), i)), nil); code != http.StatusCreated {
			t.Fatalf("seed job: %d %v", code, out)
		}
	}
	if _, err := db.GetDB().Exec("UPDATE print_jobs SET status='failed', error_message='boom', started_at=datetime('now','-2 seconds'), completed_at=datetime('now') WHERE id = (SELECT MAX(id) FROM print_jobs)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetDB().Exec("UPDATE print_jobs SET status='completed', started_at=datetime('now','-2 seconds'), completed_at=datetime('now') WHERE id = (SELECT MAX(id)-1 FROM print_jobs)"); err != nil {
		t.Fatal(err)
	}
	var lastJob int64
	_ = db.GetDB().QueryRow("SELECT MAX(id) FROM print_jobs").Scan(&lastJob)

	paths := []string{
		"/", "/dashboard", "/printers", "/queue", "/history", "/templates", "/settings", "/settings/audit",
		"/api/dashboard/stats",
		fmt.Sprintf("/ui/printers/%d/status", f.printerID),
		"/api/printers/grid", "/api/jobs/queue-content",
		"/api/jobs/history-table",
		"/api/jobs/history-table?status=failed&page=1&sort=id&dir=asc",
		fmt.Sprintf("/api/jobs/history-table?printer=%d&dateFrom=2020-01-01&dateTo=2099-01-01", f.printerID),
		"/printers/add-modal",
		fmt.Sprintf("/printers/%d/edit-modal", f.printerID),
		fmt.Sprintf("/printers/%d/test-modal", f.printerID),
		"/jobs/new-modal",
		fmt.Sprintf("/jobs/%d/detail-modal", lastJob),
		"/templates/create-modal",
		fmt.Sprintf("/templates/%d/edit-modal", f.templateID),
		fmt.Sprintf("/templates/%d/preview-modal", f.templateID),
	}
	for _, path := range paths {
		w := s.get(path, nil)
		body := w.Body.String()
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: %d %.200s", path, w.Code, body)
			continue
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("GET %s: 200 with an empty body", path)
		}
		if strings.Contains(body, "<no value>") {
			t.Errorf("GET %s: body contains <no value>", path)
		}
	}

	// Full pages carry the layout and the seeded printer's name.
	for _, path := range []string{"/", "/printers"} {
		body := s.get(path, nil).Body.String()
		for _, want := range []string{"<!DOCTYPE html>", "TSC Spool", f.printerName(t)} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s: missing %q", path, want)
			}
		}
	}
	history := s.get("/api/jobs/history-table", nil).Body.String()
	if !strings.Contains(history, fmt.Sprintf("#%d", lastJob)) || !strings.Contains(history, "Failed") {
		t.Errorf("history table does not list the failed job #%d", lastJob)
	}
	queue := s.get("/api/jobs/queue-content", nil).Body.String()
	if !strings.Contains(queue, "data-job-id=") {
		t.Errorf("queue fragment lists no pending job")
	}
	edit := s.get(fmt.Sprintf("/templates/%d/edit-modal", f.templateID), nil).Body.String()
	for _, want := range []string{"Use {{variable}} syntax in content to define variables", `placeholder="Use {{variable}} for dynamic text"`, "templateEditor({"} {
		if !strings.Contains(edit, want) {
			t.Errorf("template editor missing %q", want)
		}
	}
}

func (f *keyFixture) printerName(t *testing.T) string {
	t.Helper()
	p, err := db.Printers.GetPrinterByID(context.Background(), f.printerID)
	if err != nil {
		t.Fatal(err)
	}
	return p.Name
}

func TestUnknownIDsAre404NotEmpty200(t *testing.T) {
	s := newUIServer(t)
	for _, path := range []string{"/printers/99999/edit-modal", "/templates/99999/edit-modal", "/jobs/99999/detail-modal", "/ui/printers/99999/status"} {
		if w := s.get(path, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, w.Code)
		}
	}
}

// A page button that POSTs to the JSON API through HTMX gets the refreshed HTML
// fragment it targets; a plain API call keeps the JSON reply.
func TestHTMXMutationReturnsFragment(t *testing.T) {
	s := newUIServer(t)
	f := s.fix
	code, out := f.post(t, f.job("HX-1", ""), nil)
	if code != http.StatusCreated {
		t.Fatalf("seed: %d %v", code, out)
	}
	id := int64(out["id"].(float64))

	post := func(path string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(s.cookie)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.r.ServeHTTP(w, req)
		return w
	}
	pause := fmt.Sprintf("/api/jobs/%d/pause", id)
	w := post(pause, map[string]string{"HX-Request": "true", "HX-Target": "queue-content"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), fmt.Sprintf(`data-job-id="%d"`, id)) ||
		strings.HasPrefix(strings.TrimSpace(w.Body.String()), "{") {
		t.Fatalf("HTMX pause did not return the queue fragment: %d %.300s", w.Code, w.Body.String())
	}
	resume := fmt.Sprintf("/api/jobs/%d/resume", id)
	w = post(resume, nil)
	if w.Code != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(w.Body.String()), "{") {
		t.Fatalf("plain API resume must stay JSON: %d %.200s", w.Code, w.Body.String())
	}
	// An error is passed through untouched so the page does not swap in a stale table.
	w = post("/api/jobs/99999/pause", map[string]string{"HX-Request": "true", "HX-Target": "queue-content"})
	if w.Code < 400 || !strings.HasPrefix(strings.TrimSpace(w.Body.String()), "{") {
		t.Fatalf("an HTMX error should keep the JSON error: %d %.200s", w.Code, w.Body.String())
	}
}

func TestLogoutClearsSessionAndRedirects(t *testing.T) {
	s := newUIServer(t)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(s.cookie)
	w := httptest.NewRecorder()
	s.r.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("logout: %d -> %q", w.Code, w.Header().Get("Location"))
	}
}
