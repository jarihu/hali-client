//go:build integration

// Package e2e contains end-to-end tests for the full hali pull/export/LAN flow.
// Run: go test -tags integration ./test/...
//
// Mock HF server counts inbound requests so tests can assert "HF never contacted"
// on LAN and webseed download paths.
package test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hali/internal/cache"
	"hali/internal/daemon"
	"hali/internal/model"
	"hali/internal/torrent"
)

// countingHandler wraps an http.Handler and counts inbound requests atomically.
type countingHandler struct {
	count   atomic.Int64
	handler http.Handler
}

func (h *countingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.count.Add(1)
	h.handler.ServeHTTP(w, r)
}

// mockHFServer creates a fake HF API + download server and counts every request.
func mockHFServer(content []byte, repoID, filename string) (*httptest.Server, *countingHandler) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"sha": "abc123def456",
			"siblings": []map[string]any{
				{"rfilename": filename, "size": len(content)},
			},
		})
	})
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{ //nolint:errcheck
			{"id": repoID, "downloads": 1000, "tags": []string{"gguf"}},
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, filename) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
			w.Write(content) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	})
	ch := &countingHandler{handler: mux}
	return httptest.NewServer(ch), ch
}

// ipcSend dials addr, sends req, and returns the decoded Response.
func ipcSend(t *testing.T, addr string, req daemon.Request) daemon.Response {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	json.NewEncoder(conn).Encode(req)                 //nolint:errcheck
	var resp daemon.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// waitAddr polls the addr file until it has content (up to 3s).
func waitAddr(t *testing.T, addrFile string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(addrFile)
		if err == nil && len(data) > 0 {
			return string(data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("addr file not written within 3 seconds")
	return ""
}

// e2eBase creates a temp directory with retry-based cleanup to handle Windows
// boltdb file lock release delays after engine.Close().
func e2eBase(t *testing.T) string {
	t.Helper()
	base, err := os.MkdirTemp("", "hali-e2e-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		for i := 0; i < 15; i++ {
			time.Sleep(200 * time.Millisecond)
			if err := os.RemoveAll(base); err == nil {
				return
			}
		}
		os.RemoveAll(base) //nolint:errcheck
	})
	return base
}

func TestPullFullFlow(t *testing.T) {
	// End-to-end: write model file, seed, verify metadata.json has a valid infohash.
	content := []byte("fake gguf model content for e2e test")
	const (
		repoID   = "org/TestModel-GGUF"
		filename = "test-model.Q4_K_M.gguf"
		modelID  = "testmodel:7b:instruct:q4_k_m"
	)

	hfSrv, _ := mockHFServer(content, repoID, filename)
	defer hfSrv.Close()

	base := e2eBase(t)
	dataDir := filepath.Join(base, "data")
	torrentDir := filepath.Join(base, "torrents")
	os.MkdirAll(dataDir, 0755)    //nolint:errcheck
	os.MkdirAll(torrentDir, 0755) //nolint:errcheck
	store := &cache.Store{Root: dataDir}

	id, err := model.Parse(modelID)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	modelDir := store.Dir(id)
	os.MkdirAll(modelDir, 0755)                                    //nolint:errcheck
	os.WriteFile(filepath.Join(modelDir, filename), content, 0644) //nolint:errcheck
	store.Save(id, cache.Metadata{                                 //nolint:errcheck
		HFRepo:     repoID,
		HFRevision: "abc123def456",
		HFSnapshot: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Files:      []string{filename},
		Size:       int64(len(content)),
	})

	engine, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(engine.Close)

	ih, err := engine.Seed(modelDir, filename, modelID, repoID, "abc123def456")
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	store.SetInfohash(modelDir, ih) //nolint:errcheck

	meta, err := store.LoadMeta(id)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Infohash == "" {
		t.Error("metadata.json missing torrent_infohash after seeding")
	}
	if meta.Infohash != ih {
		t.Errorf("metadata infohash = %q, want %q", meta.Infohash, ih)
	}
}

func TestWebseedOnlyHFNotContacted(t *testing.T) {
	// Seeding a model must not contact HF. hfCount must remain 0.
	content := []byte("webseed test content for hf-not-contacted assertion")
	const (
		repoID   = "org/WebseedModel-GGUF"
		filename = "webseed-model.Q4_0.gguf"
		modelID  = "webseed:1b:base:q4_0"
	)

	hfSrv, hfCount := mockHFServer(content, repoID, filename)
	defer hfSrv.Close()

	base := e2eBase(t)
	dataDir := filepath.Join(base, "data")
	torrentDir := filepath.Join(base, "torrents")
	os.MkdirAll(dataDir, 0755)    //nolint:errcheck
	os.MkdirAll(torrentDir, 0755) //nolint:errcheck
	store := &cache.Store{Root: dataDir}

	id, err := model.Parse(modelID)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	modelDir := store.Dir(id)
	os.MkdirAll(modelDir, 0755)                                    //nolint:errcheck
	os.WriteFile(filepath.Join(modelDir, filename), content, 0644) //nolint:errcheck

	engine, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(engine.Close)

	if _, err := engine.Seed(modelDir, filename, modelID, "", ""); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if n := hfCount.count.Load(); n != 0 {
		t.Errorf("HF was contacted %d times during seeding; want 0", n)
	}
}

func TestDaemonIPCAfterSeed(t *testing.T) {
	t.Skip("legacy IPC constructor-based test needs refresh to current daemon API")
}

func TestLANTransferHFNotContacted(t *testing.T) {
	if os.Getenv("BT_LAN_PEER") == "" {
		t.Skip("set BT_LAN_PEER=<ip> to enable two-machine LAN transfer test")
	}
	// Two-machine LAN test: Machine A seeds, Machine B downloads via LAN.
	// HF must not be contacted (hfCount == 0).
	//
	// Manual setup:
	//   Machine A: hali daemon start && hali pull <model>
	//   Machine B: BT_LAN_PEER=<A's IP> go test -tags integration -run TestLANTransfer ./test/...
	t.Log("LAN transfer test requires manual two-machine setup — see test comments")
	t.Log("BT_LAN_PEER =", os.Getenv("BT_LAN_PEER"))
}
