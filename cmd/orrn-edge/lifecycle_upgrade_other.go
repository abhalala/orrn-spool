//go:build !linux && !windows

package main

import (
	"errors"

	"github.com/orrn/spool/internal/install"
)

func newPlatformUpgradeServiceController() (install.ServiceController, error) {
	return nil, errors.New("upgrade service control is supported only on Linux and Windows; use the platform package manager on this operating system")
}
