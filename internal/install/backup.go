package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/orrn/spool/internal/db"
)

// BackupSQLite creates a consistent snapshot of the SQLite database (including WAL frames)
// at destPath using SQLite's VACUUM INTO command.
func BackupSQLite(ctx context.Context, srcDBPath, destPath string) error {
	if strings.TrimSpace(srcDBPath) == "" || strings.TrimSpace(destPath) == "" {
		return fmt.Errorf("source and destination database paths are required")
	}
	if _, err := os.Stat(srcDBPath); err != nil {
		return fmt.Errorf("stat source db %s: %w", srcDBPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return fmt.Errorf("mkdir backup dir: %w", err)
	}
	_ = os.Remove(destPath)

	conn, err := db.Open(srcDBPath)
	if err != nil {
		return fmt.Errorf("open source db for backup: %w", err)
	}
	defer conn.Close()

	escaped := strings.ReplaceAll(destPath, "'", "''")
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s';", escaped)); err != nil {
		return fmt.Errorf("vacuum into %s: %w", destPath, err)
	}
	return nil
}
