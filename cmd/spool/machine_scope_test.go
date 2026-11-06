package main

import (
	"net/http"
	"strings"
	"testing"
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
