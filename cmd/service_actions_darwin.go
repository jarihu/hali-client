//go:build darwin

package cmd

import (
	"fmt"
	"hali/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const darwinServiceLabel = "com.hali.daemon"

func daemonAliasesService() bool {
	return true
}

func serviceInstallAction() error {
	plistPath, err := writeLaunchAgentPlist()
	if err != nil {
		return err
	}
	_, _ = runLaunchctl("unload", plistPath)
	_, err = runLaunchctl("load", plistPath)
	return err
}

func serviceUninstallAction() error {
	plistPath, err := launchAgentPlistPath()
	if err != nil {
		return err
	}
	_, _ = runLaunchctl("unload", plistPath)
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func serviceStartAction() error {
	_, err := runLaunchctl("start", darwinServiceLabel)
	return err
}

func serviceStopAction() error {
	_, err := runLaunchctl("stop", darwinServiceLabel)
	return err
}

func serviceRestartAction() error {
	if err := serviceStopAction(); err != nil {
		return err
	}
	return serviceStartAction()
}

func serviceStatusAction() (string, error) {
	uid := strconv.Itoa(os.Getuid())
	out, err := runLaunchctl("print", "gui/"+uid+"/"+darwinServiceLabel)
	if err != nil {
		return "", err
	}
	state := "unknown"
	if strings.Contains(out, "state = running") {
		state = "running"
	} else if strings.Contains(out, "state = waiting") {
		state = "waiting"
	}
	return fmt.Sprintf("%s: %s", darwinServiceLabel, state), nil
}

func runLaunchctl(args ...string) (string, error) {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("launchctl %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(string(out)), nil
}

func launchAgentPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	return filepath.Join(dir, darwinServiceLabel+".plist"), nil
}

func writeLaunchAgentPlist() (string, error) {
	plistPath, err := launchAgentPlistPath()
	if err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
		return "", err
	}
	logPath := filepath.Join(config.ServiceLogDir(), "hali.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return "", err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple Computer//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>daemon</string>
    <string>_run</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, darwinServiceLabel, exe, logPath, logPath)
	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		return "", err
	}
	return plistPath, nil
}
