//go:build !windows

package main

import "fmt"

func runInstallCommand(_ []string) error {
	return fmt.Errorf("native install is supported only on Windows; on Linux or Raspberry Pi use packaging/linux/install.sh, or use the Docker package")
}

func runUninstallCommand(_ []string) error {
	return fmt.Errorf("native uninstall is supported only on Windows; on Linux or Raspberry Pi use packaging/linux/uninstall.sh, or remove the Docker deployment while retaining its state volume")
}
