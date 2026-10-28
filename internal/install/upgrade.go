package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type ServiceController interface {
	Stop(ctx context.Context) error
	Start(ctx context.Context) error
}

type ReadinessChecker interface {
	WaitReady(ctx context.Context, timeout time.Duration) error
}

type BackupFunc func(ctx context.Context, srcDBPath, destPath string) error

type UpgradeDeps struct {
	Paths   Paths
	Service ServiceController
	Ready   ReadinessChecker
	Backup  BackupFunc
}

// ApplyUpgrade verifies the candidate, backs up state, installs it atomically,
// and restores the previous executable and verified service readiness on failure.
func ApplyUpgrade(
	ctx context.Context,
	deps UpgradeDeps,
	candidatePath string,
	expectedSHA256 string,
	backupDBPath string,
	readyTimeout time.Duration,
) error {
	if err := VerifyFileSHA256(candidatePath, expectedSHA256); err != nil {
		return err
	}
	if readyTimeout <= 0 {
		readyTimeout = 30 * time.Second
	}

	backupFn := deps.Backup
	if backupFn == nil {
		backupFn = BackupSQLite
	}
	if deps.Paths.DatabaseFile != "" && backupDBPath != "" {
		_, err := os.Stat(deps.Paths.DatabaseFile)
		switch {
		case err == nil:
			if err := backupFn(ctx, deps.Paths.DatabaseFile, backupDBPath); err != nil {
				return fmt.Errorf("backup sqlite before upgrade: %w", err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("stat sqlite before upgrade: %w", err)
		}
	}

	if _, err := os.Stat(deps.Paths.Executable); err != nil {
		return fmt.Errorf("current executable is required for upgrade: %w", err)
	}
	if err := os.Remove(deps.Paths.PreviousExecutable); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale rollback executable: %w", err)
	}

	if deps.Service != nil {
		if err := deps.Service.Stop(ctx); err != nil {
			return fmt.Errorf("stop service before upgrade: %w", err)
		}
	}
	if err := os.Rename(deps.Paths.Executable, deps.Paths.PreviousExecutable); err != nil {
		restartErr := startService(ctx, deps.Service)
		return errors.Join(fmt.Errorf("move current executable to %s: %w", deps.Paths.PreviousExecutable, err), restartErr)
	}

	if err := copyExecutable(candidatePath, deps.Paths.Executable); err != nil {
		rollbackErr := rollbackBinary(ctx, deps, readyTimeout)
		return errors.Join(fmt.Errorf("install candidate executable: %w", err), rollbackErr)
	}
	if err := startService(ctx, deps.Service); err != nil {
		rollbackErr := rollbackBinary(ctx, deps, readyTimeout)
		return errors.Join(fmt.Errorf("start upgraded service: %w", err), rollbackErr)
	}
	if deps.Ready != nil {
		if err := deps.Ready.WaitReady(ctx, readyTimeout); err != nil {
			rollbackErr := rollbackBinary(ctx, deps, readyTimeout)
			return errors.Join(fmt.Errorf("upgraded binary failed readiness: %w", err), rollbackErr)
		}
	}
	return nil
}

func startService(ctx context.Context, service ServiceController) error {
	if service == nil {
		return nil
	}
	return service.Start(ctx)
}

func rollbackBinary(ctx context.Context, deps UpgradeDeps, readyTimeout time.Duration) error {
	var errs []error
	if deps.Service != nil {
		if err := deps.Service.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop candidate during rollback: %w", err))
		}
	}
	if err := os.Remove(deps.Paths.Executable); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("remove candidate during rollback: %w", err))
	}
	if err := os.Rename(deps.Paths.PreviousExecutable, deps.Paths.Executable); err != nil {
		errs = append(errs, fmt.Errorf("restore previous executable: %w", err))
	}
	if err := startService(ctx, deps.Service); err != nil {
		errs = append(errs, fmt.Errorf("restart restored service: %w", err))
	}
	if deps.Ready != nil {
		if err := deps.Ready.WaitReady(ctx, readyTimeout); err != nil {
			errs = append(errs, fmt.Errorf("restored service failed readiness: %w", err))
		}
	}
	return errors.Join(errs...)
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".orrn-edge-candidate-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, dst)
}
