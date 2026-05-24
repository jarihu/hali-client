//go:build windows

package notify

import "github.com/go-toast/toast"

// Toast displays a Windows toast notification using WinRT via go-toast.
// Inputs are never interpolated into a shell script, eliminating the
// PowerShell injection class entirely.
func Toast(title, body string) {
	n := toast.Notification{
		AppID:   "Hali",
		Title:   title,
		Message: body,
	}
	n.Push() //nolint:errcheck — best-effort notification
}
