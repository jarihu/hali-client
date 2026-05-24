//go:build windows

package winsvc

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	SvcName        = "HaliDaemon"
	SvcDisplayName = "Hali Model Cache Service"
	SvcDescription = "Manages the Hali model cache and torrent seeding."
)

type handler struct {
	start func() error
	stop  func()
}

func (h *handler) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}

	errCh := make(chan error, 1)
	go func() { errCh <- h.start() }()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				h.stop()
				<-errCh
				return false, 0
			}
		case <-errCh:
			return false, 1
		}
	}
}

// RunAsService runs start() under the Windows Service Control Manager.
// stop() is called when the SCM sends a Stop or Shutdown signal.
func RunAsService(start func() error, stop func()) error {
	return svc.Run(SvcName, &handler{start: start, stop: stop})
}

// IsWindowsService reports whether the process is running as a Windows Service.
func IsWindowsService() (bool, error) {
	return svc.IsWindowsService()
}

// Install registers the service with the SCM with auto-start and recovery policy.
// exePath must be the absolute path to halid.exe.
func Install(exePath string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()

	cfg := mgr.Config{
		DisplayName:    SvcDisplayName,
		Description:    SvcDescription,
		StartType:      mgr.StartAutomatic,
		BinaryPathName: exePath,
	}

	s, err := m.CreateService(SvcName, exePath, cfg)
	if err != nil {
		s, err = m.OpenService(SvcName)
		if err != nil {
			return fmt.Errorf("create service: %w", err)
		}

		existing, err := s.Config()
		if err != nil {
			s.Close()
			return fmt.Errorf("query existing service config: %w", err)
		}

		existing.BinaryPathName = exePath
		existing.DisplayName = SvcDisplayName
		existing.Description = SvcDescription
		existing.StartType = mgr.StartAutomatic

		if err := s.UpdateConfig(existing); err != nil {
			s.Close()
			return fmt.Errorf("update service config: %w", err)
		}
	}
	defer s.Close()

	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 0); err != nil {
		return fmt.Errorf("set recovery actions: %w", err)
	}
	return nil
}

// Uninstall removes the service from the SCM.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(SvcName)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()
	return s.Delete()
}

// StartService starts the service via SCM.
func StartService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(SvcName)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()
	return s.Start()
}

// StopService stops the service via SCM.
func StopService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(SvcName)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()
	_, err = s.Control(svc.Stop)
	return err
}

// QueryStatus returns a human-readable SCM service state string.
func QueryStatus() (string, error) {
	m, err := mgr.Connect()
	if err != nil {
		return "", fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(SvcName)
	if err != nil {
		return "", fmt.Errorf("open service: %w", err)
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return "", err
	}
	switch st.State {
	case svc.Running:
		return "running", nil
	case svc.Stopped:
		return "stopped", nil
	case svc.StartPending:
		return "starting", nil
	case svc.StopPending:
		return "stopping", nil
	case svc.Paused:
		return "paused", nil
	default:
		return fmt.Sprintf("state=%d", st.State), nil
	}
}
