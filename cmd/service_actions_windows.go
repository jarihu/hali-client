//go:build windows

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"hali/internal/winsvc"
)

func daemonAliasesService() bool {
	return false
}

func serviceInstallAction() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	serviceExe, err := resolveWindowsServiceExe(exe)
	if err != nil {
		return err
	}

	return winsvc.Install(serviceExe)
}

func resolveWindowsServiceExe(currentExe string) (string, error) {
	base := strings.ToLower(filepath.Base(currentExe))
	if base == "halid.exe" {
		return currentExe, nil
	}

	serviceExe := filepath.Join(filepath.Dir(currentExe), "halid.exe")
	if _, err := os.Stat(serviceExe); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("service binary not found: %s (install halid.exe next to hali.exe)", serviceExe)
		}
		return "", fmt.Errorf("check service binary: %w", err)
	}

	return serviceExe, nil
}

func serviceUninstallAction() error {
	return winsvc.Uninstall()
}

func serviceStartAction() error {
	return winsvc.StartService()
}

func serviceStopAction() error {
	return winsvc.StopService()
}

func serviceRestartAction() error {
	if err := winsvc.StopService(); err != nil {
		return err
	}
	return winsvc.StartService()
}

func serviceStatusAction() (string, error) {
	state, err := winsvc.QueryStatus()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("HaliDaemon: %s", state), nil
}
