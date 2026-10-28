package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/orrn/spool/internal/db"
)

type fakeSvc struct{ started, stopped int }

func (f *fakeSvc) Stop(context.Context) error  { f.stopped++; return nil }
func (f *fakeSvc) Start(context.Context) error { f.started++; return nil }

type failingReady struct{ calls int }

func (f *failingReady) WaitReady(context.Context, time.Duration) error {
	f.calls++
	if f.calls == 1 {
		return errors.New("candidate failed readiness")
	}
	return nil
}

func TestApplyUpgradeBacksUpSQLiteAndRestoresPreviousBinaryOnFailedHealth(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "edge.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	_, _ = sqlDB.Exec("CREATE TABLE sample (id TEXT PRIMARY KEY); INSERT INTO sample VALUES ('row-1');")
	_ = sqlDB.Close()

	execPath := filepath.Join(dir, "orrn-edge")
	prevPath := filepath.Join(dir, "orrn-edge.previous")
	candPath := filepath.Join(dir, "candidate")
	backupPath := filepath.Join(dir, "edge-backup.db")

	_ = os.WriteFile(execPath, []byte("v1.0.0-binary"), 0o755)
	candBytes := []byte("v1.1.0-broken-binary")
	_ = os.WriteFile(candPath, candBytes, 0o755)
	sum := sha256.Sum256(candBytes)

	svc := &fakeSvc{}
	ready := &failingReady{}
	err = ApplyUpgrade(ctx, UpgradeDeps{
		Paths: Paths{
			Executable:         execPath,
			PreviousExecutable: prevPath,
			StateDir:           dir,
			DatabaseFile:       dbPath,
			NodeFile:           filepath.Join(dir, "node.json"),
		},
		Service: svc,
		Ready:   ready,
		Backup:  BackupSQLite,
	}, candPath, hex.EncodeToString(sum[:]), backupPath, time.Second)
	if err == nil {
		t.Fatalf("expected upgrade to fail when readiness fails")
	}
	if ready.calls != 2 || svc.started != 2 {
		t.Fatalf("candidate/restored readiness calls = %d, service starts = %d; want 2/2", ready.calls, svc.started)
	}

	restored, _ := os.ReadFile(execPath)
	if string(restored) != "v1.0.0-binary" {
		t.Fatalf("expected previous binary restored, got %q", string(restored))
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("expected consistent SQLite backup at %s: %v", backupPath, err)
	}
}

func TestApplyUpgradeRejectsChecksumBeforeStoppingService(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "candidate")
	if err := os.WriteFile(candidate, []byte("untrusted"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := &fakeSvc{}
	err := ApplyUpgrade(context.Background(), UpgradeDeps{Service: svc}, candidate, "00", filepath.Join(dir, "backup.db"), time.Second)
	if err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	if svc.stopped != 0 || svc.started != 0 {
		t.Fatalf("service changed before checksum verification: stop=%d start=%d", svc.stopped, svc.started)
	}
}
