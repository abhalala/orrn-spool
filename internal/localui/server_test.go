package localui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orrn/spool/internal/cache"
	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/node"
)

func TestFallbackListenerExposesNoAdminRoutesAndEnforcesReprintEligibility(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()

	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatalf("jobs.NewStore: %v", err)
	}
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatalf("cache.NewStore: %v", err)
	}
	srv, err := NewServer(ctx, sqlDB, jobStore, cacheStore)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.BootstrapAdmin(ctx, "admin-pass-123456"); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	if err := srv.CreateFallbackUser(ctx, "operator1", "printer-pin-1234", RolePrinter); err != nil {
		t.Fatalf("CreateFallbackUser: %v", err)
	}
	if err := srv.CreateFallbackUser(ctx, "viewer1", "viewer-pin-1234", RoleViewer); err != nil {
		t.Fatalf("CreateFallbackUser viewer: %v", err)
	}

	// Admin route must 404 on the LAN fallback mux
	req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(""))
	w := httptest.NewRecorder()
	srv.FallbackHandler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for /admin/users on fallback listener, got %d", w.Code)
	}

	// Unauthenticated fallback read must redirect/401
	reqStock := httptest.NewRequest(http.MethodGet, "/fallback/stock", nil)
	wStock := httptest.NewRecorder()
	srv.FallbackHandler().ServeHTTP(wStock, reqStock)
	if wStock.Code != http.StatusUnauthorized && wStock.Code != http.StatusSeeOther {
		t.Fatalf("expected 401/303 for unauthenticated /fallback/stock, got %d", wStock.Code)
	}

	applyTestSection(t, ctx, cacheStore, "stock", []cache.ChunkRecordItem{{
		EntityID: "b-1", SortKey: "26I4086", DTO: json.RawMessage(`{"serial":"26I4086"}`),
	}}, []any{})

	// Login as printer operator
	loginBody, _ := json.Marshal(map[string]string{"username": "operator1", "password": "printer-pin-1234"})
	reqLogin := httptest.NewRequest(http.MethodPost, "/fallback/login", bytes.NewReader(loginBody))
	reqLogin.Header.Set("Content-Type", "application/json")
	wLogin := httptest.NewRecorder()
	srv.FallbackHandler().ServeHTTP(wLogin, reqLogin)
	if wLogin.Code != http.StatusOK {
		t.Fatalf("fallback login failed: %d %s", wLogin.Code, wLogin.Body.String())
	}
	var loginResp struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(wLogin.Body.Bytes(), &loginResp)
	cookies := wLogin.Result().Cookies()

	reqStockAuth := httptest.NewRequest(http.MethodGet, "/fallback/stock", nil)
	for _, c := range cookies {
		reqStockAuth.AddCookie(c)
	}
	wStockAuth := httptest.NewRecorder()
	srv.FallbackHandler().ServeHTTP(wStockAuth, reqStockAuth)
	if wStockAuth.Code != http.StatusOK || !strings.Contains(wStockAuth.Body.String(), "Cached as of 2026-10-26T12:00:00Z") {
		t.Fatalf("expected Cached as of metadata, got %d: %s", wStockAuth.Code, wStockAuth.Body.String())
	}

	// Create an accepted job (not yet sent) -> reprint must be rejected (409)
	cmdRes, err := jobStore.ResolvePrintCommand(ctx, jobs.PrintCommandInput{
		CommandID:   "019a0000-0000-7000-8000-000000000010",
		JobKey:      "bundle:26I4086:label:v1:attempt:1",
		PayloadHash: "hash-1",
		PrinterID:   "tsc-1",
		TSPL:        "SIZE 102.25 mm, 76.20 mm\r\nCLS\r\nPRINT 1,1\r\n",
		Copies:      1,
	})
	if err != nil {
		t.Fatalf("ResolvePrintCommand: %v", err)
	}

	reqReprintActive := httptest.NewRequest(http.MethodPost, "/fallback/jobs/"+cmdRes.AgentJobID+"/reprint", nil)
	reqReprintActive.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
	reqReprintActive.Header.Set("Origin", "http://example.com")
	for _, c := range cookies {
		reqReprintActive.AddCookie(c)
	}
	wReprintActive := httptest.NewRecorder()
	srv.FallbackHandler().ServeHTTP(wReprintActive, reqReprintActive)
	if wReprintActive.Code != http.StatusConflict {
		t.Fatalf("expected 409 for active accepted job reprint, got %d: %s", wReprintActive.Code, wReprintActive.Body.String())
	}
	missingOrigin := httptest.NewRequest(http.MethodPost, "/fallback/jobs/"+cmdRes.AgentJobID+"/reprint", nil)
	missingOrigin.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
	for _, c := range cookies {
		missingOrigin.AddCookie(c)
	}
	missingOriginResponse := httptest.NewRecorder()
	srv.FallbackHandler().ServeHTTP(missingOriginResponse, missingOrigin)
	if missingOriginResponse.Code != http.StatusForbidden {
		t.Fatalf("mutation without Origin succeeded: %d", missingOriginResponse.Code)
	}

	// Move through device_open and transmitting before the sent terminal state.
	markJobSentForTest(t, jobStore, cmdRes.AgentJobID)
	if err := jobStore.ApplyConfig(ctx, 1, []byte(`{"configVersion":1,"printers":[{"printerId":"tsc-1","host":"127.0.0.1","port":9100}],"profiles":[],"templates":[]}`)); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	reqReprintSent := httptest.NewRequest(http.MethodPost, "/fallback/jobs/"+cmdRes.AgentJobID+"/reprint", nil)
	reqReprintSent.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
	reqReprintSent.Header.Set("Origin", "http://example.com")
	for _, c := range cookies {
		reqReprintSent.AddCookie(c)
	}
	wReprintSent := httptest.NewRecorder()
	srv.FallbackHandler().ServeHTTP(wReprintSent, reqReprintSent)
	if wReprintSent.Code != http.StatusCreated {
		t.Fatalf("expected 201 for sent job reprint, got %d: %s", wReprintSent.Code, wReprintSent.Body.String())
	}
}

