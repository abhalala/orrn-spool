package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/orrn/spool/internal/db"
)

func TestStoreDeduplicatesCommandsRejectsConflictsAndRecoversTransmittingAsOutcomeUnknown(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()

	s, err := NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 1. Accept print_job command
	res1, err := s.ResolvePrintCommand(ctx, PrintCommandInput{
		CommandID:   "019a0000-0000-7000-8000-000000000001",
		JobKey:      "bundle:26I4086:label:v1:attempt:1",
		PayloadHash: "hash-a",
		PrinterID:   "tsc-1",
		TSPL:        "SIZE 102.25 mm, 76.20 mm\r\nCLS\r\nPRINT 1,1\r\n",
		Copies:      1,
	})
	if err != nil {
		t.Fatalf("ResolvePrintCommand: %v", err)
	}
	if res1.Outcome != "accepted" || res1.AgentJobID == "" {
		t.Fatalf("unexpected res1: %+v", res1)
	}

	// 2. Redelivery of same commandId returns identical resolution without creating second job
	resReplay, err := s.ResolvePrintCommand(ctx, PrintCommandInput{
		CommandID:   "019a0000-0000-7000-8000-000000000001",
		JobKey:      "bundle:26I4086:label:v1:attempt:1",
		PayloadHash: "hash-a",
		PrinterID:   "tsc-1",
		TSPL:        "SIZE 102.25 mm, 76.20 mm\r\nCLS\r\nPRINT 1,1\r\n",
		Copies:      1,
	})
	if err != nil || resReplay.AgentJobID != res1.AgentJobID {
		t.Fatalf("replay mismatch: %+v vs %+v (err=%v)", resReplay, res1, err)
	}

	// 3. Different commandId with same job_key but different payloadHash is durably rejected
	resConflict, err := s.ResolvePrintCommand(ctx, PrintCommandInput{
		CommandID:   "019a0000-0000-7000-8000-000000000002",
		JobKey:      "bundle:26I4086:label:v1:attempt:1",
		PayloadHash: "hash-different",
		PrinterID:   "tsc-1",
		TSPL:        "SIZE 102.25 mm, 76.20 mm\r\nCLS\r\nPRINT 2,1\r\n",
		Copies:      2,
	})
	if err != nil {
		t.Fatalf("ResolvePrintCommand conflict: %v", err)
	}
	if resConflict.Outcome != "rejected" || resConflict.Resolution != "job_key_conflict" {
		t.Fatalf("expected job_key_conflict rejection, got %+v", resConflict)
	}

	// 4. Claim and move to transmitting, then simulate crash recovery
	claimed, err := s.ClaimNextJob(ctx, "tsc-1")
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNextJob: %v %+v", err, claimed)
	}
	if err := s.MarkTransmitting(ctx, claimed.ID); err != nil {
		t.Fatalf("MarkTransmitting: %v", err)
	}
	// Cancel during transmitting must be rejected
	cancelRes, err := s.ResolveCancelCommand(ctx, "019a0000-0000-7000-8000-000000000003", claimed.ID)
	if err != nil || cancelRes.Outcome != "rejected" || cancelRes.Resolution != "transmission_started" {
		t.Fatalf("expected transmission_started rejection, got %+v (err=%v)", cancelRes, err)
	}

	// Startup crash recovery must turn transmitting into outcome_unknown (never back to accepted)
	if err := s.RecoverAfterCrash(ctx); err != nil {
		t.Fatalf("RecoverAfterCrash: %v", err)
	}
	job, err := s.GetJob(ctx, claimed.ID)
	if err != nil || job.State != StateOutcomeUnknown {
		t.Fatalf("expected outcome_unknown after crash, got %+v (err=%v)", job, err)
	}

	// 5. Offline reprint requires an intact destination and creates a linked job.
	if _, err := s.CreateOfflineReprint(ctx, claimed.ID, "operator1"); !errors.Is(err, ErrJobNotReprintable) {
		t.Fatalf("reprint without destination config = %v, want ErrJobNotReprintable", err)
	}
	if err := s.ApplyConfig(ctx, 1, []byte(`{"configVersion":1,"printers":[{"printerId":"tsc-1","host":"127.0.0.1","port":9100}],"profiles":[],"templates":[]}`)); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	reprint, err := s.CreateOfflineReprint(ctx, claimed.ID, "operator1")
	if err != nil {
		t.Fatalf("CreateOfflineReprint: %v", err)
	}
	if reprint.SourceJobID != claimed.ID || reprint.State != StateAccepted {
		t.Fatalf("unexpected reprint job: %+v", reprint)
	}
}

