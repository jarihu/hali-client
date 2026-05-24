package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hali/internal/cache"
	"hali/internal/config"
	"hali/internal/events"
	"hali/internal/torrent"
)

// newTestServer creates a Server on ephemeral ports. The IPC accept loop runs
// in a background goroutine. Returns the server and the bound IPC address.
func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	torrentDir := t.TempDir()

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

	addr := srv.ipcLn.Addr().String()
	go func() {
		_ = srv.serveIPC()
	}()
	return srv, addr
}

// sendIPC dials the daemon addr, sends req, decodes and returns Response.
func sendIPC(t *testing.T, addr string, req Request) Response {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode request: %v", err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestServerStartBindsPort(t *testing.T) {
	_, addr := newTestServer(t)
	if addr == "" {
		t.Error("IPC address is empty after start")
	}
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Errorf("cannot reach IPC port after start: %v", err)
	} else {
		conn.Close()
	}
}

func TestServerStopClosesPort(t *testing.T) {
	srv, addr := newTestServer(t)

	srv.Stop()
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Error("IPC port still reachable after Stop")
	}
}

func TestServerUnknownCommand(t *testing.T) {
	_, addr := newTestServer(t)

	resp := sendIPC(t, addr, Request{Cmd: "invalid_command"})
	if resp.OK {
		t.Error("unknown command should return OK=false")
	}
	if resp.Error == "" {
		t.Error("unknown command should return a non-empty error message")
	}
}

func TestServerCmdListReturnsExplicitError(t *testing.T) {
	_, addr := newTestServer(t)

	resp := sendIPC(t, addr, Request{Cmd: CmdList})
	if resp.OK {
		t.Fatal("CmdList should return OK=false")
	}
	if !strings.Contains(resp.Error, "not served by daemon IPC") {
		t.Fatalf("CmdList error = %q, want explicit guidance", resp.Error)
	}
}

func TestServerInvalidJSONHandledGracefully(t *testing.T) {
	_, addr := newTestServer(t)

	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	conn.Write([]byte("{not valid json\n")) //nolint:errcheck

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Skipf("server closed conn before response (also valid behavior): %v", err)
	}
	if resp.OK {
		t.Error("invalid JSON should return OK=false")
	}
}

func TestServerStatsCommandZeroTorrents(t *testing.T) {
	_, addr := newTestServer(t)

	resp := sendIPC(t, addr, Request{Cmd: CmdStats})
	if !resp.OK {
		t.Fatalf("CmdStats failed: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Error("CmdStats should return a StatsSnapshot")
	}

	data, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("re-marshal StatsSnapshot: %v", err)
	}
	var snap torrent.StatsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal StatsSnapshot: %v", err)
	}
	if snap.ActiveDLs < 0 {
		t.Error("ActiveDLs should be non-negative")
	}
}

func TestServerStopViaCommand(t *testing.T) {
	_, addr := newTestServer(t)

	resp := sendIPC(t, addr, Request{Cmd: CmdStop})
	if !resp.OK {
		t.Errorf("CmdStop returned OK=false: %s", resp.Error)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return
		}
		conn.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("daemon still reachable after CmdStop")
}

func TestServerStopIdempotent(t *testing.T) {
	srv, _ := newTestServer(t)

	srv.Stop()
	srv.Stop()
	srv.Stop()
}

func TestPartialIPCWriteHandledGracefully(t *testing.T) {
	_, addr := newTestServer(t)

	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Write([]byte(`{"cmd":"st`)) //nolint:errcheck
	conn.Close()

	time.Sleep(200 * time.Millisecond)
	resp := sendIPC(t, addr, Request{Cmd: CmdStats})
	if !resp.OK {
		t.Errorf("server should still work after partial write; got error: %s", resp.Error)
	}
}

