//go:build !windows && !linux && !darwin

package cmd

import "fmt"

func daemonAliasesService() bool {
	return false
}

func serviceInstallAction() error {
	return fmt.Errorf("service management is only supported on Windows and Linux")
}

func serviceUninstallAction() error {
	return fmt.Errorf("service management is only supported on Windows and Linux")
}

func serviceStartAction() error {
	return fmt.Errorf("service management is only supported on Windows and Linux")
}

func serviceStopAction() error {
	return fmt.Errorf("service management is only supported on Windows and Linux")
}

func serviceRestartAction() error {
	return fmt.Errorf("service management is only supported on Windows and Linux")
}

func serviceStatusAction() (string, error) {
	return "", fmt.Errorf("service management is only supported on Windows and Linux")
}
