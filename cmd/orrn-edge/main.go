package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	mathrand "math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/orrn/spool/internal/cache"
	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/edgeclient"
	"github.com/orrn/spool/internal/install"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/localui"
	"github.com/orrn/spool/internal/node"
	"github.com/orrn/spool/internal/service"
	"github.com/orrn/spool/internal/worker"
)

// Version is set at build time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if err := runMain(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "orrn-edge: %v\n", err)
		os.Exit(1)
	}
}

func runMain(args []string) error {
	subcmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		subcmd = args[0]
		args = args[1:]
	}

	switch subcmd {
	case "version", "--version", "-v":
		fmt.Println(Version)
		return nil
	case "enroll":
		return runEnroll(args)
	case "healthcheck":
		return runHealthcheck(args)
	case "admin-password":
		return runAdminPassword(args)
	case "doctor":
		return runDoctor(args)
	case "run":
		return runDaemon(args)
	default:
		if handled, err := runLifecycleCommand(subcmd, args); handled {
			return err
		}
		return fmt.Errorf("unknown subcommand %q (expected: version, enroll, install, uninstall, upgrade, run, healthcheck, admin-password)", subcmd)
	}
}

func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// pollBackoff is 1s doubling to 60s, with up to 20% added jitter.
func pollBackoff(failures int) time.Duration {
	d := time.Second
	for i := 1; i < failures && d < time.Minute; i++ {
		d *= 2
	}
	if d > time.Minute {
		d = time.Minute
	}
	return d + time.Duration(mathrand.Int63n(int64(d)/5+1))
}

func resolvePaths(stateDirFlag string) install.Paths {
	paths := install.DefaultPaths()
	if strings.TrimSpace(stateDirFlag) != "" {
		paths.StateDir = stateDirFlag
		paths.DatabaseFile = filepath.Join(stateDirFlag, "edge.db")
		paths.NodeFile = filepath.Join(stateDirFlag, "node.json")
	}
	return paths
}

func runEnroll(args []string) error {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	stateDir := fs.String("state-dir", os.Getenv("ORRN_EDGE_STATE_DIR"), "State directory containing node.json and edge.db")
	apiOrigin := fs.String("api-origin", "https://api.orrn.in", "ORRN control plane API origin")
	tokenFile := fs.String("token-file", "", "Read one-time enrollment token from a file, or - for stdin")
	installID := fs.String("install-id", "", "UUIDv4 installation identifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*tokenFile) == "" {
		return errors.New("--token-file is required (use - for stdin)")
	}
	var raw []byte
	var err error
	if *tokenFile == "-" {
		raw, err = io.ReadAll(io.LimitReader(os.Stdin, 4096))
	} else {
		raw, err = os.ReadFile(*tokenFile)
	}
	if err != nil {
		return fmt.Errorf("read enrollment token: %w", err)
	}
	enrollmentToken := strings.TrimSpace(string(raw))
	if enrollmentToken == "" {
		return errors.New("enrollment token is empty")
	}
	id := strings.TrimSpace(*installID)
	if id == "" {
		id = newUUIDv4()
	}

	paths := resolvePaths(*stateDir)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	sqlDB, err := db.Open(paths.DatabaseFile)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		return err
	}
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		return err
	}
	uiServer, err := localui.NewServer(ctx, sqlDB, jobStore, cacheStore)
	if err != nil {
		return err
	}
	store := node.NewStore(paths.NodeFile)
	client := edgeclient.New(store, Version, nil).WithStores(jobStore, cacheStore).
		WithRevocationHandler(uiServer.RevokeLocalAccess)

	resp, err := client.Enroll(ctx, *apiOrigin, enrollmentToken, id)
	if err != nil {
		return err
	}
	fmt.Printf("enrolled node %s (instance=%s, deployment=%s)\n", resp.NodeName, resp.InstanceID, resp.DeploymentID)
	return nil
}