type localUITestFixture struct {
	server    *Server
	db        *sql.DB
	jobs      *jobs.Store
	cache     *cache.Store
	nodeStore *node.Store
}

func newLocalUITestFixture(t *testing.T) localUITestFixture {
	t.Helper()
	ctx := context.Background()
	stateDir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(stateDir, "edge.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatalf("jobs.NewStore: %v", err)
	}
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatalf("cache.NewStore: %v", err)
	}
	server, err := NewServer(ctx, sqlDB, jobStore, cacheStore)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return localUITestFixture{
		server: server, db: sqlDB, jobs: jobStore, cache: cacheStore,
		nodeStore: node.NewStore(filepath.Join(stateDir, "node.json")),
	}
}

func canonicalTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode JSON: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var canonical any
	if err := decoder.Decode(&canonical); err != nil {
		t.Fatalf("decode JSON for canonicalization: %v", err)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(canonical); err != nil {
		t.Fatalf("encode canonical JSON: %v", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
}

func applyTestSection(t *testing.T, ctx context.Context, store *cache.Store, section string, records []cache.ChunkRecordItem, summaries any) {
	t.Helper()
	if records == nil {
		records = []cache.ChunkRecordItem{}
	}
	snapshotID := "snap-" + section
	document := map[string]any{
		"protocolVersion": 1, "cacheSchemaVersion": cache.CacheSchemaVersion,
		"companyId": "comp-1", "section": section, "snapshotId": snapshotID,
		"sequence": 0, "recordCount": len(records), "records": records, "summaries": summaries,
	}
	unsigned := canonicalTestJSON(t, document)
	chunkDigest := sha256.Sum256(unsigned)
	chunkSHA := hex.EncodeToString(chunkDigest[:])
	document["sha256"] = chunkSHA
	raw := canonicalTestJSON(t, document)
	sectionInput := make([]byte, 0, len(unsigned)+2)
	sectionInput = append(sectionInput, '[')
	sectionInput = append(sectionInput, unsigned...)
	sectionInput = append(sectionInput, ']')
	sectionDigest := sha256.Sum256(sectionInput)
	err := store.ApplySection(ctx, cache.SectionMeta{
		Section: section, SnapshotID: snapshotID, CompanyID: "comp-1",
		GeneratedAt: "2026-10-26T12:00:00Z", ContentSHA256: hex.EncodeToString(sectionDigest[:]),
		RecordCount: len(records),
	}, []cache.ChunkPayload{{Sequence: 0, SHA256: chunkSHA, RecordCount: len(records), RawJSON: raw}})
	if err != nil {
		t.Fatalf("ApplySection(%s): %v", section, err)
	}
}

func saveValidTestIdentity(t *testing.T, store *node.Store, apiOrigin string) {
	t.Helper()
	if err := store.Save(node.State{
		ProtocolVersion: 1, DeploymentID: "deployment-1", InstanceID: "instance-1",
		InstallID: "install-1", NodeName: "edge-test", APIOrigin: apiOrigin,
		Active: node.Credential{Version: 1, Secret: "active-test-secret"},
	}); err != nil {
		t.Fatalf("save node identity: %v", err)
	}
}

func loginTestSession(t *testing.T, handler http.Handler, path, username, password string) ([]*http.Cookie, string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login %s: got %d: %s", path, response.Code, response.Body.String())
	}
	var payload struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return response.Result().Cookies(), payload.CSRFToken
}

func readinessForTest(t *testing.T, handler http.Handler, path string) (int, struct {
	Ready  bool                      `json:"ready"`
	Checks map[string]readinessState `json:"checks"`
}) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	var payload struct {
		Ready  bool                      `json:"ready"`
		Checks map[string]readinessState `json:"checks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode readiness response %d: %v: %s", response.Code, err, response.Body.String())
	}
	return response.Code, payload
}

func TestReadinessRequiresCompleteLocalStateAndIgnoresCloudOutage(t *testing.T) {
	ctx := context.Background()
	fixture := newLocalUITestFixture(t)
	fixture.server.WithNodeStore(fixture.nodeStore)
	cloudCalls := 0
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudCalls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	saveValidTestIdentity(t, fixture.nodeStore, cloud.URL)
	if _, err := fixture.jobs.BindIdentity(ctx, "deployment-1", "instance-1"); err != nil {
		t.Fatalf("BindIdentity: %v", err)
	}
	handler := fixture.server.FallbackHandler()

	status, report := readinessForTest(t, handler, "/healthz")
	if status != http.StatusServiceUnavailable || report.Checks["appliedConfig"].OK || report.Checks["cache"].OK {
		t.Fatalf("expected incomplete config/cache readiness failure, got %d: %+v", status, report)
	}
	if err := fixture.jobs.ApplyConfig(ctx, 1, []byte(`{"configVersion":1,"printers":[],"profiles":[],"templates":[]}`)); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	for _, section := range []string{"stock", "orders", "dies", "print_status"} {
		applyTestSection(t, ctx, fixture.cache, section, nil, []any{})
	}
	status, report = readinessForTest(t, handler, "/readyz")
	if status != http.StatusServiceUnavailable || report.Checks["cache"].Sections["company_format"].State != "missing" {
		t.Fatalf("expected missing company_format readiness failure, got %d: %+v", status, report)
	}
	applyTestSection(t, ctx, fixture.cache, "company_format", nil, []any{})
	status, report = readinessForTest(t, handler, "/healthz")
	if status != http.StatusOK || !report.Ready || !report.Checks["identity"].OK ||
		!report.Checks["database"].OK || !report.Checks["appliedConfig"].OK || !report.Checks["cache"].OK {
		t.Fatalf("expected fully local ready response, got %d: %+v", status, report)
	}
	if cloudCalls != 0 {
		t.Fatalf("readiness made %d cloud calls", cloudCalls)
	}

	cloud.Close()
	status, report = readinessForTest(t, handler, "/healthz")
	if status != http.StatusOK || !report.Ready {
		t.Fatalf("cloud outage changed local readiness: %d: %+v", status, report)
	}
	status, _ = readinessForTest(t, fixture.server.AdminHandler(), "/readyz")
	if status != http.StatusOK {
		t.Fatalf("admin readiness endpoint got %d", status)
	}
}

func TestRevokeLocalAccessWipesGrantsSessionsAndCacheButPreservesHistoryAndAdmin(t *testing.T) {
	ctx := context.Background()
	fixture := newLocalUITestFixture(t)
	fixture.server.WithNodeStore(fixture.nodeStore)
	saveValidTestIdentity(t, fixture.nodeStore, "https://api.invalid")
	if err := fixture.server.BootstrapAdmin(ctx, "admin-pass-123456"); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	if err := fixture.server.CreateFallbackUser(ctx, "operator", "1234", RolePrinter); err != nil {
		t.Fatalf("CreateFallbackUser: %v", err)
	}
	if _, err := fixture.server.AuthenticateUser(ctx, "admin", "admin-pass-123456", "admin"); err != nil {
		t.Fatalf("admin session: %v", err)
	}
	if _, err := fixture.server.AuthenticateUser(ctx, "operator", "1234", "fallback"); err != nil {
		t.Fatalf("fallback session: %v", err)
	}
	applyTestSection(t, ctx, fixture.cache, "stock", []cache.ChunkRecordItem{{
		EntityID: "stock-1", SortKey: "001", DTO: json.RawMessage(`{"serial":"A-1"}`),
	}}, []any{map[string]any{"label": "available", "value": 1}})
	resolution, err := fixture.jobs.ResolvePrintCommand(ctx, jobs.PrintCommandInput{
		CommandID: "019a0000-0000-7000-8000-000000000099", JobKey: "revocation-history-job",
		PayloadHash: "payload-hash", PrinterID: "printer-1", TSPL: "CLS\r\nPRINT 1,1\r\n", Copies: 1,
	})
	if err != nil {
		t.Fatalf("ResolvePrintCommand: %v", err)
	}
	markJobSentForTest(t, fixture.jobs, resolution.AgentJobID)
	var jobsBefore, outboxBefore int
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM edge_jobs`).Scan(&jobsBefore); err != nil {
		t.Fatalf("count jobs before revocation: %v", err)
	}
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM edge_outbox`).Scan(&outboxBefore); err != nil {
		t.Fatalf("count outbox before revocation: %v", err)
	}
	if err := fixture.nodeStore.MarkRevoked(); err != nil {
		t.Fatalf("MarkRevoked: %v", err)
	}
	if err := fixture.server.RevokeLocalAccess(ctx); err != nil {
		t.Fatalf("RevokeLocalAccess: %v", err)
	}

	for table, want := range map[string]int{
		"local_users": 1, "local_sessions": 0, "cache_sections": 0, "cache_records": 0,
		"edge_jobs": jobsBefore, "edge_outbox": outboxBefore,
	} {
		var got int
		if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s count after revocation: got %d, want %d", table, got, want)
		}
	}
	var adminName string
	if err := fixture.db.QueryRowContext(ctx, `SELECT username FROM local_users`).Scan(&adminName); err != nil || adminName != "admin" {
		t.Fatalf("admin grant was not preserved: %q, %v", adminName, err)
	}
	if _, err := fixture.server.AuthenticateUser(ctx, "operator", "1234", "fallback"); err == nil {
		t.Fatal("revoked fallback grant still authenticates")
	}

	fallback := fixture.server.FallbackHandler()
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/healthz", nil),
		httptest.NewRequest(http.MethodGet, "/fallback/login", nil),
		httptest.NewRequest(http.MethodPost, "/fallback/login", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodGet, "/fallback/stock", nil),
	} {
		response := httptest.NewRecorder()
		fallback.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"error":"node_revoked"`) {
			t.Fatalf("revoked fallback %s %s: got %d: %s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}

	admin := fixture.server.AdminHandler()
	cookies, _ := loginTestSession(t, admin, "/admin/login", "admin", "admin-pass-123456")
	request := httptest.NewRequest(http.MethodGet, "/admin/diagnostics", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	admin.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("admin diagnostics unavailable after revocation: %d: %s", response.Code, response.Body.String())
	}
}

type cachePageResponse struct {
	Total      int  `json:"total"`
	Limit      int  `json:"limit"`
	Offset     int  `json:"offset"`
	NextOffset *int `json:"nextOffset"`
	Records    []struct {
		EntityID string         `json:"entityId"`
		DTO      map[string]any `json:"dto"`
	} `json:"records"`
	Summaries []map[string]any `json:"summaries"`
}

func getCachePage(t *testing.T, handler http.Handler, cookies []*http.Cookie, query string) cachePageResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/fallback/stock?"+query, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("cache page %s: got %d: %s", query, response.Code, response.Body.String())
	}
	var payload cachePageResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode cache page: %v", err)
	}
	return payload
}

