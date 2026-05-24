//go:build !windows

package winsvc

import "errors"

const (
	SvcName        = "HaliDaemon"
	SvcDisplayName = "Hali Model Cache Service"
	SvcDescription = "Manages the Hali model cache and torrent seeding."
)

var errNotWindows = errors.New("Windows Service management is only available on Windows")

func RunAsService(start func() error, stop func()) error { return errNotWindows }
func IsWindowsService() (bool, error)                    { return false, nil }
func Install(exePath string) error                       { return errNotWindows }
func Uninstall() error                                   { return errNotWindows }
func StartService() error                                { return errNotWindows }
func StopService() error                                 { return errNotWindows }
func QueryStatus() (string, error)                       { return "", errNotWindows }
