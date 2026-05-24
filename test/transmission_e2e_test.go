//go:build integration

package test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hali/internal/daemon"
)

// TestTransmissionSeedingRegistrationE2E verifies that when a model is seeded
// the Transmission hook fires and registers the torrent via the RPC API.
// It uses a mock Transmission server so no real Transmission instance is needed.
func TestTransmissionSeedingRegistrationE2E(t *testing.T) {
	if daemonProtocolReachable() {
		t.Skip("daemon already running on fixed IPC port; stop it before running this e2e")
	}
	if !ipcPortAvailable() {
		t.Skip("IPC port 47432 is already in use")
	}

	// Mock Transmission RPC server: enforces session-ID handshake, records torrent-add calls.
	var addCalls int64
	var capturedDir string
	txMux := http.NewServeMux()
	txMux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "e2e-test-sid")
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var rpcReq struct {
			Method    string         `json:"method"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(body, &rpcReq)
		if rpcReq.Method == "torrent-add" {
			atomic.AddInt64(&addCalls, 1)
			if dir, ok := rpcReq.Arguments["download-dir"].(string); ok {
				capturedDir = dir
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"result":"success","arguments":{"torrent-added":{"hashString":"`+strings.Repeat("a", 40)+`","id":1,"name":"e2e-tx-model"}}}`) //nolint:errcheck
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"result":"success","arguments":{}}`) //nolint:errcheck
	})
	txSrv := httptest.NewServer(txMux)
	defer txSrv.Close()

	// Start an isolated daemon with Transmission pointing at the mock.
	_, daemonLogs := startIsolatedDaemon(t, map[string]any{
		"telemetry_enabled": false,
		"lan_hmac_enabled":  false,
		"debug":             true,
		"transmission":      map[string]any{"url": txSrv.URL},
	})

	// Confirm the hook registered before we emit events.
	waitForLogContains(t, daemonLogs, "transmission: seeding hook registered", 10*time.Second)

	// Write a small fake model file inside the service data models dir.
	serviceDir := os.Getenv("HALI_SERVICE_DATA_DIR")
	modelsRoot := filepath.Join(serviceDir, "models")
	modelDir := filepath.Join(modelsRoot, "e2e-tx-model", "q4_0")
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatalf("MkdirAll model dir: %v", err)
	}
	const filename = "e2e-tx-model.Q4_0.gguf"
	// 64 KiB of content — small enough to hash quickly, large enough to produce a real torrent.
	content := make([]byte, 1<<16)
	for i := range content {
		content[i] = byte(i)
	}
	if err := os.WriteFile(filepath.Join(modelDir, filename), content, 0644); err != nil {
		t.Fatalf("WriteFile model file: %v", err)
	}

	// Kick off torrent hashing via CmdSeed.
	seedResp, err := daemon.DefaultClient().Send(daemon.Request{
		Cmd:        daemon.CmdSeed,
		Dir:        modelDir,
		Filename:   filename,
		ModelID:    "e2e-tx-model:7b:base:q4_0",
		HFRepo:     "e2e/tx-model-gguf",
		HFRevision: "abc123",
	})
	if err != nil || !seedResp.OK {
		t.Fatalf("CmdSeed failed: err=%v resp=%+v\nlogs:\n%s", err, seedResp, daemonLogs.String())
	}
	raw, _ := json.Marshal(seedResp.Data)
	var seedData map[string]string
	if err := json.Unmarshal(raw, &seedData); err != nil {
		t.Fatalf("decode seed response data: %v", err)
	}
	jobID := seedData["job_id"]
	if jobID == "" {
		t.Fatalf("empty job_id in seed response\nlogs:\n%s", daemonLogs.String())
	}

	// Poll CmdSeedStatus until done. Passing Dir triggers the TorrentPublishedEvent emit.
	var infohash string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		statusResp, serr := daemon.DefaultClient().Send(daemon.Request{
			Cmd:   daemon.CmdSeedStatus,
			JobID: jobID,
			Dir:   modelDir,
		})
		if serr != nil || !statusResp.OK {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		sraw, _ := json.Marshal(statusResp.Data)
		var sd daemon.SeedStatusData
		if json.Unmarshal(sraw, &sd) == nil {
			if sd.Done {
				if sd.Error != "" {
					t.Fatalf("seed job error: %s\nlogs:\n%s", sd.Error, daemonLogs.String())
				}
				infohash = sd.Infohash
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if infohash == "" {
		t.Fatalf("seed job did not complete within 60s\nlogs:\n%s", daemonLogs.String())
	}
	t.Logf("seed complete: infohash=%s", infohash)

	// Wait for the hook goroutine to call torrent-add on the mock.
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&addCalls) > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if n := atomic.LoadInt64(&addCalls); n == 0 {
		t.Fatalf("mock Transmission received no torrent-add call within 15s\nlogs:\n%s", daemonLogs.String())
	}
	t.Logf("transmission: received %d torrent-add call(s)", atomic.LoadInt64(&addCalls))

	// download-dir must point to the model content directory (forward slashes).
	if capturedDir == "" {
		t.Error("torrent-add missing download-dir argument")
	}
	if strings.Contains(capturedDir, `\`) {
		t.Errorf("download-dir contains backslashes: %q", capturedDir)
	}
}
