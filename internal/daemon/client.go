package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"hali/internal/config"
)

// Client connects to a running daemon over IPC.
// On Linux/macOS: Unix domain socket. On Windows: TCP with shared-secret token.
type Client struct {
	addr string
}

func NewClient(addr string) *Client {
	return &Client{addr: addr}
}

// DefaultClient returns a client connected to the platform IPC address.
func DefaultClient() *Client {
	return NewClient(IPCAddr())
}

func (c *Client) Send(req Request) (Response, error) {
	// Windows: attach shared-secret token for IPC auth.
	if runtime.GOOS == "windows" {
		if data, err := os.ReadFile(config.IPCSecretPath()); err == nil {
			req.Token = strings.TrimSpace(string(data))
		}
	}

	conn, err := dialIPC(c.addr)
	if err != nil {
		return Response{}, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, fmt.Errorf("sending request: %w", err)
	}

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("reading response: %w", err)
	}
	return resp, nil
}

// IsRunning returns true if a daemon is reachable on the platform IPC address.
func IsRunning() bool {
	conn, err := dialIPC(IPCAddr())
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