func TestCancellationWinsBeforeTransmitAndEnrollmentSwitchRetiresRuntimeOwnership(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()
	s, err := NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := s.BindIdentity(ctx, "deployment-one", "instance-one"); err != nil {
		t.Fatalf("BindIdentity one: %v", err)
	}
	res, err := s.ResolvePrintCommand(ctx, PrintCommandInput{
		CommandID: "command-one", JobKey: "job-one", PayloadHash: "hash-one", PrinterID: "printer-one", TSPL: "CLS\n", Copies: 1,
	})
	if err != nil {
		t.Fatalf("ResolvePrintCommand: %v", err)
	}
	claimed, err := s.ClaimNextJob(ctx, "printer-one")
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNextJob: %v %+v", err, claimed)
	}
	cancelled, err := s.ResolveCancelCommand(ctx, "cancel-one", claimed.ID)
	if err != nil || cancelled.Resolution != "cancelled" {
		t.Fatalf("ResolveCancelCommand: %+v %v", cancelled, err)
	}
	if err := s.MarkTransmitting(ctx, claimed.ID); !errors.Is(err, ErrJobStateChanged) {
		t.Fatalf("MarkTransmitting after cancellation = %v, want ErrJobStateChanged", err)
	}
	job, err := s.GetJob(ctx, claimed.ID)
	if err != nil || job.State != StateCancelled {
		t.Fatalf("cancelled job changed state: %+v %v", job, err)
	}
	if err := s.ApplyConfig(ctx, 7, []byte(`{"configVersion":7,"printers":[],"profiles":[],"templates":[]}`)); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}

	changed, err := s.BindIdentity(ctx, "deployment-two", "instance-two")
	if err != nil || !changed {
		t.Fatalf("BindIdentity two changed=%v err=%v", changed, err)
	}
	if pending, err := s.ClaimNextJob(ctx, "printer-one"); err != nil || pending != nil {
		t.Fatalf("old deployment job claimed after switch: %+v %v", pending, err)
	}
	events, err := s.ListPendingOutboxEvents(ctx, 50)
	if err != nil || len(events) != 0 {
		t.Fatalf("old deployment events exposed after switch: %+v %v", events, err)
	}
	if version, _, err := s.LoadAppliedConfig(ctx); err != nil || version != 0 {
		t.Fatalf("old config remained active: version=%d err=%v", version, err)
	}
	if _, err := s.GetJob(ctx, res.AgentJobID); err != nil {
		t.Fatalf("historical job was not preserved: %v", err)
	}

	newRes, err := s.ResolvePrintCommand(ctx, PrintCommandInput{
		CommandID: "command-two", JobKey: "job-two", PayloadHash: "hash-two", PrinterID: "printer-two", TSPL: "CLS\n", Copies: 1,
	})
	if err != nil || newRes.Outcome != "accepted" {
		t.Fatalf("new deployment command failed: %+v %v", newRes, err)
	}
	events, err = s.ListPendingOutboxEvents(ctx, 50)
	if err != nil || len(events) != 2 || events[0].DeploymentID != "deployment-two" || events[1].DeploymentID != "deployment-two" {
		t.Fatalf("new deployment outbox ownership incorrect: %+v %v", events, err)
	}
	page, total, err := s.ListJobsPage(ctx, 1, 0)
	if err != nil || total != 1 || len(page) != 1 || page[0].ID != newRes.AgentJobID {
		t.Fatalf("active job page leaked or truncated history: total=%d page=%+v err=%v", total, page, err)
	}
	all, err := s.ListJobs(ctx, 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("administrator diagnostic history lost rows: %+v %v", all, err)
	}
}

func TestEnrollmentChangeDoesNotAdoptUnownedLegacyRuntimeRows(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s, err := NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.ResolvePrintCommand(ctx, PrintCommandInput{
		CommandID: "legacy-command", JobKey: "legacy-job", PayloadHash: "hash", PrinterID: "legacy-printer", TSPL: "CLS\n", Copies: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PreserveUnownedHistory(ctx, "old-deployment", "old-instance"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindIdentity(ctx, "new-deployment", "new-instance"); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimNextJob(ctx, "legacy-printer"); err != nil || claimed != nil {
		t.Fatalf("legacy job became active under new enrollment: %+v %v", claimed, err)
	}
	if events, err := s.ListPendingOutboxEvents(ctx, 50); err != nil || len(events) != 0 {
		t.Fatalf("legacy events became active under new enrollment: %+v %v", events, err)
	}
	if _, err := s.GetJob(ctx, legacy.AgentJobID); err != nil {
		t.Fatalf("legacy audit job was deleted: %v", err)
	}
}
