//go:build windows

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const ServiceName = "ORRNEdge"

type Runner func(ctx context.Context) error

type winService struct {
	run Runner
}

func (w *winService) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- w.run(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for {
		select {
		case err := <-done:
			changes <- svc.Status{State: svc.StopPending}
			if err != nil {
				return false, 1
			}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}

func IsWindowsService() (bool, error) {
	return svc.IsWindowsService()
}

func RunWindowsService(run Runner) error {
	return svc.Run(ServiceName, &winService{run: run})
}

func ConfigureRecoveryActions(serviceName string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return err
	}
	defer s.Close()

	return configureRecoveryActions(s)
}

func configureRecoveryActions(s *mgr.Service) error {
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 15 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}
	return s.SetRecoveryActions(actions, 86400)
}

func windowsServiceBinaryPath(executablePath, stateDir string) string {
	return syscall.EscapeArg(executablePath) + " run --state-dir " + syscall.EscapeArg(stateDir)
}

// InstallWindowsService registers ORRN Edge for automatic startup, configures
// failure recovery, and starts it. An existing registration is updated in place.
func InstallWindowsService(executablePath, stateDir string) error {
	if strings.TrimSpace(executablePath) == "" || strings.TrimSpace(stateDir) == "" {
		return errors.New("executable path and state directory are required")
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to Windows service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	created := false
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		s, err = m.CreateService(ServiceName, executablePath, mgr.Config{
			StartType:    mgr.StartAutomatic,
			ErrorControl: mgr.ErrorNormal,
			DisplayName:  "ORRN Edge",
			Description:  "ORRN Edge site agent",
		}, "run", "--state-dir", stateDir)
		created = err == nil
	} else if err == nil {
		var cfg mgr.Config
		cfg, err = s.Config()
		if err == nil {
			cfg.BinaryPathName = windowsServiceBinaryPath(executablePath, stateDir)
			cfg.StartType = mgr.StartAutomatic
			cfg.ErrorControl = mgr.ErrorNormal
			cfg.DisplayName = "ORRN Edge"
			cfg.Description = "ORRN Edge site agent"
			err = s.UpdateConfig(cfg)
		}
	}
	if err != nil {
		if s != nil {
			_ = s.Close()
		}
		return fmt.Errorf("register Windows service %s: %w", ServiceName, err)
	}
	defer s.Close()

	if err := configureRecoveryActions(s); err != nil {
		if created {
			_ = s.Delete()
		}
		return fmt.Errorf("configure Windows service recovery: %w", err)
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		if created {
			_ = s.Delete()
		}
		return fmt.Errorf("configure Windows service non-crash recovery: %w", err)
	}
	if err := startWindowsService(s); err != nil {
		if created {
			_ = s.Delete()
		}
		return fmt.Errorf("start Windows service %s: %w", ServiceName, err)
	}
	return nil
}

func startWindowsService(s *mgr.Service) error {
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	return nil
}

// StartWindowsService starts an existing registration. It is a no-op when the
// service is not installed, which lets failed first installs restart cleanly.
func StartWindowsService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to Windows service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Windows service %s: %w", ServiceName, err)
	}
	defer s.Close()
	if err := startWindowsService(s); err != nil {
		return fmt.Errorf("start Windows service %s: %w", ServiceName, err)
	}
	return nil
}

// StopWindowsService stops an existing registration and waits for the process
// to exit so its executable can be replaced. A missing service is a no-op.
func StopWindowsService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to Windows service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Windows service %s: %w", ServiceName, err)
	}
	defer s.Close()
	if err := stopWindowsService(s); err != nil {
		return fmt.Errorf("stop Windows service %s: %w", ServiceName, err)
	}
	return nil
}

func stopWindowsService(s *mgr.Service) error {
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return err
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		status, err = s.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Stopped {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out after 30 seconds")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// UninstallWindowsService removes only the service registration. Program data,
// enrollment, and the SQLite state are deliberately left untouched.
func UninstallWindowsService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to Windows service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Windows service %s: %w", ServiceName, err)
	}
	defer s.Close()
	if err := stopWindowsService(s); err != nil {
		return fmt.Errorf("stop Windows service %s: %w", ServiceName, err)
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete Windows service %s: %w", ServiceName, err)
	}
	return nil
}