// runAdminPassword sets the loopback admin password from the first line of
// stdin so it never appears in process arguments or shell history.
func runAdminPassword(args []string) error {
	fs := flag.NewFlagSet("admin-password", flag.ContinueOnError)
	stateDir := fs.String("state-dir", os.Getenv("ORRN_EDGE_STATE_DIR"), "State directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	password := strings.TrimRight(line, "\r\n")

	paths := resolvePaths(*stateDir)
	ctx := context.Background()
	sqlDB, err := db.Open(paths.DatabaseFile)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		return err
	}
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		return err
	}
	ui, err := localui.NewServer(ctx, sqlDB, jobStore, cacheStore)
	if err != nil {
		return err
	}
	if err := ui.BootstrapAdmin(ctx, password); err != nil {
		return err
	}
	fmt.Println("admin password set")
	return nil
}

func runHealthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	urlStr := fs.String("url", "http://127.0.0.1:8080/healthz", "Healthcheck endpoint URL")
	timeout := fs.Duration("timeout", 5*time.Second, "Request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client := &http.Client{Timeout: *timeout}
	resp, err := client.Get(*urlStr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned status %d", resp.StatusCode)
	}
	return nil
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	stateDir := fs.String("state-dir", os.Getenv("ORRN_EDGE_STATE_DIR"), "State directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	paths := resolvePaths(*stateDir)
	report := map[string]any{
		"version":      Version,
		"stateDir":     paths.StateDir,
		"databaseFile": paths.DatabaseFile,
		"nodeFile":     paths.NodeFile,
		"enrolled":     false,
	}

	store := node.NewStore(paths.NodeFile)
	if st, err := store.Load(); err == nil {
		report["enrolled"] = true
		report["instanceId"] = st.InstanceID
		report["deploymentId"] = st.DeploymentID
		report["apiOrigin"] = st.APIOrigin
		report["credentialVersion"] = st.Active.Version
		report["revoked"] = st.Revoked
	}

	if sqlDB, err := db.Open(paths.DatabaseFile); err == nil {
		var mode string
		_ = sqlDB.QueryRow("PRAGMA journal_mode;").Scan(&mode)
		report["sqliteJournalMode"] = mode
		_ = sqlDB.Close()
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	stateDir := fs.String("state-dir", os.Getenv("ORRN_EDGE_STATE_DIR"), "State directory")
	adminAddr := fs.String("admin-addr", "127.0.0.1:8081", "Loopback admin bind address")
	fallbackAddr := fs.String("fallback-addr", ":8080", "LAN fallback bind address")
	pollInterval := fs.Duration("poll-interval", 5*time.Second, "Control plane poll interval")
	snapshotInterval := fs.Duration("snapshot-interval", time.Minute, "Read-cache snapshot sync interval")
	if err := fs.Parse(args); err != nil {
		return err
	}

	paths := resolvePaths(*stateDir)
	runner := func(ctx context.Context) error {
		sqlDB, err := db.Open(paths.DatabaseFile)
		if err != nil {
			return fmt.Errorf("open sqlite %s: %w", paths.DatabaseFile, err)
		}
		defer sqlDB.Close()

		jobStore, err := jobs.NewStore(ctx, sqlDB)
		if err != nil {
			return fmt.Errorf("init jobs store: %w", err)
		}
		nodeStore := node.NewStore(paths.NodeFile)
		nodeState, nodeErr := nodeStore.Load()
		cloudEnabled := nodeErr == nil && nodeState.Valid()
		if cloudEnabled {
			if _, err := jobStore.BindIdentity(ctx, nodeState.DeploymentID, nodeState.InstanceID); err != nil {
				return fmt.Errorf("bind runtime identity: %w", err)
			}
			if err := jobStore.RecoverAfterCrash(ctx); err != nil {
				return fmt.Errorf("recover jobs after crash: %w", err)
			}
		}

		cacheStore, err := cache.NewStore(ctx, sqlDB)
		if err != nil {
			return fmt.Errorf("init cache store: %w", err)
		}

		uiServer, err := localui.NewServer(ctx, sqlDB, jobStore, cacheStore)
		if err != nil {
			return fmt.Errorf("init local ui server: %w", err)
		}
		uiServer.WithNodeStore(nodeStore)

		adminSrv := &http.Server{
			Addr:              *adminAddr,
			Handler:           uiServer.AdminHandler(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		fallbackSrv := &http.Server{
			Addr:              *fallbackAddr,
			Handler:           uiServer.FallbackHandler(),
			ReadHeaderTimeout: 10 * time.Second,
		}

		errCh := make(chan error, 2)
		go func() {
			if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("admin listener (%s): %w", *adminAddr, err)
			}
		}()
		go func() {
			if err := fallbackSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("fallback listener (%s): %w", *fallbackAddr, err)
			}
		}()

		edgeClient := edgeclient.New(nodeStore, Version, nil).WithStores(jobStore, cacheStore).
			WithRevocationHandler(uiServer.RevokeLocalAccess)
		if cloudEnabled {
			if err := edgeClient.RestoreConfig(ctx); err != nil {
				return fmt.Errorf("restore applied config: %w", err)
			}
		} else if nodeErr == nil && nodeState.Revoked {
			if err := edgeClient.PollOnce(ctx); err != nil && !errors.Is(err, edgeclient.ErrNodeRevoked) {
				log.Printf("orrn-edge: revoked-state cleanup failed: %v", err)
			}
		}
		printWorker := worker.New(jobStore, edgeClient.Printers)
		edgeClient.WithPrinterReporter(printWorker.Reports)
		uiServer.SetPrinterReporter(printWorker.Reports)
		runtimeCtx, stopRuntime := context.WithCancel(ctx)
		defer stopRuntime()
		if !cloudEnabled {
			stopRuntime()
		}
		go printWorker.Run(runtimeCtx, time.Second)

		pollTimer := time.NewTimer(time.Hour)
		if cloudEnabled {
			pollTimer.Reset(0)
		}
		defer pollTimer.Stop()
		snapshotTicker := time.NewTicker(*snapshotInterval)
		defer snapshotTicker.Stop()
		snapshotRevoked := make(chan struct{}, 1)
		go func() {
			for {
				select {
				case <-runtimeCtx.Done():
					return
				case <-snapshotTicker.C:
					snapCtx, cancel := context.WithTimeout(runtimeCtx, 2*time.Minute)
					err := edgeClient.SyncSnapshotsOnce(snapCtx)
					cancel()
					if errors.Is(err, edgeclient.ErrNodeRevoked) {
						select {
						case snapshotRevoked <- struct{}{}:
						default:
						}
						return
					}
					if err != nil {
						log.Printf("orrn-edge: snapshot sync failed: %v", err)
					}
				}
			}
		}()
		failures := 0

		for {
			select {
			case <-ctx.Done():
				shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = adminSrv.Shutdown(shutCtx)
				_ = fallbackSrv.Shutdown(shutCtx)
				return nil
			case err := <-errCh:
				return err
			case <-pollTimer.C:
				pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := edgeClient.PollOnce(pollCtx)
				cancel()
				switch {
				case errors.Is(err, edgeclient.ErrNodeRevoked):
					log.Printf("orrn-edge: node revoked by ORRN; cloud, printing and fallback access stopped")
					cloudEnabled = false
					stopRuntime()
					pollTimer.Stop()
				case err != nil:
					failures++
					delay := pollBackoff(failures)
					log.Printf("orrn-edge: poll failed (retry in %s): %v", delay.Round(time.Second), err)
					pollTimer.Reset(delay)
				default:
					failures = 0
					pollTimer.Reset(*pollInterval)
				}
			case <-snapshotRevoked:
				log.Printf("orrn-edge: node revoked during snapshot sync; cloud, printing and fallback access stopped")
				cloudEnabled = false
				stopRuntime()
				pollTimer.Stop()
			}
		}
	}

	if isWinSvc, err := service.IsWindowsService(); err == nil && isWinSvc {
		return service.RunWindowsService(runner)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runner(ctx)
}
