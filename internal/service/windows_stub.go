//go:build !windows

package service

import "context"

const ServiceName = "ORRNEdge"

type Runner func(ctx context.Context) error

func IsWindowsService() (bool, error) {
	return false, nil
}

func RunWindowsService(run Runner) error {
	return run(context.Background())
}

func ConfigureRecoveryActions(_ string) error {
	return nil
}