func TestHandleSeedRejectsPathTraversal(t *testing.T) {
	modelsRoot := t.TempDir()
	t.Setenv("HALI_MODELS_DIR", modelsRoot)

	_, addr := newTestServer(t)

	bad := []string{
		"/etc",
		filepath.Join(modelsRoot, "../../etc"),
		filepath.Join(modelsRoot, "../evil"),
	}
	for _, dir := range bad {
		resp := sendIPC(t, addr, Request{
			Cmd:      CmdSeed,
			Dir:      dir,
			Filename: "model.gguf",
			ModelID:  "test:7b:base:q4_0",
		})
		if resp.OK {
			t.Errorf("handleSeed dir=%q should be rejected, got OK=true", dir)
		}
	}
}

func TestHandleSeedRejectsUnsafeFilename(t *testing.T) {
	modelsRoot := t.TempDir()
	t.Setenv("HALI_MODELS_DIR", modelsRoot)

	_, addr := newTestServer(t)

	bad := []string{
		"../evil.gguf",
		"CON",
		"NUL.gguf",
		"a/b.gguf",
		"",
	}
	for _, filename := range bad {
		resp := sendIPC(t, addr, Request{
			Cmd:      CmdSeed,
			Dir:      modelsRoot,
			Filename: filename,
			ModelID:  "test:7b:base:q4_0",
		})
		if resp.OK {
			t.Errorf("handleSeed filename=%q should be rejected, got OK=true", filename)
		}
	}
}

func TestHandleEnqueueEventPersistsQueuedEvent(t *testing.T) {
	serviceDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", serviceDir)
	dataDir := t.TempDir()
	torrentDir := t.TempDir()

	engine, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(engine.Close)

	store := &cache.Store{Root: dataDir}
	stats := torrent.NewStatsCollector(engine)
	srv := NewServer(engine, store, stats)
	srv.eventWorker = events.NewWorker(filepath.Join(serviceDir, "events"), config.LoadService)
	event := events.ModelPullEvent{
		ModelID:   "mistral:7b:instruct:q4_k_m",
		Revision:  "abc123",
		InfoHash:  "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Magnet:    "magnet:?xt=urn:btih:deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		SourceURL: "https://example.invalid/model.gguf",
		LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp: time.Unix(123, 0).UTC(),
	}

	resp := srv.handleEnqueueEvent(Request{Cmd: CmdEnqueueEvent, Event: &event, AllowUnreachablePublish: true})
	if !resp.OK {
		t.Fatalf("CmdEnqueueEvent failed: %s", resp.Error)
	}

	queued, err := events.NewQueue(filepath.Join(serviceDir, "events")).List()
	if err != nil {
		t.Fatalf("List queued events: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("queued = %d, want 1", len(queued))
	}
	if queued[0].Event != event {
		t.Fatalf("queued event mismatch: %+v vs %+v", queued[0].Event, event)
	}
}

func TestHandleDownloadRejectsPathTraversal(t *testing.T) {
	modelsRoot := t.TempDir()
	t.Setenv("HALI_MODELS_DIR", modelsRoot)

	_, addr := newTestServer(t)

	const validIH = "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	bad := []string{
		"/etc",
		filepath.Join(modelsRoot, "../../etc"),
		filepath.Join(modelsRoot, "../evil"),
	}
	for _, dir := range bad {
		resp := sendIPC(t, addr, Request{
			Cmd:      CmdDownload,
			Dir:      dir,
			ModelID:  "test:7b:base:q4_0",
			Infohash: validIH,
		})
		if resp.OK {
			t.Errorf("handleDownload dir=%q should be rejected, got OK=true", dir)
		}
	}
}

