package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/orrn/spool/internal/db"
)

// ---- Finding 2: GET /print/:layout/:uid ----

func seedLegacyTemplate(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	seq := atomic.AddInt64(&printerSeq, 1)
	name := fmt.Sprintf("legacy-%s-%d", t.Name(), seq)
	ip := fmt.Sprintf("10.77.%d.%d", seq/250, seq%250+1)
	p := &db.Printer{Name: fmt.Sprintf("p-%s-%d", t.Name(), seq), IPAddress: ip, Port: 9100, DPI: 203,
		LabelWidthMM: 50, LabelHeightMM: 30, GapMM: 2, Status: "online"}
	if err := db.Printers.CreatePrinter(ctx, p); err != nil {
		t.Fatal(err)
	}
	schema := `{"name":"legacy","width_mm":50,"height_mm":30,"dpi":203,
"elements":[{"type":"text","x":10,"y":10,"content":"{{uid}}","font":"3"}],
"variables":{"uid":{"type":"string","required":true}}}`
	if err := db.Templates.CreateTemplate(ctx, &db.LabelTemplate{Name: name, SchemaJSON: schema, WidthMM: 50, HeightMM: 30}); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestLegacyPrintGetQueuesNothing(t *testing.T) {
	s := newTestSpool(t)
	layout := seedLegacyTemplate(t)
	before := countJobs(t)

	// An <img src> or link on any page the operator opens sends exactly this.
	req := httptest.NewRequest(http.MethodGet, "/print/"+layout+"/U-1", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := s.do(req)
	if w.Code < 400 {
		t.Errorf("GET /print without auth: %d, want a refusal", w.Code)
	}
	// Even a signed-in browser must not print from a GET.
	cookie := s.session(t)
	if w := s.browser(cookie, http.MethodGet, "/print/"+layout+"/U-2", nil); w.Code < 400 {
		t.Errorf("GET /print with a session: %d, want a refusal", w.Code)
	}
	if after := countJobs(t); after != before {
		t.Fatalf("GET /print queued %d job(s)", after-before)
	}
}

func TestLegacyPrintPostNeedsAuth(t *testing.T) {
	s := newTestSpool(t)
	layout := seedLegacyTemplate(t)
	before := countJobs(t)

	w := s.do(httptest.NewRequest(http.MethodPost, "/print/"+layout+"/U-3", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("POST /print without auth: %d, want 401", w.Code)
	}
	if after := countJobs(t); after != before {
		t.Fatalf("unauthenticated POST /print queued %d job(s)", after-before)
	}

	cookie := s.session(t)
	if w := s.browser(cookie, http.MethodPost, "/print/"+layout+"/U-4", nil); w.Code != http.StatusOK {
		t.Errorf("POST /print with a session: %d %s, want 200", w.Code, w.Body.String())
	}
	if w := s.machine(http.MethodPost, "/print/"+layout+"/U-5", nil); w.Code != http.StatusOK {
		t.Errorf("POST /print with a machine token: %d %s, want 200", w.Code, w.Body.String())
	}
	if after := countJobs(t); after != before+2 {
		t.Fatalf("authenticated POST /print queued %d job(s), want 2", after-before)
	}
}
