//go:build linux

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const linuxServiceName = "halid"

func daemonAliasesService() bool {
	return true
}

// hasUserSession reports whether a D-Bus user session is available for systemctl --user.
func hasUserSession() bool {
	return strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")) != ""
}

func serviceInstallAction() error {
	// Enable for login/boot — works without an active session.
	if err := runSystemctl("--user", "enable", linuxServiceName); err != nil {
		return err
	}
	// Start immediately if a D-Bus session is available; ignore failure if not.
	if hasUserSession() {
		_ = runSystemctl("--user", "start", linuxServiceName)
	}
	return nil
}

func serviceUninstallAction() error {
	if hasUserSession() {
		_ = runSystemctl("--user", "disable", "--now", linuxServiceName)
	}
	return nil
}

func serviceStartAction() error {
	if !hasUserSession() {
		return userSessionError()
	}
	return runSystemctl("--user", "start", linuxServiceName)
}

func serviceStopAction() error {
	if !hasUserSession() {
		return userSessionError()
	}
	return runSystemctl("--user", "stop", linuxServiceName)
}

func serviceRestartAction() error {
	if !hasUserSession() {
		return userSessionError()
	}
	return runSystemctl("--user", "restart", linuxServiceName)
}

func serviceStatusAction() (string, error) {
	if !hasUserSession() {
		return "", userSessionError()
	}
	return runSystemctlOutput("--user", "status", "--no-pager", linuxServiceName)
}

func userSessionError() error {
	return fmt.Errorf("user systemd session not available\nTry:\n  loginctl enable-linger $USER\nor run inside a logged-in session")
}

func runSystemctl(args ...string) error {
	_, err := runSystemctlOutput(args...)
	return err
}

func runCommandOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("%s not found: %w", name, err)
		}
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func runSystemctlOutput(args ...string) (string, error) {
	return runCommandOutput("systemctl", args...)
}