func TestIPCClientReconnectAfterDaemonRestart(t *testing.T) {
	sharedDataDir := t.TempDir()

	startDaemon := func(torrentDir string) (*Server, *torrent.Engine, string) {
		engine, err := torrent.NewEngine(sharedDataDir, torrentDir)
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		store := &cache.Store{Root: sharedDataDir}
		stats := torrent.NewStatsCollector(engine)
		srv := NewServer(engine, store, stats)
		if err := srv.startOnAddrs("127.0.0.1:0", "127.0.0.1:0"); err != nil {
			engine.Close()
			t.Fatalf("startOnAddrs: %v", err)
		}
		addr := srv.ipcLn.Addr().String()
		go func() {
			_ = srv.serveIPC()
		}()
		return srv, engine, addr
	}

	torrentDir1 := t.TempDir()
	srv1, engine1, addr1 := startDaemon(torrentDir1)

	resp := sendIPC(t, addr1, Request{Cmd: CmdStats})
	if !resp.OK {
		t.Fatalf("first daemon not responding: %s", resp.Error)
	}

	srv1.Stop()
	engine1.Close()
	time.Sleep(200 * time.Millisecond)

	torrentDir2 := t.TempDir()
	_, engine2, addr2 := startDaemon(torrentDir2)
	t.Cleanup(engine2.Close)

	resp2 := sendIPC(t, addr2, Request{Cmd: CmdStats})
	if !resp2.OK {
		t.Errorf("new daemon not responding after restart: %s", resp2.Error)
	}
}

func TestHandleLanSeenObservationalSnapshot(t *testing.T) {
	srv, addr := newTestServer(t)

	now := time.Now()
	srv.lanIndex.mu.Lock()
	srv.lanIndex.entries = map[string][]ModelHint{
		"mistral:7b:instruct:q4_k_m": {
			{Identity: torrent.TorrentIdentity{InfohashV1: ih1}, Revision: "r1", PubkeyHash: "node-a", SeenAt: now.Add(-30 * time.Second)},
			{Identity: torrent.TorrentIdentity{InfohashV1: ih1}, Revision: "r1", PubkeyHash: "node-b", SeenAt: now.Add(-45 * time.Second)},
			{Identity: torrent.TorrentIdentity{InfohashV1: ih1}, Revision: "r1", PubkeyHash: "node-b", SeenAt: now.Add(-20 * time.Second)}, // dedupe same node
		},
		"llama:8b:instruct:q4_0": {
			{Identity: torrent.TorrentIdentity{InfohashV1: ih2}, Revision: "r2", PubkeyHash: "node-c", SeenAt: now.Add(-(lanObservedHardTTL + time.Minute))},
		},
	}
	srv.lanIndex.mu.Unlock()

	resp := sendIPC(t, addr, Request{Cmd: CmdLanSeen})
	if !resp.OK {
		t.Fatalf("CmdLanSeen failed: %s", resp.Error)
	}

	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal LanSeenData: %v", err)
	}
	var seen LanSeenData
	if err := json.Unmarshal(raw, &seen); err != nil {
		t.Fatalf("unmarshal LanSeenData: %v", err)
	}

	if len(seen.Announcements) != 1 {
		t.Fatalf("announcements = %d, want 1 recent entry", len(seen.Announcements))
	}
	got := seen.Announcements[0]
	if got.ModelID != "mistral:7b:instruct:q4_k_m" {
		t.Fatalf("model_id = %q", got.ModelID)
	}
	if got.ArtifactKey == "" {
		t.Fatal("artifact_key should be set")
	}
	if got.PeerCount != 2 {
		t.Fatalf("peer_count = %d, want 2 unique nodes", got.PeerCount)
	}
	if got.LastSeen <= 0 {
		t.Fatalf("last_seen = %d, want unix timestamp", got.LastSeen)
	}
}

