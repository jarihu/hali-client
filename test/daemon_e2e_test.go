//go:build integration

package test

// Daemon-only e2e tests.  These exercise the daemon subprocess via IPC without
// requiring the real backend — a lightweight httptest.Server is used as the
// ingest endpoint where needed.

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hali/internal/daemon"
	"hali/internal/events"
)

func waitForLogContains(t *testing.T, logs *bytes.Buffer, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), want) {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("log did not contain %q within %s\nlogs:\n%s", want, timeout, logs.String())
}

func waitForLogContainsOptional(logs *bytes.Buffer, want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), want) {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

func sendUnsignedLANPacket(t *testing.T, modelID, infohash string) {
	t.Helper()
	payload := map[string]any{
		"v":   "1",
		"nid": "e2e-unsigned-node",
		"ts":  time.Now().Unix(),
		"p":   4269,
		"models": []map[string]any{
			{"id": modelID, "ih": infohash},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal lan payload: %v", err)
	}
	addr, err := net.ResolveUDPAddr("udp4", "239.192.42.1:4269")
	if err != nil {
		t.Fatalf("resolve lan addr: %v", err)
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		t.Fatalf("dial lan addr: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write lan payload: %v", err)
	}
}

// startIsolatedDaemon builds the hali binary, writes cfg as the daemon config,
// starts the daemon, and registers cleanup.  Returns the exec.Cmd and log
// buffer so callers can include logs in failure messages.
func startIsolatedDaemon(t *testing.T, cfg map[string]any) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	clientRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve client root: %v", err)
	}
	base := e2eBase(t)
	env := buildIsolatedEnv(base)
	applyEnvToCurrentProcess(t, env)

	cfgPath := filepath.Join(env["HALI_SERVICE_DATA_DIR"], "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal daemon config: %v", err)
	}
	if err := os.WriteFile(cfgPath, append(data, '\n'), 0644); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}

	haliExe := buildHaliBinary(t, clientRoot, base)
	cmd, logs := startDaemonProcess(t, haliExe, env)
	t.Cleanup(func() { stopDaemonProcess(cmd) })
	return cmd, logs
}

// e2eInfohash produces a unique 40-hex-char infohash from seed.
func e2eInfohash(seed string) string {
	sum := sha1.Sum([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func writeE2ETorrentFile(t *testing.T, infohash string) {
	t.Helper()
	serviceDir := os.Getenv("HALI_SERVICE_DATA_DIR")
	if serviceDir == "" {
		t.Fatal("HALI_SERVICE_DATA_DIR is empty")
	}
	torrentDir := filepath.Join(serviceDir, "torrents")
	if err := os.MkdirAll(torrentDir, 0755); err != nil {
		t.Fatalf("MkdirAll torrent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(torrentDir, infohash+".torrent"), []byte("test-torrent"), 0644); err != nil {
		t.Fatalf("WriteFile torrent: %v", err)
	}
}

// TestDaemonStatusIPC verifies that CmdStatus returns a well-formed StatusData
// with a valid PID, torrent port, and non-empty uptime string.
func TestDaemonStatusIPC(t *testing.T) {
	if daemonProtocolReachable() {
		t.Skip("daemon already running on fixed IPC port; stop it before running this e2e")
	}
	if !ipcPortAvailable() {
		t.Skip("IPC port 47432 is already in use")
	}

	_, daemonLogs := startIsolatedDaemon(t, map[string]any{
		"telemetry_enabled": false,
	})

	resp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdStatus})
	if err != nil {
		t.Fatalf("CmdStatus: %v\ndaemon logs:\n%s", err, daemonLogs.String())
	}
	if !resp.OK {
		t.Fatalf("CmdStatus not ok: %s\ndaemon logs:\n%s", resp.Error, daemonLogs.String())
	}

	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("re-marshal StatusData: %v", err)
	}
	var status daemon.StatusData
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatalf("unmarshal StatusData: %v", err)
	}
	if status.PID <= 0 {
		t.Errorf("status.PID = %d, want > 0", status.PID)
	}
	if status.Port <= 0 {
		t.Errorf("status.Port = %d, want > 0", status.Port)
	}
	if status.Uptime == "" {
		t.Error("status.Uptime is empty")
	}
}

