package jobs

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/orrn/spool/internal/db"
)

func TestCommandResolutionAndJobEventsCommitTogether(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store, err := NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindIdentity(ctx, "deployment-events", "instance-events"); err != nil {
		t.Fatal(err)
	}
	resolution, err := store.ResolvePrintCommand(ctx, PrintCommandInput{
		CommandID: "command-test-print", Kind: "test_print", JobKey: "test:command-test-print",
		PayloadHash: "hash", PrinterID: "printer-test", TSPL: "CLS\r\n", Copies: 1,
	})
	if err != nil || resolution.Outcome != "accepted" {
		t.Fatalf("ResolvePrintCommand = %+v, %v", resolution, err)
	}
	events, err := store.ListPendingOutboxEvents(ctx, 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("resolution and job events = %+v, %v", events, err)
	}
	if events[0].Kind != "command_resolved" || events[0].Payload["kind"] != "test_print" || events[1].Kind != "job_state_changed" {
		t.Fatalf("unexpected event sequence: %+v", events)
	}
	var kind string
	if err := sqlDB.QueryRowContext(ctx, `SELECT kind FROM edge_commands WHERE command_id = ?`, "command-test-print").Scan(&kind); err != nil || kind != "test_print" {
		t.Fatalf("stored command kind = %q, %v", kind, err)
	}
	claimed, err := store.ClaimNextJob(ctx, "printer-test")
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNextJob = %+v, %v", claimed, err)
	}
	if err := store.MarkTransmitting(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSent(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTestPrintResult(ctx, claimed.ID, ""); err != nil {
		t.Fatal(err)
	}
	events, err = store.ListPendingOutboxEvents(ctx, 10)
	if err != nil || len(events) != 6 || events[5].Kind != "test_print_result" {
		t.Fatalf("terminal test print events = %+v, %v", events, err)
	}
}

func TestDeviceActionResultIsDurable(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store, err := NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := store.ResolvePrintCommand(ctx, PrintCommandInput{
		CommandID: "command-action", Kind: "device_action", JobKey: "action:command-action",
		PayloadHash: "hash", PrinterID: "printer-test", TSPL: "SET CUTTER ON\r\n", Copies: 1,
	})
	if err != nil || resolution.Outcome != "accepted" {
		t.Fatalf("ResolvePrintCommand = %+v, %v", resolution, err)
	}
	claimed, err := store.ClaimNextJob(ctx, "printer-test")
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNextJob = %+v, %v", claimed, err)
	}
	if err := store.MarkTransmitting(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSent(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTestPrintResult(ctx, claimed.ID, ""); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListPendingOutboxEvents(ctx, 10)
	if err != nil || len(events) != 6 || events[5].Kind != "device_action_result" || events[5].Payload["commandId"] != "command-action" {
		t.Fatalf("device action events = %+v, %v", events, err)
	}
}

func TestInvalidCommandResolutionIsDurableAndReplayable(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store, err := NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ResolveInvalidCommand(ctx, "command-unknown", "future_kind", "unsupported")
	if err != nil || first.Outcome != "rejected" {
		t.Fatalf("ResolveInvalidCommand = %+v, %v", first, err)
	}
	second, err := store.ResolveInvalidCommand(ctx, "command-unknown", "future_kind", "different detail must not replace original")
	if err != nil || second != first {
		t.Fatalf("replayed invalid resolution = %+v, want %+v (%v)", second, first, err)
	}
	events, err := store.ListPendingOutboxEvents(ctx, 10)
	if err != nil || len(events) != 1 || events[0].Kind != "command_resolved" || events[0].Payload["resolution"] != "invalid_payload" {
		t.Fatalf("invalid command event = %+v, %v", events, err)
	}
}
