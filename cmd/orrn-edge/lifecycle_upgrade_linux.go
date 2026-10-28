//go:build linux

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/orrn/spool/internal/install"
)

type systemctlUpgradeServiceController struct{}

func newPlatformUpgradeServiceController() (install.ServiceController, error) {
	return systemctlUpgradeServiceController{}, nil
}

func (systemctlUpgradeServiceController) Stop(ctx context.Context) error {
	return runSystemctlUpgrade(ctx, "stop")
}

func (systemctlUpgradeServiceController) Start(ctx context.Context) error {
	return runSystemctlUpgrade(ctx, "start")
}

func runSystemctlUpgrade(ctx context.Context, action string) error {
	cmd := exec.CommandContext(ctx, "systemctl", action, "orrn-edge.service")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return fmt.Errorf("systemctl %s orrn-edge.service: %w: %s", action, err, detail)
	}
	return fmt.Errorf("systemctl %s orrn-edge.service: %w", action, err)
}
