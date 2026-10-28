//go:build windows

package node

import (
	"fmt"
	"os/exec"
)

func protectNodeFile(path string) error {
	// LocalSystem and Administrators can read credentials; inherited broad ACLs are removed.
	if output, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", "*S-1-5-18:F", "*S-1-5-32-544:F").CombinedOutput(); err != nil {
		return fmt.Errorf("restrict node credential ACL: %w: %s", err, output)
	}
	return nil
}

func syncNodeDirectory(string) error { return nil }