func TestCachedFallbackPagesReachEveryRecordAndDecodeSummaries(t *testing.T) {
	ctx := context.Background()
	fixture := newLocalUITestFixture(t)
	if err := fixture.server.CreateFallbackUser(ctx, "viewer", "1234", RoleViewer); err != nil {
		t.Fatalf("CreateFallbackUser: %v", err)
	}
	records := make([]cache.ChunkRecordItem, 205)
	for index := range records {
		serial := fmt.Sprintf("STOCK-%03d", index)
		records[index] = cache.ChunkRecordItem{
			EntityID: fmt.Sprintf("entity-%03d", index), SortKey: serial,
			DTO: json.RawMessage(fmt.Sprintf(`{"serial":%q,"password":"redact-me"}`, serial)),
		}
	}
	applyTestSection(t, ctx, fixture.cache, "stock", records, []any{map[string]any{"label": "available", "value": 205}})
	handler := fixture.server.FallbackHandler()
	cookies, _ := loginTestSession(t, handler, "/fallback/login", "viewer", "1234")

	pages := []cachePageResponse{
		getCachePage(t, handler, cookies, "limit=100&offset=0"),
		getCachePage(t, handler, cookies, "limit=100&offset=100"),
		getCachePage(t, handler, cookies, "limit=100&offset=200"),
	}
	wantLengths := []int{100, 100, 5}
	wantNext := []int{100, 200, 0}
	seen := make(map[string]bool, 205)
	for index, page := range pages {
		if page.Total != 205 || page.Limit != 100 || page.Offset != index*100 || len(page.Records) != wantLengths[index] {
			t.Fatalf("page %d metadata/records mismatch: %+v", index, page)
		}
		if index < 2 {
			if page.NextOffset == nil || *page.NextOffset != wantNext[index] {
				t.Fatalf("page %d nextOffset: got %v, want %d", index, page.NextOffset, wantNext[index])
			}
		} else if page.NextOffset != nil {
			t.Fatalf("final page has nextOffset %d", *page.NextOffset)
		}
		if len(page.Summaries) != 1 || page.Summaries[0]["label"] != "available" || page.Summaries[0]["value"] != float64(205) {
			t.Fatalf("page %d summaries were not decoded: %+v", index, page.Summaries)
		}
		for _, record := range page.Records {
			if seen[record.EntityID] {
				t.Fatalf("duplicate record across pages: %s", record.EntityID)
			}
			seen[record.EntityID] = true
			if _, exposed := record.DTO["password"]; exposed {
				t.Fatalf("sensitive DTO field exposed for %s", record.EntityID)
			}
		}
	}
	if len(seen) != 205 {
		t.Fatalf("pages reached %d records, want 205", len(seen))
	}
}

func markJobSentForTest(t *testing.T, store *jobs.Store, jobID string) {
	t.Helper()
	job, err := store.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before sent: %v", err)
	}
	claimed, err := store.ClaimNextJob(context.Background(), job.PrinterID)
	if err != nil || claimed == nil || claimed.ID != jobID {
		t.Fatalf("ClaimNextJob before sent: %+v %v", claimed, err)
	}
	if err := store.MarkTransmitting(context.Background(), jobID); err != nil {
		t.Fatalf("MarkTransmitting: %v", err)
	}
	if err := store.MarkSent(context.Background(), jobID); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
}
