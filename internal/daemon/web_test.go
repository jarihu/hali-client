package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hali/internal/cache"
	"hali/internal/config"
	"hali/internal/policy"
	"hali/internal/torrent"
)

// newWebTestServer creates a Server on ephemeral ports, starts it, and returns
// (srv, webBaseURL) where webBaseURL is e.g. "http://127.0.0.1:54321".
func newWebTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	torrentDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())

	engine, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(engine.Close)

	store := &cache.Store{Root: dataDir}
	stats := torrent.NewStatsCollector(engine)
	srv := NewServer(engine, store, stats)

	if err := srv.startOnAddrs("127.0.0.1:0", "127.0.0.1:0"); err != nil {
		t.Fatalf("startOnAddrs: %v", err)
	}
	t.Cleanup(srv.Stop)
	go srv.serveIPC() //nolint:errcheck

	webURL := fmt.Sprintf("http://%s", srv.webLn.Addr().String())
	return srv, webURL
}

func TestWebDashboardServesHTML(t *testing.T) {
	_, baseURL := newWebTestServer(t)

	resp, err := http.Get(baseURL + "/") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) < 100 {
		t.Errorf("dashboard response too short (%d bytes)", len(body))
	}
	if !containsBytes(body, []byte("<!DOCTYPE html>")) {
		t.Error("response body does not contain <!DOCTYPE html>")
	}
}

func TestWebStatsEndpointReturnsJSON(t *testing.T) {
	_, baseURL := newWebTestServer(t)

	resp, err := http.Get(baseURL + "/api/stats") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/stats: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/stats status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var snap torrent.StatsSnapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Errorf("/api/stats is not a valid StatsSnapshot: %v\nbody: %s", err, body)
	}
	if snap.ActiveDLs < 0 {
		t.Error("active_downloads should be non-negative")
	}
}

func TestWebDashboardLocalhostOnly(t *testing.T) {
	srv, _ := newWebTestServer(t)

	host, _, err := net.SplitHostPort(srv.webLn.Addr().String())
	if err != nil {
		t.Fatalf("parse web addr: %v", err)
	}
	if host != "127.0.0.1" {
		t.Errorf("web server bound to %q — must be 127.0.0.1 only", host)
	}
}

func TestWebHealthEndpoint(t *testing.T) {
	_, baseURL := newWebTestServer(t)

	resp, err := http.Get(baseURL + "/api/health") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
	if _, ok := body["uptime"]; !ok {
		t.Error("health response missing uptime field")
	}
}

func TestWebPauseResume(t *testing.T) {
	srv, baseURL := newWebTestServer(t)

	if srv.paused.Load() {
		t.Error("paused should be false at start")
	}

	resp, err := http.Post(baseURL+"/api/pause", "application/json", nil) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /api/pause: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("pause status = %d, want 200", resp.StatusCode)
	}
	if !srv.paused.Load() {
		t.Error("paused should be true after /api/pause")
	}

	resp, err = http.Post(baseURL+"/api/resume", "application/json", nil) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /api/resume: %v", err)
	}
	resp.Body.Close()
	if srv.paused.Load() {
		t.Error("paused should be false after /api/resume")
	}
}

func TestWebPauseStateEndpoint(t *testing.T) {
	srv, baseURL := newWebTestServer(t)

	resp, err := http.Get(baseURL + "/api/pause-state") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/pause-state: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var state map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode /api/pause-state: %v", err)
	}
	paused, ok := state["paused"].(bool)
	if !ok {
		t.Fatalf("paused field missing or wrong type: %#v", state["paused"])
	}
	if paused != srv.paused.Load() {
		t.Errorf("paused = %v, want %v", paused, srv.paused.Load())
	}
	if _, ok := state["pause_until"]; !ok {
		t.Fatal("pause_until field missing")
	}
}

func TestWebPauseWithMinutes(t *testing.T) {
	_, baseURL := newWebTestServer(t)
	body := []byte(`{"minutes":45}`)

	resp, err := http.Post(baseURL+"/api/pause", "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /api/pause with minutes: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode /api/pause response: %v", err)
	}
	if out["applied_minutes"] != float64(45) {
		t.Fatalf("applied_minutes = %v, want 45", out["applied_minutes"])
	}
	if out["pause_until"] == float64(0) {
		t.Fatalf("pause_until should be non-zero for timed pause")
	}
}

func TestWebLanSharingEndpoint(t *testing.T) {
	srv, baseURL := newWebTestServer(t)

	resp, err := http.Get(baseURL + "/api/lan-sharing") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/lan-sharing: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var state map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode /api/lan-sharing: %v", err)
	}
	if !state["enabled"] {
		t.Fatal("default LAN sharing state should be enabled")
	}

	body := []byte(`{"enabled":false}`)
	resp2, err := http.Post(baseURL+"/api/lan-sharing", "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /api/lan-sharing: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}
	if srv.lanShare.Load() {
		t.Fatal("expected lanShare=false after POST disabled")
	}
}

