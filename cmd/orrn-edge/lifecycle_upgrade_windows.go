//go:build windows

package main

import (
	"context"

	"github.com/orrn/spool/internal/install"
	"github.com/orrn/spool/internal/service"
)

type windowsUpgradeServiceController struct{}

func newPlatformUpgradeServiceController() (install.ServiceController, error) {
	return windowsUpgradeServiceController{}, nil
}

func (windowsUpgradeServiceController) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return service.StopWindowsService()
}

func (windowsUpgradeServiceController) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return service.StartWindowsService()
}
