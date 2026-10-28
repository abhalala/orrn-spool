package jobs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/orrn/spool/internal/db"
)

func TestOutboxRetryScheduleSurvivesStoreRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "edge.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindIdentity(ctx, "deployment-outbox", "instance-outbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolvePrintCommand(ctx, PrintCommandInput{
		CommandID: "command-outbox", JobKey: "job-outbox", PayloadHash: "hash", PrinterID: "printer-outbox", TSPL: "CLS\r\n", Copies: 1,
	}); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListPendingOutboxEvents(ctx, 10)
	if err != nil || len(events) == 0 {
		t.Fatalf("initial events = %+v, %v", events, err)
	}
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.EventID)
	}
	if err := store.ScheduleOutboxRetry(ctx, ids, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.ListPendingOutboxEvents(ctx, 10); err != nil || len(pending) != 0 {
		t.Fatalf("future events eligible early: %+v, %v", pending, err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	sqlDB, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store, err = NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := store.ListPendingOutboxEvents(ctx, 10); err != nil || len(pending) != 0 {
		t.Fatalf("restart discarded outbox schedule: %+v, %v", pending, err)
	}
	if err := store.ScheduleOutboxRetry(ctx, ids, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListPendingOutboxEvents(ctx, 10)
	if err != nil || len(pending) != len(ids) {
		t.Fatalf("eligible events missing after restart: %+v, %v", pending, err)
	}
	for _, event := range pending {
		if event.AttemptCount != 2 || event.CreatedAt == "" {
			t.Fatalf("outbox delivery metadata = %+v", event)
		}
	}
}

func TestRetryScheduleSurvivesStoreRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "edge.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindIdentity(ctx, "deployment-retry", "instance-retry"); err != nil {
		t.Fatal(err)
	}
	resolution, err := store.ResolvePrintCommand(ctx, PrintCommandInput{
		CommandID: "command-retry", JobKey: "job-retry", PayloadHash: "hash", PrinterID: "printer-retry", TSPL: "CLS\r\n", Copies: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimNextJob(ctx, "printer-retry")
	if err != nil || claimed == nil {
		t.Fatalf("initial claim = %+v, %v", claimed, err)
	}
	if err := store.MarkFailedRetryable(ctx, claimed.ID, "connection refused", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if job, err := store.ClaimNextJob(ctx, "printer-retry"); err != nil || job != nil {
		t.Fatalf("future retry claimed early: %+v, %v", job, err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	sqlDB, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store, err = NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if job, err := store.ClaimNextJob(ctx, "printer-retry"); err != nil || job != nil {
		t.Fatalf("restart discarded retry schedule: %+v, %v", job, err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE edge_jobs SET next_attempt_at = ? WHERE id = ?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), resolution.AgentJobID); err != nil {
		t.Fatal(err)
	}
	if job, err := store.ClaimNextJob(ctx, "printer-retry"); err != nil || job == nil || job.ID != resolution.AgentJobID {
		t.Fatalf("eligible retry was not claimed after restart: %+v, %v", job, err)
	}
}
