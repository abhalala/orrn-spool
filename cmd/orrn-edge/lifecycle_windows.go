//go:build windows

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/orrn/spool/internal/cache"
	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/edgeclient"
	"github.com/orrn/spool/internal/install"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/localui"
	"github.com/orrn/spool/internal/node"
	"github.com/orrn/spool/internal/service"
)

func runInstallCommand(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	apiOrigin := fs.String("api-origin", "https://api.orrn.in", "ORRN control plane API origin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("install does not accept positional arguments; the enrollment token and admin password are read from stdin")
	}

	paths := install.DefaultPaths()
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		return fmt.Errorf("create state directory %s: %w", paths.StateDir, err)
	}
	reader := bufio.NewReader(os.Stdin)
	if err := ensureInstallEnrollment(paths, *apiOrigin, reader, os.Stdout); err != nil {
		return err
	}
	if err := ensureInstallAdministrator(paths, reader, os.Stdout); err != nil {
		return err
	}

	source, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running executable: %w", err)
	}
	_, installedStatErr := os.Stat(paths.Executable)
	if installedStatErr == nil && !sameWindowsFile(source, paths.Executable) {
		digest, err := install.FileSHA256(source)
		if err != nil {
			return err
		}
		backup := filepath.Join(paths.StateDir, fmt.Sprintf("edge-pre-install-%d.db", time.Now().UTC().UnixNano()))
		if err := runUpgradeCommand([]string{
			"--candidate", source,
			"--sha256", digest,
			"--state-dir", paths.StateDir,
			"--backup", backup,
			"--ready-timeout", "60s",
		}); err != nil {
			return err
		}
		fmt.Printf("upgraded and started %s; state preserved in %s\n", service.ServiceName, paths.StateDir)
		return nil
	}
	if installedStatErr != nil && !errors.Is(installedStatErr, os.ErrNotExist) {
		return fmt.Errorf("inspect installed executable: %w", installedStatErr)
	}

	if err := service.StopWindowsService(); err != nil {
		return err
	}
	rollback, _, err := installRunningExecutable(paths)
	if err != nil {
		return errors.Join(err, service.StartWindowsService())
	}
	if err := service.InstallWindowsService(paths.Executable, paths.StateDir); err != nil {
		var rollbackErr error
		if rollback != nil {
			rollbackErr = rollback()
		}
		if rollbackErr == nil {
			rollbackErr = service.StartWindowsService()
		}
		return errors.Join(fmt.Errorf("install Windows service: %w", err), rollbackErr)
	}
	readiness, err := newUpgradeReadinessChecker("http://127.0.0.1:8080/healthz")
	if err != nil {
		return err
	}
	if err := readiness.WaitReady(context.Background(), 60*time.Second); err != nil {
		var rollbackErr error
		if rollback != nil {
			rollbackErr = rollback()
		}
		return errors.Join(fmt.Errorf("fresh Windows installation failed readiness: %w", err), rollbackErr, service.UninstallWindowsService())
	}

	fmt.Printf("installed and started %s; state preserved in %s\n", service.ServiceName, paths.StateDir)
	return nil
}

func runUninstallCommand(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("uninstall does not accept positional arguments")
	}
	if err := service.UninstallWindowsService(); err != nil {
		return err
	}
	fmt.Printf("removed %s; state preserved in %s\n", service.ServiceName, install.DefaultPaths().StateDir)
	return nil
}