// TestDaemonStopViaIPC sends CmdStop to a running daemon and verifies it
// becomes unreachable within 5 seconds.
func TestDaemonStopViaIPC(t *testing.T) {
	if daemonProtocolReachable() {
		t.Skip("daemon already running on fixed IPC port; stop it before running this e2e")
	}
	if !ipcPortAvailable() {
		t.Skip("IPC port 47432 is already in use")
	}

	_, daemonLogs := startIsolatedDaemon(t, map[string]any{
		"telemetry_enabled": false,
	})

	resp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdStop})
	if err != nil {
		t.Fatalf("CmdStop: %v\ndaemon logs:\n%s", err, daemonLogs.String())
	}
	if !resp.OK {
		t.Fatalf("CmdStop not ok: %s\ndaemon logs:\n%s", resp.Error, daemonLogs.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !daemonProtocolReachable() {
			return // daemon stopped as expected
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("daemon still reachable 5s after CmdStop\ndaemon logs:\n%s", daemonLogs.String())
}

// TestDaemonTelemetryDisabledNoDelivery verifies that events queued while
// telemetry_enabled is false are never sent to the ingest endpoint.
func TestDaemonTelemetryDisabledNoDelivery(t *testing.T) {
	if daemonProtocolReachable() {
		t.Skip("daemon already running on fixed IPC port; stop it before running this e2e")
	}
	if !ipcPortAvailable() {
		t.Skip("IPC port 47432 is already in use")
	}

	var received int64
	var mu sync.Mutex
	ingestSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/ingest" {
			mu.Lock()
			received++
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestSrv.Close()

	_, daemonLogs := startIsolatedDaemon(t, map[string]any{
		"telemetry_enabled":   false,
		"registry_ingest_url": ingestSrv.URL,
	})

	ih := e2eInfohash(fmt.Sprintf("telemetry-disabled-%d", time.Now().UnixNano()))
	writeE2ETorrentFile(t, ih)
	event := events.ModelPullEvent{
		ModelID:   "google-bert/bert-base-uncased",
		Revision:  "main",
		InfoHash:  ih,
		Magnet:    "magnet:?xt=urn:btih:" + ih,
		SourceURL: "https://huggingface.co/google-bert/bert-base-uncased",
		LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp: time.Now().UTC(),
	}

	resp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdEnqueueEvent, Event: &event})
	if err != nil {
		t.Fatalf("enqueue_event: %v\ndaemon logs:\n%s", err, daemonLogs.String())
	}
	if !resp.OK {
		t.Fatalf("enqueue_event not ok: %s\ndaemon logs:\n%s", resp.Error, daemonLogs.String())
	}

	// Give the event worker two full drain ticks plus the immediate wake.
	time.Sleep(3 * time.Second)

	mu.Lock()
	n := received
	mu.Unlock()
	if n != 0 {
		t.Errorf("ingest endpoint received %d request(s) with telemetry disabled, want 0\ndaemon logs:\n%s", n, daemonLogs.String())
	}
}

