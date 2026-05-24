//go:build !windows

package main

import "os/exec"

func openShell(target string) {
	// xdg-open on Linux, open on macOS
	for _, cmd := range []string{"xdg-open", "open"} {
		if err := exec.Command(cmd, target).Start(); err == nil {
			return
		}
	}
}