func TestHandleLanQueryIncludesPeerFacts(t *testing.T) {
	srv, addr := newTestServer(t)

	now := time.Now()
	srv.lanIndex.mu.Lock()
	srv.lanIndex.entries["mistral:7b:instruct:q4_k_m"] = []ModelHint{
		{Identity: torrent.TorrentIdentity{InfohashV1: ih1}, Revision: "r1", PubkeyHash: "node-a", SeenAt: now.Add(-15 * time.Second)},
		{Identity: torrent.TorrentIdentity{InfohashV1: ih1}, Revision: "r1", PubkeyHash: "node-b", SeenAt: now.Add(-10 * time.Second)},
		{Identity: torrent.TorrentIdentity{InfohashV1: ih3}, Revision: "r1", PubkeyHash: "node-c", SeenAt: now.Add(-3 * time.Second)},
	}
	srv.lanIndex.mu.Unlock()

	resp := sendIPC(t, addr, Request{Cmd: CmdLanQuery, ModelID: "mistral:7b:instruct:q4_k_m"})
	if !resp.OK {
		t.Fatalf("CmdLanQuery failed: %s", resp.Error)
	}

	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal LanQueryData: %v", err)
	}
	var got LanQueryData
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal LanQueryData: %v", err)
	}

	if got.Infohash != ih3 {
		t.Fatalf("selected infohash = %q, want freshest %q", got.Infohash, ih3)
	}
	if got.ArtifactKey == "" {
		t.Fatal("artifact_key should be set")
	}
	if got.PeerCount != 1 {
		t.Fatalf("peer_count = %d, want 1 for selected infohash", got.PeerCount)
	}
	if got.LastSeen <= 0 {
		t.Fatalf("last_seen = %d, want unix timestamp", got.LastSeen)
	}
}

func TestServerStatusLANUsesRecentObservations(t *testing.T) {
	srv, addr := newTestServer(t)

	now := time.Now()
	srv.lanIndex.mu.Lock()
	srv.lanIndex.entries = map[string][]ModelHint{
		"recent:7b:base:q4_0": {
			{Identity: torrent.TorrentIdentity{InfohashV1: ih1}, PubkeyHash: "node-a", SeenAt: now.Add(-10 * time.Second)},
		},
		"stale:7b:base:q4_0": {
			{Identity: torrent.TorrentIdentity{InfohashV1: ih2}, PubkeyHash: "node-b", SeenAt: now.Add(-(lanObservedHardTTL + time.Minute))},
		},
	}
	srv.lanIndex.mu.Unlock()

	resp := sendIPC(t, addr, Request{Cmd: CmdStatus})
	if !resp.OK {
		t.Fatalf("CmdStatus failed: %s", resp.Error)
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal StatusData: %v", err)
	}
	var status StatusData
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatalf("unmarshal StatusData: %v", err)
	}

	if len(status.LAN) != 1 {
		t.Fatalf("status LAN rows = %d, want 1", len(status.LAN))
	}
	if !strings.HasPrefix(status.LAN[0].ModelID, "recent:") {
		t.Fatalf("unexpected LAN row model_id: %s", status.LAN[0].ModelID)
	}
}

func TestSelectHintPrefersSoftTTLFreshHint(t *testing.T) {
	now := time.Now()
	hints := []ModelHint{
		{Identity: torrent.TorrentIdentity{InfohashV1: ih1}, SeenAt: now.Add(-3 * time.Minute)},
		{Identity: torrent.TorrentIdentity{InfohashV1: ih2}, SeenAt: now.Add(-30 * time.Second)},
	}
	best := selectHint(hints)
	if best == nil {
		t.Fatal("selectHint returned nil")
	}
	if best.Identity.InfohashV1 != ih2 {
		t.Fatalf("selectHint picked %q, want fresh %q", best.Identity.InfohashV1, ih2)
	}
}

func TestSelectHintFallsBackToHardTTL(t *testing.T) {
	now := time.Now()
	hints := []ModelHint{
		{Identity: torrent.TorrentIdentity{InfohashV1: ih1}, SeenAt: now.Add(-5 * time.Minute)},
		{Identity: torrent.TorrentIdentity{InfohashV1: ih2}, SeenAt: now.Add(-3 * time.Minute)},
	}
	best := selectHint(hints)
	if best == nil {
		t.Fatal("selectHint returned nil")
	}
	if best.Identity.InfohashV1 != ih2 {
		t.Fatalf("selectHint picked %q, want newest hard-TTL fallback %q", best.Identity.InfohashV1, ih2)
	}
}
