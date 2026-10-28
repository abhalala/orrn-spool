package db

import (
	"path/filepath"
	"testing"

	"github.com/orrn/spool/web"
)

func TestOpenConfiguresWALAndEmbeddedAssetsExist(t *testing.T) {
	dir := t.TempDir()
	conn, err := Open(filepath.Join(dir, "edge.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer conn.Close()

	var mode string
	if err := conn.QueryRow("PRAGMA journal_mode;").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}

	entries, err := web.Assets.ReadDir("templates")
	if err != nil || len(entries) == 0 {
		t.Fatalf("embedded templates missing: %v", err)
	}
}
