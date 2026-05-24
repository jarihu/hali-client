//go:build !windows

package notify

func Toast(title, body string) {} // no-op on non-Windows
