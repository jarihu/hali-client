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

func serviceInstallAction() error {
	if err := ensureLinuxServiceAccountAndDirs(); err != nil {
		return err
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}
	if err := runSystemctl("enable", linuxServiceName); err != nil {
		return err
	}
	return runSystemctl("start", linuxServiceName)
}

func serviceUninstallAction() error {
	if err := runSystemctl("disable", linuxServiceName); err != nil {
		return err
	}
	return runSystemctl("stop", linuxServiceName)
}

func serviceStartAction() error {
	return runSystemctl("start", linuxServiceName)
}

func serviceStopAction() error {
	return runSystemctl("stop", linuxServiceName)
}

func serviceRestartAction() error {
	return runSystemctl("restart", linuxServiceName)
}

func serviceStatusAction() (string, error) {
	return runSystemctlOutput("status", "--no-pager", linuxServiceName)
}

func runSystemctl(args ...string) error {
	_, err := runSystemctlOutput(args...)
	return err
}

func ensureLinuxServiceAccountAndDirs() error {
	if _, err := runCommandOutput("id", "-u", "hali"); err != nil {
		if err := runPrivileged("useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "hali"); err != nil {
			return fmt.Errorf("create hali system user: %w", err)
		}
	}

	for _, dir := range []string{"/var/lib/hali", "/var/log/hali", "/run/hali"} {
		if err := runPrivileged("install", "-d", "-m", "2775", "-o", "hali", "-g", "hali", dir); err != nil {
			return fmt.Errorf("prepare %s: %w", dir, err)
		}
	}
	return nil
}

func runPrivileged(name string, args ...string) error {
	if os.Geteuid() == 0 {
		_, err := runCommandOutput(name, args...)
		return err
	}
	sudoArgs := append([]string{name}, args...)
	_, err := runCommandOutput("sudo", sudoArgs...)
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