func TestWebPauseRejectsGet(t *testing.T) {
	_, baseURL := newWebTestServer(t)

	resp, err := http.Get(baseURL + "/api/pause") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/pause: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestWebActivityEndpoint(t *testing.T) {
	srv, baseURL := newWebTestServer(t)

	srv.activity.Append(Event{Kind: "test.event", Message: "hello"})

	resp, err := http.Get(baseURL + "/api/activity") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/activity: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var events []Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatalf("decode activity: %v", err)
	}
	// at least our test event + the service.start event
	if len(events) < 2 {
		t.Errorf("expected >= 2 events, got %d", len(events))
	}
}

func TestWebSettings(t *testing.T) {
	_, baseURL := newWebTestServer(t)

	// GET returns defaults
	resp, err := http.Get(baseURL + "/api/settings") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/settings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET settings status = %d, want 200", resp.StatusCode)
	}
	var cfg policy.Settings
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode settings: %v", err)
	}

	// POST updates
	newCfg := policy.Settings{MaxUploadKBps: 512, MaxDownloadKBps: 1024}
	body, _ := json.Marshal(newCfg)
	resp2, err := http.Post(baseURL+"/api/settings", "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /api/settings: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("POST settings status = %d, want 200", resp2.StatusCode)
	}

	// GET again verifies the update
	resp3, err := http.Get(baseURL + "/api/settings") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/settings after update: %v", err)
	}
	defer resp3.Body.Close()
	var updated policy.Settings
	if err := json.NewDecoder(resp3.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated settings: %v", err)
	}
	if updated.MaxUploadKBps != 512 {
		t.Errorf("MaxUploadKBps = %d, want 512", updated.MaxUploadKBps)
	}
	if updated.MaxDownloadKBps != 1024 {
		t.Errorf("MaxDownloadKBps = %d, want 1024", updated.MaxDownloadKBps)
	}
}

func TestWebSettingsFormMBps(t *testing.T) {
	_, baseURL := newWebTestServer(t)

	form := url.Values{}
	form.Set("max_upload_mbps", "125")
	form.Set("max_download_mbps", "250.5")

	resp, err := http.Post(baseURL+"/api/settings", "application/x-www-form-urlencoded", strings.NewReader(form.Encode())) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /api/settings form: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, string(b))
	}

	resp2, err := http.Get(baseURL + "/api/settings") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/settings after form update: %v", err)
	}
	defer resp2.Body.Close()

	var updated policy.Settings
	if err := json.NewDecoder(resp2.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated settings: %v", err)
	}
	if updated.MaxUploadKBps != 128000 {
		t.Errorf("MaxUploadKBps = %d, want 128000", updated.MaxUploadKBps)
	}
	if updated.MaxDownloadKBps != 256512 {
		t.Errorf("MaxDownloadKBps = %d, want 256512", updated.MaxDownloadKBps)
	}
}

func TestWebSettingsNoOpSubmitDoesNotRevertConfigEdits(t *testing.T) {
	_, baseURL := newWebTestServer(t)

	initial := policy.Settings{MaxUploadKBps: 128000, MaxDownloadKBps: 257024}
	body, _ := json.Marshal(initial)
	resp, err := http.Post(baseURL+"/api/settings", "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /api/settings initial: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initial status = %d, want 200", resp.StatusCode)
	}

	// Simulate external edit via CLI/config file while daemon remains running.
	if err := config.SaveService(config.File{MaxUploadMBps: 0, MaxDownloadMBps: 0}); err != nil {
		t.Fatalf("SaveService external edit: %v", err)
	}

	// Submit unchanged stale values (what old UI state would post).
	body2, _ := json.Marshal(initial)
	resp2, err := http.Post(baseURL+"/api/settings", "application/json", bytes.NewReader(body2)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /api/settings no-op: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("no-op status = %d, want 200", resp2.StatusCode)
	}

	cfg, err := config.LoadService()
	if err != nil {
		t.Fatalf("LoadService: %v", err)
	}
	if cfg.MaxUploadMBps != 0 {
		t.Fatalf("MaxUploadMBps = %d, want 0", cfg.MaxUploadMBps)
	}
	if cfg.MaxDownloadMBps != 0 {
		t.Fatalf("MaxDownloadMBps = %d, want 0", cfg.MaxDownloadMBps)
	}
}

func TestWebFragmentsReturnHTML(t *testing.T) {
	_, baseURL := newWebTestServer(t)

	for _, path := range []string{"/fragments/status", "/fragments/downloads", "/fragments/activity", "/fragments/settings"} {
		resp, err := http.Get(baseURL + path) //nolint:noctx
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want text/html", path, ct)
		}
		if !containsBytes(body, []byte("<")) {
			t.Errorf("GET %s response contains no HTML", path)
		}
	}
}

func TestWebAPIEndpointsReturnJSON(t *testing.T) {
	_, baseURL := newWebTestServer(t)

	for _, path := range []string{"/api/health", "/api/stats", "/api/activity", "/api/settings"} {
		resp, err := http.Get(baseURL + path) //nolint:noctx
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		ct := resp.Header.Get("Content-Type")
		resp.Body.Close()
		if !strings.HasPrefix(ct, "application/json") {
			t.Errorf("GET %s Content-Type = %q, want application/json", path, ct)
		}
	}
}

// containsBytes reports whether haystack contains needle as a contiguous subsequence.
func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
outer:
	for i := 0; i <= len(haystack)-len(needle); i++ {
		for j := range needle {
			if haystack[i+j] != needle[j] {
				continue outer
			}
		}
		return true
	}
	return false
}
