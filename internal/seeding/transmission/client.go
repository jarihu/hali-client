package transmission

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"hali/internal/config"
)

// ErrDuplicateTorrent is returned when Transmission already manages the torrent.
// Callers must treat this as success.
var ErrDuplicateTorrent = errors.New("transmission: torrent already exists")

type rpcRequest struct {
	Method    string         `json:"method"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type rpcResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
}

type addArguments struct {
	TorrentAdded     *torrentRef `json:"torrent-added"`
	TorrentDuplicate *torrentRef `json:"torrent-duplicate"`
}

type torrentRef struct {
	HashString string `json:"hashString"`
	ID         int    `json:"id"`
	Name       string `json:"name"`
}

// Client is a Transmission RPC client. It handles the CSRF-style session ID
// handshake automatically. Safe for concurrent use.
type Client struct {
	baseURL   string
	username  string
	password  string
	mu        sync.Mutex
	sessionID string
	http      *http.Client
}

// NewClient creates a Client from a TransmissionConfig.
func NewClient(cfg config.TransmissionConfig) (*Client, error) {
	rawURL := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if rawURL == "" {
		return nil, errors.New("transmission: URL must not be empty")
	}
	if _, err := url.Parse(rawURL); err != nil {
		return nil, fmt.Errorf("transmission: invalid URL %q: %w", rawURL, err)
	}

	transport := http.DefaultTransport
	if cfg.SkipTLSVerify {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // user-opt-in for self-signed seedboxes
		}
	}

	return &Client{
		baseURL:  rawURL,
		username: cfg.Username,
		password: cfg.Password,
		http: &http.Client{
			Timeout:   15_000_000_000, // 15s
			Transport: transport,
		},
	}, nil
}

// AddTorrent registers a .torrent file with Transmission so it seeds from contentDir.
// Returns ErrDuplicateTorrent if the torrent is already managed.
func (c *Client) AddTorrent(ctx context.Context, torrentPath, contentDir string) error {
	slog.Debug("transmission: AddTorrent", "torrent_path", torrentPath, "content_dir", filepath.ToSlash(contentDir))

	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return fmt.Errorf("transmission: read torrent file %s: %w", torrentPath, err)
	}

	args := map[string]any{
		"metainfo":     base64.StdEncoding.EncodeToString(torrentBytes),
		"download-dir": filepath.ToSlash(contentDir),
		"paused":       false,
	}

	resp, err := c.do(ctx, rpcRequest{Method: "torrent-add", Arguments: args})
	if err != nil {
		return err
	}

	var add addArguments
	if err := json.Unmarshal(resp.Arguments, &add); err != nil {
		return fmt.Errorf("transmission: decode torrent-add response: %w", err)
	}
	if add.TorrentDuplicate != nil {
		return ErrDuplicateTorrent
	}
	if add.TorrentAdded != nil {
		return nil
	}
	return fmt.Errorf("transmission: torrent-add returned unexpected arguments")
}

// SessionGet performs a session-get RPC call, useful as a health check.
func (c *Client) SessionGet(ctx context.Context) error {
	_, err := c.do(ctx, rpcRequest{Method: "session-get"})
	return err
}

// do sends a single RPC request, handling the 409/session-ID handshake automatically.
// It retries once if Transmission responds with 409 (missing or stale session ID).
func (c *Client) do(ctx context.Context, req rpcRequest) (*rpcResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("transmission: marshal request: %w", err)
	}

	resp, err := c.sendOnce(ctx, body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusConflict {
		// 409 means our session ID is missing or expired. Extract the new one and retry.
		newID := resp.Header.Get("X-Transmission-Session-Id")
		if newID == "" {
			return nil, fmt.Errorf("transmission: 409 response missing X-Transmission-Session-Id header")
		}
		resp.Body.Close()

		c.mu.Lock()
		c.sessionID = newID
		c.mu.Unlock()

		slog.Debug("transmission: session refreshed", "session_id_len", len(newID))

		resp, err = c.sendOnce(ctx, body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusConflict {
			resp.Body.Close()
			return nil, fmt.Errorf("transmission: session conflict after retry")
		}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("transmission: read response: %w", err)
	}

	var rpc rpcResponse
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return nil, fmt.Errorf("transmission: decode response (status %d): %w", resp.StatusCode, err)
	}
	if rpc.Result != "success" {
		return nil, fmt.Errorf("transmission: RPC error: %s", rpc.Result)
	}
	return &rpc, nil
}

func (c *Client) sendOnce(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/transmission/rpc",
		strings.NewReader(string(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("transmission: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	c.mu.Lock()
	sid := c.sessionID
	c.mu.Unlock()
	if sid != "" {
		req.Header.Set("X-Transmission-Session-Id", sid)
	}

	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transmission: request failed: %w", err)
	}
	return resp, nil
}
