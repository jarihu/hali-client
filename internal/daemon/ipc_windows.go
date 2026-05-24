//go:build windows

package daemon

import (
	"net"
	"time"
)

// listenIPC creates a TCP listener on addr.
// Windows named pipes with proper DACLs are the correct long-term fix
// but require microsoft/go-winio. Until then, TCP + shared-secret token
// provides defense-in-depth against non-same-user processes; same-user
// processes are implicitly trusted on single-user Windows systems (same
// threat model as Unix socket + 0600).
func listenIPC(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// dialIPC connects to the TCP IPC address.
func dialIPC(addr string) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, 3*time.Second)
}
