//go:build windows

package main

import "os/exec"

func openShell(target string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start() //nolint:errcheck
}
