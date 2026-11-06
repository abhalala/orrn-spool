package main

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/orrn/spool/internal/db"
)

// ---- Finding 1: SQL injection through sort_by / sort_dir ----

func TestJobListSortRefusesSQL(t *testing.T) {
	s := newTestSpool(t)
	cookie := s.session(t)
	ctx := context.Background()
	if err := db.Settings.SetSetting(ctx, "sort_canary", "untouched", false); err != nil {
		t.Fatal(err)
	}
	payloads := []struct{ by, dir string }{
		{"created_at DESC; UPDATE settings SET value='owned' WHERE key='sort_canary'; --", ""},
		{"created_at", "DESC; UPDATE settings SET value='owned' WHERE key='sort_canary'; --"},
		{"(SELECT value FROM settings WHERE key='jwt_secret')", ""},
		{"CASE WHEN (SELECT substr(value,1,1) FROM settings WHERE key='admin_password')='$' THEN id ELSE created_at END", ""},
		{"id--", ""},
		{"id, created_at", ""},
		{"created_at", "ASC NULLS FIRST"},
		{"created_at", "asc --"},
		{"priority DESC, created_at", ""},
	}
	for _, p := range payloads {
		q := url.Values{"sort_by": {p.by}, "sort_dir": {p.dir}}
		w := s.browser(cookie, http.MethodGet, "/api/jobs?"+q.Encode(), nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("sort_by=%q sort_dir=%q: %d, want 400", p.by, p.dir, w.Code)
		}
	}
	if got := setting(t, "sort_canary"); got != "untouched" {
		t.Fatalf("an injected statement ran: sort_canary = %q", got)
	}
	for _, q := range []string{"", "?sort_by=id&sort_dir=asc", "?sort_by=created_at&sort_dir=DESC", "?sort_by=priority"} {
		if w := s.browser(cookie, http.MethodGet, "/api/jobs"+q, nil); w.Code != http.StatusOK {
			t.Errorf("GET /api/jobs%s: %d %s, want 200", q, w.Code, w.Body.String())
		}
	}
}
