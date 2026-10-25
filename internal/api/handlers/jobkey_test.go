package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/core"
	"github.com/orrn/spool/internal/db"
)

const keyTestSchema = `{"name":"bundle","width_mm":50,"height_mm":30,"dpi":203,
"elements":[{"type":"text","x":10,"y":10,"content":"{{serial}}","font":"3"}],
"variables":{"serial":{"type":"string","required":true}}}`

var keyIP int32

type keyFixture struct {
	r          *gin.Engine
	queue      *core.Queue
	printerID  int64
	templateID int64
}

func newKeyFixture(t *testing.T) *keyFixture {
	t.Helper()
	dbOnce.Do(func() {
		dir, err := os.MkdirTemp("", "spool-scale-test")
		if err != nil {
			dbErr = err
			return
		}
		dbErr = db.Init(db.Config{Path: filepath.Join(dir, "spool.db")})
	})
	if dbErr != nil {
		t.Fatal(dbErr)
	}
	ctx := context.Background()
	printer := &db.Printer{Name: "p-" + t.Name(), IPAddress: fmt.Sprintf("10.9.9.%d", atomic.AddInt32(&keyIP, 1)), Port: 9100, DPI: 203,
		LabelWidthMM: 50, LabelHeightMM: 30, GapMM: 2, Status: "online"}
	if err := db.Printers.CreatePrinter(ctx, printer); err != nil {
		t.Fatal(err)
	}
	if err := db.Printers.UpdatePrinterStatus(ctx, printer.ID, "online", nil); err != nil {
		t.Fatal(err)
	}
	tpl := &db.LabelTemplate{Name: "t-" + t.Name(), SchemaJSON: keyTestSchema, WidthMM: 50, HeightMM: 30}
	if err := db.Templates.CreateTemplate(ctx, tpl); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	queue := core.NewQueue(db.GetDB(), nil, nil, nil, nil)
	jobs := NewJobHandler(db.GetDB(), queue, core.NewTSPL2Generator())
	r := gin.New()
	jobs.RegisterRoutes(r.Group("/api"))
	return &keyFixture{r: r, queue: queue, printerID: printer.ID, templateID: tpl.ID}
}

func (f *keyFixture) post(t *testing.T, body map[string]any, header http.Header) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/jobs", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (f *keyFixture) job(serial, key string) map[string]any {
	m := map[string]any{
		"printer_id":  f.printerID,
		"template_id": f.templateID,
		"variables":   map[string]string{"serial": serial},
		"copies":      1,
	}
	if key != "" {
		m["job_key"] = key
	}
	return m
}

