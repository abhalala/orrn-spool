package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/orrn/spool/internal/install"
)

func runUpgradeCommand(args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	candidate := fs.String("candidate", "", "Verified candidate executable to install")
	expectedSHA256 := fs.String("sha256", "", "Expected candidate executable SHA-256")
	stateDir := fs.String("state-dir", os.Getenv("ORRN_EDGE_STATE_DIR"), "State directory containing node.json and edge.db")
	backupPath := fs.String("backup", "", "Destination for the pre-upgrade SQLite backup")
	readyURL := fs.String("ready-url", "http://127.0.0.1:8080/healthz", "Readiness endpoint to poll after upgrade")
	readyTimeout := fs.Duration("ready-timeout", 60*time.Second, "Maximum time to wait for readiness")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("upgrade does not accept positional arguments")
	}
	if strings.TrimSpace(*candidate) == "" {
		return errors.New("--candidate is required")
	}
	if strings.TrimSpace(*expectedSHA256) == "" {
		return errors.New("--sha256 is required")
	}
	if strings.TrimSpace(*backupPath) == "" {
		return errors.New("--backup is required")
	}
	if *readyTimeout <= 0 {
		return errors.New("--ready-timeout must be greater than zero")
	}

	readiness, err := newUpgradeReadinessChecker(*readyURL)
	if err != nil {
		return err
	}
	controller, err := newPlatformUpgradeServiceController()
	if err != nil {
		return err
	}
	paths := resolvePaths(*stateDir)
	deps := install.UpgradeDeps{
		Paths:   paths,
		Service: controller,
		Ready:   readiness,
		Backup:  install.BackupSQLite,
	}
	if err := install.ApplyUpgrade(
		context.Background(),
		deps,
		*candidate,
		strings.TrimSpace(*expectedSHA256),
		*backupPath,
		*readyTimeout,
	); err != nil {
		return fmt.Errorf("upgrade ORRN Edge: %w", err)
	}
	fmt.Println("upgrade applied and ready")
	return nil
}

type upgradeReadinessChecker struct {
	client *http.Client
	url    string
}

func newUpgradeReadinessChecker(rawURL string) (*upgradeReadinessChecker, error) {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid --ready-url: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("--ready-url must be an absolute HTTP or HTTPS URL")
	}
	return &upgradeReadinessChecker{
		client: &http.Client{Timeout: 5 * time.Second},
		url:    rawURL,
	}, nil
}

func (c *upgradeReadinessChecker) WaitReady(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		return errors.New("readiness timeout must be greater than zero")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	lastErr := c.check(ctx)
	if lastErr == nil {
		return nil
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("readiness was not achieved within %s: %w (last check: %v)", timeout, ctx.Err(), lastErr)
		case <-ticker.C:
			if err := c.check(ctx); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
	}
}

func (c *upgradeReadinessChecker) check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned status %d", resp.StatusCode)
	}
	return nil
}
