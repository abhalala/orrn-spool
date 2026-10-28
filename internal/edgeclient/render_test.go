package edgeclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/node"
)

const fourcubesSchema = `{"name":"Bundle","width_mm":102.25,"height_mm":76.2,"gap_mm":3,"dpi":203,
"elements":[{"type":"text","x":20,"y":20,"content":"{{serial}}","font":"3","x_scale":1,"y_scale":1},
{"type":"barcode","x":20,"y":80,"content":"{{serial}}","symbology":"128","height":60,"narrow":2,"wide":2}],
"variables":{"serial":{"type":"string","required":true}}}`

func TestRenderPrintJobFreezesDecimalSizeBarcodeAndCopies(t *testing.T) {
	tspl, err := renderPrintJob(printJobPayload{
		TemplateSchema: json.RawMessage(fourcubesSchema),
		Variables:      map[string]any{"serial": "26I4086"},
		Copies:         3,
	})
	if err != nil {
		t.Fatalf("renderPrintJob: %v", err)
	}
	for _, want := range []string{
		"SIZE 102.25 mm, 76.20 mm\r\n",
		`BARCODE 20,80,"128",60,1,0,2,2,"26I4086"`,
		"PRINT 1,3\r\n",
	} {
		if !strings.Contains(tspl, want) {
			t.Fatalf("rendered TSPL missing %q:\n%s", want, tspl)
		}
	}
	if _, err := renderPrintJob(printJobPayload{TemplateSchema: json.RawMessage(fourcubesSchema)}); err == nil {
		t.Fatal("expected missing required variable to fail rendering")
	}
}

func TestPollAppliesPrinterConfigRendersPrintJobsAndRejectsUnknownPrinters(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var acks []CommandAckRequest
	commands := []map[string]any{
		{
			"commandId": "019a0000-0000-7000-8000-0000000000c1", "leaseId": "019a0000-0000-7000-8000-0000000000l1",
			"kind": "print_job", "jobKey": "bundle:26I4086:label:v1:attempt:1", "payloadHash": strings.Repeat("a", 64),
			"payload": map[string]any{
				"printerId": "tsc-1", "templateSchema": json.RawMessage(fourcubesSchema),
				"variables": map[string]any{"serial": "26I4086"}, "copies": 1,
			},
		},
		{
			"commandId": "019a0000-0000-7000-8000-0000000000c2", "leaseId": "019a0000-0000-7000-8000-0000000000l2",
			"kind": "print_job", "jobKey": "bundle:26I4087:label:v1:attempt:1", "payloadHash": strings.Repeat("b", 64),
			"payload": map[string]any{
				"printerId": "missing", "templateSchema": json.RawMessage(fourcubesSchema),
				"variables": map[string]any{"serial": "26I4087"}, "copies": 1,
			},
		},
		{
			"commandId": "019a0000-0000-7000-8000-0000000000c3", "leaseId": "019a0000-0000-7000-8000-0000000000l3",
			"kind": "test_print", "payload": map[string]any{"printerId": "tsc-1"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/edge/v1/commands/poll":
			_ = json.NewEncoder(w).Encode(map[string]any{"configVersion": 4, "pendingCredentialVersion": nil, "commands": commands})
		case "/api/edge/v1/config":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"protocolVersion":     1,
				"configSchemaVersion": 1,
				"deploymentId":        "019a0000-0000-7000-8000-000000000001",
				"instanceId":          "019a0000-0000-7000-8000-000000000002",
				"configVersion":       4,
				"printers":            []any{map[string]any{"printerId": "tsc-1", "name": "Packing", "host": "20.0.0.182", "port": 9100}},
				"profiles":            []any{},
				"templates":           []any{},
			})
		case "/api/edge/v1/commands/ack":
			var ack CommandAckRequest
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &ack)
			mu.Lock()
			acks = append(acks, ack)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/edge/v1/events":
			_, _ = w.Write([]byte(`{"processed":0}`))
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	store := node.NewStore(filepath.Join(dir, "node.json"))
	_ = store.Save(node.State{
		ProtocolVersion: 1,
		DeploymentID:    "019a0000-0000-7000-8000-000000000001",
		InstanceID:      "019a0000-0000-7000-8000-000000000002",
		APIOrigin:       srv.URL,
		Active:          node.Credential{Version: 1, Secret: "secret-v1-0000000000000000000000000000"},
	})
	sqlDB, err := db.Open(filepath.Join(dir, "edge.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()
	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatalf("jobs.NewStore: %v", err)
	}

	client := New(store, "1.0.0", srv.Client()).WithStores(jobStore, nil)
	if err := client.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	if len(acks) != 3 {
		t.Fatalf("acks = %+v", acks)
	}
	if acks[0].Outcome != "accepted" || acks[0].AgentJobID == nil {
		t.Fatalf("print_job ack = %+v", acks[0])
	}
	if acks[1].Outcome != "rejected" || acks[1].Resolution != "invalid_payload" || acks[1].Detail == nil || !strings.Contains(*acks[1].Detail, "missing") {
		t.Fatalf("unknown printer ack = %+v", acks[1])
	}
	if acks[2].Outcome != "accepted" {
		t.Fatalf("test_print ack = %+v", acks[2])
	}

	job, err := jobStore.GetJob(ctx, *acks[0].AgentJobID)
	if err != nil || !strings.Contains(job.TSPL, `"26I4086"`) || job.PrinterID != "tsc-1" {
		t.Fatalf("stored job = %+v %v", job, err)
	}

	version, raw, err := jobStore.LoadAppliedConfig(ctx)
	if err != nil || version != 4 || !strings.Contains(string(raw), "20.0.0.182") || strings.Contains(string(raw), "secret") {
		t.Fatalf("applied config = %d %s %v", version, raw, err)
	}

	restarted := New(store, "1.0.0", srv.Client()).WithStores(jobStore, nil)
	if err := restarted.RestoreConfig(ctx); err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}
	if got := restarted.Printers(); len(got) != 1 || got[0].Host != "20.0.0.182" || restarted.configVersion() != 4 {
		t.Fatalf("restored printers = %+v", got)
	}
}