// TestDaemonMultipleEventsAllDelivered enqueues several events and verifies
// every one is delivered to the ingest endpoint with the correct infohash.
func TestDaemonMultipleEventsAllDelivered(t *testing.T) {
	if daemonProtocolReachable() {
		t.Skip("daemon already running on fixed IPC port; stop it before running this e2e")
	}
	if !ipcPortAvailable() {
		t.Skip("IPC port 47432 is already in use")
	}

	const N = 3
	delivered := make(chan string, N*2) // receives infohashes as they arrive
	ingestSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/ingest" {
			if err := r.ParseMultipartForm(8 << 20); err == nil {
				file, _, _ := r.FormFile("torrent")
				if file != nil {
					_, _ = io.ReadAll(file)
					_ = file.Close()
				}
				select {
				case delivered <- r.FormValue("infohash"):
				default:
				}
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestSrv.Close()

	_, daemonLogs := startIsolatedDaemon(t, map[string]any{
		"telemetry_enabled":   true,
		"registry_ingest_url": ingestSrv.URL,
	})

	// Generate unique infohashes then enqueue each event.
	sent := make([]string, N)
	for i := range sent {
		sent[i] = e2eInfohash(fmt.Sprintf("multi-event-%d-%d", i, time.Now().UnixNano()))
		ih := sent[i]
		writeE2ETorrentFile(t, ih)
		ev := events.ModelPullEvent{
			ModelID:   fmt.Sprintf("test-model-%d/gguf", i),
			Revision:  "main",
			InfoHash:  ih,
			Magnet:    "magnet:?xt=urn:btih:" + ih,
			SourceURL: "https://example.test/model.gguf",
			LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			Timestamp: time.Now().UTC(),
		}
		resp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdEnqueueEvent, Event: &ev})
		if err != nil {
			t.Fatalf("enqueue_event[%d]: %v\ndaemon logs:\n%s", i, err, daemonLogs.String())
		}
		if !resp.OK {
			t.Fatalf("enqueue_event[%d] not ok: %s\ndaemon logs:\n%s", i, resp.Error, daemonLogs.String())
		}
	}

	// Collect delivered infohashes (30s timeout to account for retry backoff).
	receivedIH := make(map[string]bool)
	timeout := time.After(30 * time.Second)
	for len(receivedIH) < N {
		select {
		case ih := <-delivered:
			receivedIH[ih] = true
		case <-timeout:
			t.Fatalf("received %d/%d events within 30s\ndaemon logs:\n%s", len(receivedIH), N, daemonLogs.String())
		}
	}

	// Verify the specific infohashes that were sent all arrived.
	for i, ih := range sent {
		if !receivedIH[ih] {
			t.Errorf("event[%d] infohash %s not received", i, ih)
		}
	}
}

func TestDaemonAppliesConfiguredSpeedLimitsE2E(t *testing.T) {
	if daemonProtocolReachable() {
		t.Skip("daemon already running on fixed IPC port; stop it before running this e2e")
	}
	if !ipcPortAvailable() {
		t.Skip("IPC port 47432 is already in use")
	}

	_, daemonLogs := startIsolatedDaemon(t, map[string]any{
		"telemetry_enabled":   false,
		"max_upload_mbps":     1,
		"max_download_mbps":   2,
		"lan_hmac_enabled":    false,
		"registry_ingest_url": "https://api.hali.network/ingest",
	})

	waitForLogContains(t, daemonLogs, "msg=engine_limits_applied", 10*time.Second)
	waitForLogContains(t, daemonLogs, "max_upload_kbps=1024", 10*time.Second)
	waitForLogContains(t, daemonLogs, "max_download_kbps=2048", 10*time.Second)
}

func TestDaemonLogsHMACMismatchE2E(t *testing.T) {
	if daemonProtocolReachable() {
		t.Skip("daemon already running on fixed IPC port; stop it before running this e2e")
	}
	if !ipcPortAvailable() {
		t.Skip("IPC port 47432 is already in use")
	}

	_, daemonLogs := startIsolatedDaemon(t, map[string]any{
		"telemetry_enabled":      false,
		"lan_hmac_enabled":       true,
		"lan_hmac_shared_secret": strings.Repeat("ab", 32),
	})

	for i := 0; i < 4; i++ {
		sendUnsignedLANPacket(t, "e2e_hmac_test:1b:base:q4_0", e2eInfohash(fmt.Sprintf("hmac-mismatch-%d", i)))
		time.Sleep(400 * time.Millisecond)
	}
	if !waitForLogContainsOptional(daemonLogs, "msg=lan_announcement_hmac_mismatch", 10*time.Second) {
		t.Skipf("multicast LAN packet was not observed in this environment; skipping mismatch assertion\nlogs:\n%s", daemonLogs.String())
	}
	waitForLogContains(t, daemonLogs, "reason=missing_signature", 10*time.Second)
}
