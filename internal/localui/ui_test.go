package localui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orrn/spool/internal/cache"
	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/jobs"
)

func TestEmbeddedUIAndRedactedReadViews(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(ctx, sqlDB, jobStore, cacheStore)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.BootstrapAdmin(ctx, "admin-password"); err != nil {
		t.Fatal(err)
	}
	if err := srv.CreateFallbackUser(ctx, "operator", "1234", RolePrinter); err != nil {
		t.Fatal(err)
	}
	fallback := srv.FallbackHandler()

	loginPage := httptest.NewRecorder()
	fallback.ServeHTTP(loginPage, httptest.NewRequest(http.MethodGet, "/fallback/login", nil))
	if loginPage.Code != http.StatusOK || !strings.Contains(loginPage.Body.String(), `data-scope="fallback"`) || !strings.Contains(loginPage.Body.String(), "/assets/edge/login.js") {
		t.Fatalf("functional login page not served: %d %s", loginPage.Code, loginPage.Body.String())
	}
	asset := httptest.NewRecorder()
	fallback.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/edge/fallback.js", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "Offline reprint queued") {
		t.Fatalf("embedded fallback asset not served: %d", asset.Code)
	}

	loginBody := bytes.NewBufferString(`{"username":"operator","password":"1234"}`)
	loginRequest := httptest.NewRequest(http.MethodPost, "/fallback/login", loginBody)
	loginRequest.Header.Set("Content-Type", "application/json")
	login := httptest.NewRecorder()
	fallback.ServeHTTP(login, loginRequest)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("fallback session cookie is not SameSite=Strict: %+v", cookies)
	}
	authenticated := func(method, path string) *http.Request {
		request := httptest.NewRequest(method, path, nil)
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		return request
	}

	accepted, err := jobStore.ResolvePrintCommand(ctx, jobs.PrintCommandInput{
		CommandID: "019a0000-0000-7000-8000-000000000091", JobKey: "job-safe-view",
		PayloadHash: "hash", PrinterID: "tsc-1", TSPL: "CLOUD-LABEL-SECRET", Copies: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	localJobs := httptest.NewRecorder()
	fallback.ServeHTTP(localJobs, authenticated(http.MethodGet, "/fallback/jobs"))
	if localJobs.Code != http.StatusOK || !strings.Contains(localJobs.Body.String(), "job-safe-view") {
		t.Fatalf("local jobs view: %d %s", localJobs.Code, localJobs.Body.String())
	}
	if strings.Contains(localJobs.Body.String(), "CLOUD-LABEL-SECRET") || strings.Contains(localJobs.Body.String(), `"tspl"`) {
		t.Fatalf("local jobs exposed printable payload: %s", localJobs.Body.String())
	}
	markJobSentForTest(t, jobStore, accepted.AgentJobID)

	applyTestSection(t, ctx, cacheStore, "orders", []cache.ChunkRecordItem{{
		EntityID: "order-1", SortKey: "SO-1", DTO: json.RawMessage(`{"orderNumber":"SO-1","price":999,"ratePaise":1200,"sellingRate":12,"labelVariables":{"customerSecret":"do-not-expose"},"variables":{"address":"do-not-expose"},"nested":{"unit_price":50},"customerName":"Safe display"}`),
	}}, []any{})
	orders := httptest.NewRecorder()
	fallback.ServeHTTP(orders, authenticated(http.MethodGet, "/fallback/orders"))
	body := orders.Body.String()
	if orders.Code != http.StatusOK || !strings.Contains(body, "Cached as of 2026-10-26T12:00:00Z") || !strings.Contains(body, "Safe display") {
		t.Fatalf("orders cached read: %d %s", orders.Code, body)
	}
	for _, forbidden := range []string{"do-not-expose", "labelVariables", "unit_price", `"price"`, "ratePaise", "sellingRate", "999", "1200"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("orders response exposed %q: %s", forbidden, body)
		}
	}

	page := httptest.NewRecorder()
	fallback.ServeHTTP(page, authenticated(http.MethodGet, "/fallback/"))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Cloud print history") || !strings.Contains(page.Body.String(), "Read-only plant fallback") {
		t.Fatalf("fallback application page: %d %s", page.Code, page.Body.String())
	}
}

