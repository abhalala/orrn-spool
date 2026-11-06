package db

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var (
	sharedOnce sync.Once
	sharedErr  error
)

// initShared opens the package-wide database once; Init is a sync.Once too.
func initShared(t *testing.T) {
	t.Helper()
	sharedOnce.Do(func() {
		dir, err := os.MkdirTemp("", "spool-db-test")
		if err != nil {
			sharedErr = err
			return
		}
		sharedErr = Init(Config{Path: filepath.Join(dir, "spool.db")})
	})
	if sharedErr != nil {
		t.Fatal(sharedErr)
	}
}

// Request text in OrderBy/OrderDir must never become SQL. Each payload below
// is a real attack shape against the old fmt.Sprintf ORDER BY.
func TestListJobsRefusesSQLInSort(t *testing.T) {
	initShared(t)
	ctx := context.Background()
	if err := Settings.SetSetting(ctx, "sort_canary", "untouched", false); err != nil {
		t.Fatal(err)
	}
	payloads := []struct{ by, dir string }{
		{"created_at DESC; UPDATE settings SET value='owned' WHERE key='sort_canary'; --", ""},
		{"created_at", "DESC; UPDATE settings SET value='owned' WHERE key='sort_canary'; --"},
		{"(SELECT value FROM settings WHERE key='jwt_secret')", ""},
		{"CASE WHEN (SELECT substr(value,1,1) FROM settings WHERE key='admin_password')='$' THEN id ELSE created_at END", ""},
		{"id--", ""},
		{"id, created_at", ""},
		{"ID", ""},
		{"created_at ", ""},
		{"created_at", "ASC NULLS FIRST"},
		{"created_at", "asc --"},
		{"created_at", "DESC, (SELECT 1)"},
		{"priority DESC, created_at", ""},
	}
	for _, p := range payloads {
		if _, err := Jobs.ListJobs(ctx, JobFilter{OrderBy: p.by, OrderDir: p.dir}); err == nil {
			t.Errorf("OrderBy=%q OrderDir=%q: accepted, want an error", p.by, p.dir)
		}
	}
	s, err := Settings.GetSetting(ctx, "sort_canary")
	if err != nil {
		t.Fatal(err)
	}
	if s.Value != "untouched" {
		t.Fatalf("an injected statement ran: sort_canary = %q", s.Value)
	}
}

func TestListJobsAcceptsAllowedSorts(t *testing.T) {
	initShared(t)
	ctx := context.Background()
	for _, p := range []struct{ by, dir string }{
		{"", ""}, {"created_at", "DESC"}, {"id", "asc"}, {"id", "Desc"},
		{"priority", ""}, {"status", "ASC"}, {"printer_id", ""}, {"template_id", ""},
		{"started_at", ""}, {"completed_at", ""}, {JobOrderQueue, "ASC"},
	} {
		if _, err := Jobs.ListJobs(ctx, JobFilter{OrderBy: p.by, OrderDir: p.dir}); err != nil {
			t.Errorf("OrderBy=%q OrderDir=%q: %v", p.by, p.dir, err)
		}
	}
}

// First-boot setup relies on this: only one caller may create a key.
func TestCreateSettingIfAbsentNeverOverwrites(t *testing.T) {
	initShared(t)
	ctx := context.Background()
	key := "create_once_" + t.Name()
	// The database is shared across -count runs; start without the key.
	if err := Settings.DeleteSetting(ctx, key); err != nil {
		t.Fatal(err)
	}
	created, err := Settings.CreateSettingIfAbsent(ctx, key, "first", false)
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	created, err = Settings.CreateSettingIfAbsent(ctx, key, "second", false)
	if err != nil || created {
		t.Fatalf("second create: created=%v err=%v, want false", created, err)
	}
	s, err := Settings.GetSetting(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if s.Value != "first" {
		t.Fatalf("value = %q, want first", s.Value)
	}
}