func ensureInstallEnrollment(paths install.Paths, apiOrigin string, in io.Reader, out io.Writer) error {
	store := node.NewStore(paths.NodeFile)
	st, err := store.Load()
	if err == nil && !st.Revoked && st.Active.Version > 0 && strings.TrimSpace(st.Active.Secret) != "" {
		return nil
	}
	if err != nil && !errors.Is(err, node.ErrNodeNotEnrolled) {
		return fmt.Errorf("load existing enrollment: %w", err)
	}

	if _, err := fmt.Fprint(out, "Enrollment token: "); err != nil {
		return err
	}
	token, err := readLifecycleLine(in)
	if err != nil {
		return fmt.Errorf("read enrollment token from stdin: %w", err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("enrollment token is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	sqlDB, err := db.Open(paths.DatabaseFile)
	if err != nil {
		return fmt.Errorf("open state database: %w", err)
	}
	defer sqlDB.Close()
	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		return fmt.Errorf("initialize jobs store: %w", err)
	}
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		return fmt.Errorf("initialize cache store: %w", err)
	}
	client := edgeclient.New(store, Version, nil).WithStores(jobStore, cacheStore)
	resp, err := client.Enroll(ctx, apiOrigin, token, newUUIDv4())
	if err != nil {
		return fmt.Errorf("enroll edge node: %w", err)
	}
	_, err = fmt.Fprintf(out, "enrolled node %s (instance=%s, deployment=%s)\n", resp.NodeName, resp.InstanceID, resp.DeploymentID)
	return err
}

func ensureInstallAdministrator(paths install.Paths, in io.Reader, out io.Writer) error {
	ctx := context.Background()
	sqlDB, err := db.Open(paths.DatabaseFile)
	if err != nil {
		return fmt.Errorf("open state database: %w", err)
	}
	defer sqlDB.Close()

	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		return fmt.Errorf("initialize jobs store: %w", err)
	}
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		return fmt.Errorf("initialize cache store: %w", err)
	}
	ui, err := localui.NewServer(ctx, sqlDB, jobStore, cacheStore)
	if err != nil {
		return fmt.Errorf("initialize local administration store: %w", err)
	}

	var adminCount int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(1) FROM local_users WHERE username = 'admin'`).Scan(&adminCount); err != nil {
		return fmt.Errorf("check local administrator: %w", err)
	}
	if adminCount > 0 {
		return nil
	}

	if _, err := fmt.Fprint(out, "Local admin password: "); err != nil {
		return err
	}
	password, err := readLifecycleLine(in)
	if err != nil {
		return fmt.Errorf("read admin password from stdin: %w", err)
	}
	password = strings.TrimRight(password, "\r\n")
	if err := ui.BootstrapAdmin(ctx, password); err != nil {
		return fmt.Errorf("bootstrap local administrator: %w", err)
	}
	return nil
}

func readLifecycleLine(in io.Reader) (string, error) {
	reader, ok := in.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(in)
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}
	return line, nil
}

// installRunningExecutable atomically replaces the installed binary and keeps
// the prior binary for rollback. The returned closure is used only if service
// registration or startup fails.
func installRunningExecutable(paths install.Paths) (rollback func() error, restartPrevious bool, err error) {
	source, err := os.Executable()
	if err != nil {
		return nil, false, fmt.Errorf("locate running executable: %w", err)
	}
	if sameWindowsFile(source, paths.Executable) {
		return nil, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(paths.Executable), 0o755); err != nil {
		return nil, false, fmt.Errorf("create executable directory: %w", err)
	}

	hadPrevious := false
	if _, err := os.Stat(paths.Executable); err == nil {
		if err := os.Remove(paths.PreviousExecutable); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, false, fmt.Errorf("remove old rollback executable: %w", err)
		}
		if err := os.Rename(paths.Executable, paths.PreviousExecutable); err != nil {
			return nil, false, fmt.Errorf("retain current executable: %w", err)
		}
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("inspect installed executable: %w", err)
	}

	if err := copyExecutableAtomic(source, paths.Executable); err != nil {
		if hadPrevious {
			if restoreErr := os.Rename(paths.PreviousExecutable, paths.Executable); restoreErr != nil {
				return nil, false, errors.Join(fmt.Errorf("install executable: %w", err), fmt.Errorf("restore previous executable: %w", restoreErr))
			}
		}
		return nil, false, fmt.Errorf("install executable: %w", err)
	}

	rollback = func() error {
		if err := os.Remove(paths.Executable); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove failed executable: %w", err)
		}
		if hadPrevious {
			if err := os.Rename(paths.PreviousExecutable, paths.Executable); err != nil {
				return fmt.Errorf("restore previous executable: %w", err)
			}
		}
		return nil
	}
	return rollback, hadPrevious, nil
}

func sameWindowsFile(source, destination string) bool {
	sourceAbs, sourceErr := filepath.Abs(source)
	destinationAbs, destinationErr := filepath.Abs(destination)
	if sourceErr == nil && destinationErr == nil && strings.EqualFold(filepath.Clean(sourceAbs), filepath.Clean(destinationAbs)) {
		return true
	}
	sourceInfo, sourceErr := os.Stat(source)
	destinationInfo, destinationErr := os.Stat(destination)
	return sourceErr == nil && destinationErr == nil && os.SameFile(sourceInfo, destinationInfo)
}

func copyExecutableAtomic(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.CreateTemp(filepath.Dir(destination), ".orrn-edge-install-*")
	if err != nil {
		return err
	}
	temporaryPath := out.Name()
	keep := false
	defer func() {
		_ = out.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Chmod(0o755); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	keep = true
	return nil
}
