package qbittorrent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"hali/internal/config"
)

// ErrAlreadyRegistered is returned when qBittorrent already manages the torrent.
// Callers must treat this as success.
var ErrAlreadyRegistered = errors.New("torrent already registered in qBittorrent")

// AuthError is returned when qBittorrent rejects login credentials.
// This is a permanent failure — do not retry.
type AuthError struct {
	URL string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("qbittorrent: authentication failed for %s (check username/password)", e.URL)
}

// TorrentInfo is a minimal subset of the qBittorrent torrent info response.
type TorrentInfo struct {
	Hash     string  `json:"hash"`
	Name     string  `json:"name"`
	State    string  `json:"state"`
	Progress float64 `json:"progress"`
	SavePath string  `json:"save_path"`
}

// Client is a qBittorrent WebUI API v2 client. It holds a cookie jar so the
// SID cookie persists across calls within one instance. Safe for concurrent use.
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

// NewClient creates a Client from a QBittorrentConfig.
func NewClient(cfg config.QBittorrentConfig) (*Client, error) {
	rawURL := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if rawURL == "" {
		return nil, errors.New("qbittorrent: URL must not be empty")
	}
	if _, err := url.Parse(rawURL); err != nil {
		return nil, fmt.Errorf("qbittorrent: invalid URL %q: %w", rawURL, err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: create cookie jar: %w", err)
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
		httpClient: &http.Client{
			Timeout:   15_000_000_000, // 15s
			Jar:       jar,
			Transport: transport,
		},
	}, nil
}

// Login authenticates against the qBittorrent WebUI and stores the SID cookie
// in the client's cookie jar for subsequent requests.
func (c *Client) Login(ctx context.Context) error {
	slog.Debug("qbittorrent: POST /api/v2/auth/login", "url", c.baseURL)
	body := url.Values{
		"username": {c.username},
		"password": {c.password},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v2/auth/login",
		strings.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("qbittorrent: build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Debug("qbittorrent: login HTTP error", "err", err)
		return fmt.Errorf("qbittorrent: login request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("qbittorrent: read login response: %w", err)
	}

	result := strings.TrimSpace(string(raw))
	slog.Debug("qbittorrent: login response", "status", resp.StatusCode, "body", result)
	switch result {
	case "Ok.":
		return nil
	case "Fails.":
		return &AuthError{URL: c.baseURL}
	default:
		return fmt.Errorf("qbittorrent: unexpected login response (status %d): %s", resp.StatusCode, result)
	}
}

// TorrentInfo queries the status of a torrent by infohash.
// Returns an empty slice if no matching torrent is found.
func (c *Client) TorrentInfo(ctx context.Context, infohash string) ([]TorrentInfo, error) {
	slog.Debug("qbittorrent: GET /api/v2/torrents/info", "url", c.baseURL, "infohash", strings.ToLower(infohash))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v2/torrents/info?hashes="+strings.ToLower(infohash),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: build info request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Debug("qbittorrent: info HTTP error", "err", err)
		return nil, fmt.Errorf("qbittorrent: info request failed: %w", err)
	}
	defer resp.Body.Close()

	slog.Debug("qbittorrent: info response status", "status", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qbittorrent: info returned status %d", resp.StatusCode)
	}

	var infos []TorrentInfo
	if err := json.NewDecoder(resp.Body).Decode(&infos); err != nil {
		return nil, fmt.Errorf("qbittorrent: decode info response: %w", err)
	}
	slog.Debug("qbittorrent: info decoded", "count", len(infos))
	return infos, nil
}

// AddTorrent registers a .torrent file with qBittorrent.
// savePath must point to the directory containing the torrent content.
// skip_checking is set to false so qBittorrent performs a fast hash recheck
// before seeding — no downloading should occur.
// Returns ErrAlreadyRegistered if the torrent is already managed.
func (c *Client) AddTorrent(ctx context.Context, torrentPath, savePath, category string, tags []string) error {
	slog.Debug("qbittorrent: POST /api/v2/torrents/add",
		"torrent_path", torrentPath,
		"save_path", filepath.ToSlash(savePath),
		"category", category,
		"tags", tags,
	)
	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return fmt.Errorf("qbittorrent: read torrent file %s: %w", torrentPath, err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Torrent file field.
	fw, err := mw.CreateFormFile("torrents", filepath.Base(torrentPath))
	if err != nil {
		return fmt.Errorf("qbittorrent: create torrents field: %w", err)
	}
	if _, err := fw.Write(torrentBytes); err != nil {
		return fmt.Errorf("qbittorrent: write torrent bytes: %w", err)
	}

	// qBittorrent WebUI expects forward slashes even on Windows.
	if err := mw.WriteField("savepath", filepath.ToSlash(savePath)); err != nil {
		return fmt.Errorf("qbittorrent: write savepath field: %w", err)
	}
	if err := mw.WriteField("skip_checking", "false"); err != nil {
		return fmt.Errorf("qbittorrent: write skip_checking field: %w", err)
	}
	if category != "" {
		if err := mw.WriteField("category", category); err != nil {
			return fmt.Errorf("qbittorrent: write category field: %w", err)
		}
	}
	if len(tags) > 0 {
		if err := mw.WriteField("tags", strings.Join(tags, ",")); err != nil {
			return fmt.Errorf("qbittorrent: write tags field: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("qbittorrent: finalize multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v2/torrents/add",
		&buf,
	)
	if err != nil {
		return fmt.Errorf("qbittorrent: build add request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qbittorrent: add request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("qbittorrent: read add response: %w", err)
	}

	switch strings.TrimSpace(string(raw)) {
	case "Ok.":
		return nil
	case "Fails.":
		// qBittorrent returns "Fails." for duplicate torrents on the add endpoint.
		return ErrAlreadyRegistered
	default:
		return fmt.Errorf("qbittorrent: unexpected add response (status %d): %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}
}
