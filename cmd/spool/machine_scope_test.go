package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orrn/spool/internal/db"
)

// ---- Finding 3: machine token scope ----

// cloudMachineRoutes is every call ORRN's SpoolClient makes to a legacy spool
// (packages/server/src/lib/spool-client.ts), plus the signed legacy print.
var cloudMachineRoutes = map[string]bool{
	"GET /api/printers":            true,
	"POST /api/printers":           true,
	"GET /api/printers/:id/status": true,
	"POST /api/printers/:id/test":  true,
	"GET /api/templates":           true,
	"POST /api/templates":          true,
	"PUT /api/templates/:id":       true,
	"POST /api/jobs":               true,
	"GET /api/jobs/:id":            true,
	"POST /api/jobs/:id/cancel":    true,
	"POST /api/jobs/:id/retry":     true,
	"GET /scale/reading":           true,
	"POST /print/:layout/:uid":     true,
}

// publicRoutes need no credential at all.
var publicRoutes = map[string]bool{
	"GET /health":             true,
	"GET /login":              true,
	"POST /login":             true,
	"POST /logout":            true,
	"POST /api/auth/login":    true,
	"POST /api/auth/logout":   true,
	"GET /api/auth/status":    true,
	"POST /api/auth/setup":    true,
	"GET /print/:layout/:uid": true,
}

func concretePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
			parts[i] = "1"
		}
	}
	return strings.Join(parts, "/")
}

func TestMachineTokenOnlyReachesCloudRoutes(t *testing.T) {
	s := newTestSpool(t)
	registered := map[string]bool{}
	for _, rt := range s.r.Routes() {
		registered[rt.Method+" "+rt.Path] = true
	}
	for want := range cloudMachineRoutes {
		if !registered[want] {
			t.Errorf("machine route %s is not registered", want)
		}
	}
	for _, rt := range s.r.Routes() {
		key := rt.Method + " " + rt.Path
		if publicRoutes[key] {
			continue
		}
		var body []byte
		if rt.Method != http.MethodGet && rt.Method != http.MethodDelete {
			body = []byte(`{}`)
		}
		w := s.machine(rt.Method, concretePath(rt.Path), body)
		switch {
		case cloudMachineRoutes[key] && (w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized):
			t.Errorf("%s: machine token refused with %d, ORRN needs this route", key, w.Code)
		case !cloudMachineRoutes[key] && w.Code != http.StatusForbidden:
			t.Errorf("%s: machine token got %d, want 403 (admin only)", key, w.Code)
		}
	}
}

func TestAdminSessionStillReachesAdminRoutes(t *testing.T) {
	s := newTestSpool(t)
	cookie := s.session(t)
	for _, path := range []string{"/api/settings", "/api/webhooks", "/api/ai/config", "/api/jobs", "/api/archives/stats"} {
		if w := s.browser(cookie, http.MethodGet, path, nil); w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
			t.Errorf("GET %s with an admin session: %d", path, w.Code)
		}
	}
}

// ORRN's calls work end to end with a machine token, not only get past the
// scope gate: each answers 2xx and returns or changes the seeded rows. The
// printer status and test print need a live printer manager, which the test
// spool does not have; the route sweep above covers their scope.
func TestMachineTokenCallsWorkEndToEnd(t *testing.T) {
	s := newTestSpool(t)
	ctx := context.Background()
	seq := atomic.AddInt64(&printerSeq, 1)
	printer := &db.Printer{Name: fmt.Sprintf("orrn-%s-%d", t.Name(), seq), IPAddress: fmt.Sprintf("10.78.%d.%d", seq/250, seq%250+1),
		Port: 9100, DPI: 203, LabelWidthMM: 50, LabelHeightMM: 30, GapMM: 2, Status: "online"}
	if err := db.Printers.CreatePrinter(ctx, printer); err != nil {
		t.Fatal(err)
	}
	schema := `{"name":"orrn","width_mm":50,"height_mm":30,"dpi":203,` +
		`"elements":[{"type":"text","x":10,"y":10,"content":"{{uid}}","font":"3"}],` +
		`"variables":{"uid":{"type":"string","required":true}}}`
	tmpl := &db.LabelTemplate{Name: fmt.Sprintf("orrn-%s-%d", t.Name(), seq), SchemaJSON: schema, WidthMM: 50, HeightMM: 30}
	if err := db.Templates.CreateTemplate(ctx, tmpl); err != nil {
		t.Fatal(err)
	}

	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		w := s.machine(method, path, raw)
		if w.Code != want {
			t.Fatalf("machine %s %s: %d %s, want %d", method, path, w.Code, w.Body.String(), want)
		}
		return w.Body.Bytes()
	}
	idOf := func(raw []byte) int64 {
		t.Helper()
		var v struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(raw, &v); err != nil || v.ID == 0 {
			t.Fatalf("no id in %s (%v)", raw, err)
		}
		return v.ID
	}

	if got := call(http.MethodGet, "/api/printers", nil, http.StatusOK); !bytes.Contains(got, []byte(printer.Name)) {
		t.Errorf("GET /api/printers does not list %s", printer.Name)
	}
	if got := call(http.MethodGet, "/api/templates", nil, http.StatusOK); !bytes.Contains(got, []byte(tmpl.Name)) {
		t.Errorf("GET /api/templates does not list %s", tmpl.Name)
	}

	created := idOf(call(http.MethodPost, "/api/templates", map[string]any{
		"name": tmpl.Name + "-new", "schema_json": schema, "width_mm": 50, "height_mm": 30,
	}, http.StatusCreated))
	call(http.MethodPut, fmt.Sprintf("/api/templates/%d", created), map[string]any{"description": "from ORRN"}, http.StatusOK)
	if got, err := db.Templates.GetTemplateByID(ctx, created); err != nil || got.Description != "from ORRN" {
		t.Errorf("PUT /api/templates/%d did not store the change: %+v %v", created, got, err)
	}

	jobBody := call(http.MethodPost, "/api/jobs", map[string]any{
		"printer_id": printer.ID, "template_id": tmpl.ID, "variables": map[string]string{"uid": "U-E2E"},
		"job_key": fmt.Sprintf("e2e-%d-%d", seq, time.Now().UnixNano()),
	}, http.StatusCreated)
	job := idOf(jobBody)
	if got := call(http.MethodGet, fmt.Sprintf("/api/jobs/%d", job), nil, http.StatusOK); idOf(got) != job {
		t.Errorf("GET /api/jobs/%d returned another job: %s", job, got)
	}
	call(http.MethodPost, fmt.Sprintf("/api/jobs/%d/cancel", job), nil, http.StatusOK)
	// Only a failed job can be retried; the test spool has no worker to fail it.
	if _, err := db.GetDB().Exec("UPDATE print_jobs SET status = 'failed' WHERE id = ?", job); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPost, fmt.Sprintf("/api/jobs/%d/retry", job), nil, http.StatusOK)
}