func TestAdminPagesAndLocalTestPrint(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(ctx, sqlDB, jobStore, cacheStore)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.BootstrapAdmin(ctx, "admin-password"); err != nil {
		t.Fatal(err)
	}
	admin := srv.AdminHandler()
	request := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(`{"username":"admin","password":"admin-password"}`))
	request.Host = "127.0.0.1:8081"
	request.Header.Set("Content-Type", "application/json")
	login := httptest.NewRecorder()
	admin.ServeHTTP(login, request)
	if login.Code != http.StatusOK {
		t.Fatalf("admin login: %d %s", login.Code, login.Body.String())
	}
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(login.Body.Bytes(), &session)
	cookies := login.Result().Cookies()
	authenticated := func(method, path string) *http.Request {
		req := httptest.NewRequest(method, path, nil)
		req.Host = "127.0.0.1:8081"
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		return req
	}
	page := httptest.NewRecorder()
	admin.ServeHTTP(page, authenticated(http.MethodGet, "/admin"))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Fallback users") || !strings.Contains(page.Body.String(), "Diagnostics") {
		t.Fatalf("admin application page: %d %s", page.Code, page.Body.String())
	}
	userRequest := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(`{"username":"viewer2","password":"1234","role":"viewer"}`))
	userRequest.Host = "127.0.0.1:8081"
	userRequest.Header.Set("Content-Type", "application/json")
	userRequest.Header.Set("X-CSRF-Token", session.CSRFToken)
	userRequest.Header.Set("Origin", "http://127.0.0.1:8081")
	for _, cookie := range cookies {
		userRequest.AddCookie(cookie)
	}
	createdUser := httptest.NewRecorder()
	admin.ServeHTTP(createdUser, userRequest)
	if createdUser.Code != http.StatusCreated {
		t.Fatalf("create fallback user: %d %s", createdUser.Code, createdUser.Body.String())
	}
	users := httptest.NewRecorder()
	admin.ServeHTTP(users, authenticated(http.MethodGet, "/admin/users"))
	if users.Code != http.StatusOK || !strings.Contains(users.Body.String(), "viewer2") || strings.Contains(users.Body.String(), "password") {
		t.Fatalf("list fallback users: %d %s", users.Code, users.Body.String())
	}

	_, err = sqlDB.ExecContext(ctx, `INSERT INTO edge_config_state (id, applied_config_version, config_json, updated_at) VALUES (1, 7, ?, '2026-10-26T12:00:00Z')`, `{"configVersion":7,"printers":[{"printerId":"tsc-1","name":"Packing","host":"192.0.2.20","port":9100,"dpi":203,"labelWidthMm":100,"labelHeightMm":75,"gapMm":3}]}`)
	if err != nil {
		t.Fatal(err)
	}
	// The admin listener exposes printer tests through its admin-only route set.
	testRequest := authenticated(http.MethodPost, "/admin/printers/tsc-1/test")
	testRequest.Header.Set("X-CSRF-Token", session.CSRFToken)
	testRequest.Header.Set("Origin", "http://127.0.0.1:8081")
	created := httptest.NewRecorder()
	admin.ServeHTTP(created, testRequest)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), `"tspl"`) {
		t.Fatalf("local test print: %d %s", created.Code, created.Body.String())
	}
	var response struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	job, err := jobStore.GetJob(ctx, response.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(job.TSPL, "TEST LABEL") || job.PrinterID != "tsc-1" {
		t.Fatalf("unexpected test job: %+v", job)
	}
	diagnostics := httptest.NewRecorder()
	admin.ServeHTTP(diagnostics, authenticated(http.MethodGet, "/admin/diagnostics"))
	if diagnostics.Code != http.StatusOK || !strings.Contains(diagnostics.Body.String(), "local-test:") || strings.Contains(diagnostics.Body.String(), "TEST LABEL") {
		t.Fatalf("redacted diagnostics: %d %s", diagnostics.Code, diagnostics.Body.String())
	}
}