func countByKey(t *testing.T, key string) int {
	t.Helper()
	var n int
	if err := db.GetDB().QueryRow("SELECT COUNT(*) FROM print_jobs WHERE job_key = ?", key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDuplicateJobKeyReturnsFirstJobAndPrintsOnce(t *testing.T) {
	f := newKeyFixture(t)
	key := "bundle:B-1001:label:v3:attempt:1"

	code1, first := f.post(t, f.job("B-1001", key), nil)
	if code1 != http.StatusCreated || first["duplicate"] != false {
		t.Fatalf("first POST: %d %v", code1, first)
	}
	code2, second := f.post(t, f.job("B-1001", key), nil)
	if code2 != http.StatusOK || second["duplicate"] != true {
		t.Fatalf("second POST: %d %v", code2, second)
	}
	if first["id"] != second["id"] {
		t.Fatalf("duplicate must return the first job: %v vs %v", first["id"], second["id"])
	}
	if n := countByKey(t, key); n != 1 {
		t.Fatalf("want exactly one stored job for the key, got %d", n)
	}
}

func TestDuplicateJobKeyIsServedEvenIfPrinterIsNowPaused(t *testing.T) {
	f := newKeyFixture(t)
	key := "bundle:B-2002:label:v3:attempt:1"
	if code, out := f.post(t, f.job("B-2002", key), nil); code != http.StatusCreated {
		t.Fatalf("first POST: %d %v", code, out)
	}
	if err := db.Printers.UpdatePrinterStatus(context.Background(), f.printerID, "paused", nil); err != nil {
		t.Fatal(err)
	}
	code, out := f.post(t, f.job("B-2002", key), nil)
	if code != http.StatusOK || out["duplicate"] != true {
		t.Fatalf("a retry of an already-queued label must not be refused: %d %v", code, out)
	}
	// A different attempt is a new print and the paused printer still refuses it.
	if code, _ := f.post(t, f.job("B-2002", "bundle:B-2002:label:v3:attempt:2"), nil); code != http.StatusBadRequest {
		t.Fatalf("new key on a paused printer should be refused, got %d", code)
	}
}

func TestSameJobKeyWithDifferentLabelConflicts(t *testing.T) {
	f := newKeyFixture(t)
	key := "bundle:B-3003:label:v3:attempt:1"
	if code, _ := f.post(t, f.job("B-3003", key), nil); code != http.StatusCreated {
		t.Fatal("first POST failed")
	}
	code, out := f.post(t, f.job("B-9999", key), nil)
	if code != http.StatusConflict || out["error"] != "job_key_conflict" {
		t.Fatalf("want 409 job_key_conflict, got %d %v", code, out)
	}
	if n := countByKey(t, key); n != 1 {
		t.Fatalf("conflict must not store a second job, got %d", n)
	}
}

func TestNewAttemptKeyPrintsAgain(t *testing.T) {
	f := newKeyFixture(t)
	_, a := f.post(t, f.job("B-4004", "bundle:B-4004:label:v3:attempt:1"), nil)
	_, b := f.post(t, f.job("B-4004", "bundle:B-4004:label:v3:attempt:2"), nil)
	if a["id"] == b["id"] {
		t.Fatalf("a reprint with a new key must create a new job: %v", a["id"])
	}
}

func TestJobsWithoutKeyAreNotDeduplicated(t *testing.T) {
	f := newKeyFixture(t)
	_, a := f.post(t, f.job("B-5005", ""), nil)
	_, b := f.post(t, f.job("B-5005", ""), nil)
	if a["id"] == b["id"] {
		t.Fatalf("keyless submissions are independent jobs")
	}
}

func TestIdempotencyKeyHeaderIsAccepted(t *testing.T) {
	f := newKeyFixture(t)
	h := http.Header{"Idempotency-Key": []string{"hdr:B-6006:1"}}
	_, a := f.post(t, f.job("B-6006", ""), h)
	code, b := f.post(t, f.job("B-6006", ""), h)
	if code != http.StatusOK || a["id"] != b["id"] || b["duplicate"] != true {
		t.Fatalf("header key not honoured: %d %v %v", code, a, b)
	}
}

func TestInvalidJobKeyIsRejected(t *testing.T) {
	f := newKeyFixture(t)
	for _, bad := range []string{"has space", "new\nline", string(bytes.Repeat([]byte("a"), 201)), "emoji-☃"} {
		code, out := f.post(t, f.job("B-7007", bad), nil)
		if code != http.StatusBadRequest || out["error"] != "invalid_job_key" {
			t.Errorf("key %q: want 400 invalid_job_key, got %d %v", bad, code, out)
		}
	}
}

func TestConcurrentDuplicatePostsStoreOneJob(t *testing.T) {
	f := newKeyFixture(t)
	key := "bundle:B-8008:label:v3:attempt:1"
	const n = 16
	ids := make([]any, n)
	created := make([]bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code, out := f.post(t, f.job("B-8008", key), nil)
			if code != http.StatusCreated && code != http.StatusOK {
				t.Errorf("POST %d: %d %v", i, code, out)
			}
			ids[i] = out["id"]
			created[i] = code == http.StatusCreated
		}(i)
	}
	wg.Wait()
	creates := 0
	for i := range ids {
		if ids[i] != ids[0] {
			t.Fatalf("all replies must name the same job: %v", ids)
		}
		if created[i] {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("exactly one POST may create the job, got %d", creates)
	}
	if got := countByKey(t, key); got != 1 {
		t.Fatalf("want one stored job, got %d", got)
	}
}

func TestQuickPrintHonoursJobKey(t *testing.T) {
	f := newKeyFixture(t)
	tpl := NewTemplateHandler(db.GetDB(), core.NewTSPL2Generator(), f.queue)
	r := gin.New()
	RegisterTemplateRoutes(r.Group("/api"), tpl)
	send := func() (int, map[string]any) {
		raw, _ := json.Marshal(map[string]any{
			"printer_id": f.printerID, "variables": map[string]string{"serial": "B-9009"},
			"job_key": "quick:B-9009:1",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/templates/"+itoa(f.templateID)+"/print", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	c1, a := send()
	c2, b := send()
	if c1 != http.StatusAccepted || c2 != http.StatusOK || a["job_id"] != b["job_id"] {
		t.Fatalf("quick print: %d %v / %d %v", c1, a, c2, b)
	}
	if n := countByKey(t, "quick:B-9009:1"); n != 1 {
		t.Fatalf("want one job, got %d", n)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
