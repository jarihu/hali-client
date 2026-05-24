//go:build !windows

package daemon

import (
	"net"
	"os"
	"time"
)

// listenIPC creates a Unix domain socket at addr with 0660 permissions.
// The OS enforces that only the socket owner/group can connect — no token needed.
func listenIPC(addr string) (net.Listener, error) {
	os.Remove(addr) // remove any stale socket from a previous run
	ln, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}
	os.Chmod(addr, 0660) //nolint:errcheck
	return ln, nil
}

// dialIPC connects to the Unix domain socket at addr.
func dialIPC(addr string) (net.Conn, error) {
	return net.DialTimeout("unix", addr, 3*time.Second)
}
