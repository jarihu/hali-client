package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"hali/internal/config"
)

// Launch spawns a detached background daemon process using the current binary.
// It waits for the daemon to write its .ready sentinel file.
func Launch() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}

	// Remove any stale .ready sentinel so we don't mistake it for the new daemon.
	readyFile := readyFilePath()
	_ = os.Remove(readyFile)

	cmd := exec.Command(exe, "daemon", "_run")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawning daemon: %w", err)
	}
	timeout := config.DefaultLaunchTimeout
	if cfg, err := config.Load(); err == nil {
		timeout = cfg.LaunchTimeout()
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyFile); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	// If .ready wasn't written, fall back to checking the port directly.
	if IsRunning() {
		return nil
	}
	return fmt.Errorf("daemon did not start within %v", timeout)
}
