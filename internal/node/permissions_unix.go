//go:build !windows

package node

import (
	"os"
	"path/filepath"
)

func protectNodeFile(path string) error {
	return os.Chmod(path, 0o600)
}

func syncNodeDirectory(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
