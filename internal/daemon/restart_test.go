package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"hali/internal/cache"
	"hali/internal/model"
	"hali/internal/torrent"
)

// restartBase creates shared dirs for restart tests.
// Returns (base, dataDir, torrentDir). Uses os.MkdirTemp so cleanup can be
// deferred until after all engine instances are closed (avoids boltdb lock on Windows).
func restartBase(t *testing.T) (base, dataDir, torrentDir string) {
	t.Helper()
	base, err := os.MkdirTemp("", "hali-restart-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	dataDir = filepath.Join(base, "data")
	torrentDir = filepath.Join(base, "torrents")
	for _, d := range []string{dataDir, torrentDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			os.RemoveAll(base)
			t.Fatalf("MkdirAll %s: %v", d, err)
		}
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
	return base, dataDir, torrentDir
}

// seedModelToStore writes model.gguf + metadata.json, seeds the torrent, and
// records the infohash in the store. Returns modelDir and infohash.
func seedModelToStore(t *testing.T, eng *torrent.Engine, store *cache.Store, id model.ModelID, content []byte) (modelDir, ih string) {
	t.Helper()
	modelDir = store.Dir(id)
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatalf("MkdirAll modelDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.gguf"), content, 0644); err != nil {
		t.Fatalf("write model.gguf: %v", err)
	}
	ih, err := eng.Seed(modelDir, "model.gguf", id.String(), "", "")
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if err := store.Save(id, cache.Metadata{
		HFRepo:     "org/repo",
		HFRevision: "abc123",
		HFSnapshot: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Files:      []string{"model.gguf"},
		Size:       int64(len(content)),
	}); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	if err := store.SetInfohash(modelDir, ih); err != nil {
		t.Fatalf("store.SetInfohash: %v", err)
	}
	return modelDir, ih
}

// waitForModelEntry polls engine.Entries() until modelID appears or deadline expires.
func waitForModelEntry(eng *torrent.Engine, modelID string, deadline time.Duration) bool {
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		for _, e := range eng.Entries() {
			if e.ModelID == modelID {
				return true
			}
		}
		select {
		case <-timer.C:
			return false
		case <-tick.C:
		}
	}
}

// startSrv creates and starts a Server with the given engine + store on ephemeral ports.
func startSrv(t *testing.T, eng *torrent.Engine, store *cache.Store) *Server {
	t.Helper()
	stats := torrent.NewStatsCollector(eng)
	srv := NewServer(eng, store, stats)
	if err := srv.startOnAddrs("127.0.0.1:0", "127.0.0.1:0"); err != nil {
		t.Fatalf("startOnAddrs: %v", err)
	}
	t.Cleanup(srv.Stop)
	go func() {
		_ = srv.serveIPC()
	}()
	return srv
}

func TestDaemonRestartReloadsSeededTorrents(t *testing.T) {
	_, dataDir, torrentDir := restartBase(t)
	store := &cache.Store{Root: dataDir}

	id, err := model.Parse("restart:7b:instruct:q4_k_m")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// First engine: seed the model.
	engine1, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		t.Fatalf("NewEngine1: %v", err)
	}
	_, ih1 := seedModelToStore(t, engine1, store, id, []byte("restart recovery test content"))
	engine1.Close()
	time.Sleep(500 * time.Millisecond) // let boltdb release file lock on Windows

	// Second engine: restart with same dirs — simulates daemon restart.
	engine2, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		t.Fatalf("NewEngine2: %v", err)
	}
	t.Cleanup(engine2.Close)

	startSrv(t, engine2, store)

	if !waitForModelEntry(engine2, id.String(), 15*time.Second) {
		t.Fatal("model did not appear in engine entries after restart")
	}

	var ih2 string
	for _, e := range engine2.Entries() {
		if e.ModelID == id.String() {
			ih2 = e.Identity.InfohashV1
		}
	}
	if ih1 != ih2 {
		t.Errorf("infohash changed across restart: before=%s after=%s", ih1, ih2)
	}
}

func TestRestartUsesTorrentFileWithoutRehash(t *testing.T) {
	// Guard: SeedFromTorrentFile must be used on restart (not Seed which rehashes).
	// Verified by checking the .torrent file was not re-created (mod time unchanged).
	_, dataDir, torrentDir := restartBase(t)
	store := &cache.Store{Root: dataDir}

	id, err := model.Parse("nohash:7b:instruct:q4_k_m")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	engine1, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		t.Fatalf("NewEngine1: %v", err)
	}
	_, ih1 := seedModelToStore(t, engine1, store, id, []byte("no-rehash invariant content"))

	torrentPath := filepath.Join(torrentDir, ih1+".torrent")
	statBefore, err := os.Stat(torrentPath)
	if err != nil {
		t.Fatalf("stat .torrent before restart: %v", err)
	}

	engine1.Close()
	// Sleep > 1s so any re-creation would produce a clearly different timestamp
	// (NTFS resolution is 100ns, FAT32 is 2s — 1.5s covers both).
	time.Sleep(1500 * time.Millisecond)

	engine2, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		t.Fatalf("NewEngine2: %v", err)
	}
	t.Cleanup(engine2.Close)

	startSrv(t, engine2, store)

	if !waitForModelEntry(engine2, id.String(), 15*time.Second) {
		t.Fatal("model did not appear in entries after restart")
	}

	statAfter, err := os.Stat(torrentPath)
	if err != nil {
		t.Fatalf("stat .torrent after restart: %v", err)
	}
	if !statAfter.ModTime().Equal(statBefore.ModTime()) {
		t.Error("torrent file was re-created on restart — rehash occurred; SeedFromTorrentFile must be used instead")
	}
}

func TestStatePersistsAcrossRestart(t *testing.T) {
	// metadata.json must be intact and hold the correct infohash after restart.
	_, dataDir, torrentDir := restartBase(t)
	store := &cache.Store{Root: dataDir}

	id, err := model.Parse("persist:7b:instruct:q4_k_m")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	engine1, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		t.Fatalf("NewEngine1: %v", err)
	}
	_, ih1 := seedModelToStore(t, engine1, store, id, []byte("persist state test content"))
	engine1.Close()
	time.Sleep(500 * time.Millisecond)

	// metadata.json must still hold the infohash before the second daemon starts.
	meta, err := store.LoadMeta(id)
	if err != nil {
		t.Fatalf("LoadMeta after engine1 close: %v", err)
	}
	if meta.Infohash != ih1 {
		t.Errorf("metadata infohash wrong before restart: got %q, want %q", meta.Infohash, ih1)
	}

	engine2, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		t.Fatalf("NewEngine2: %v", err)
	}
	t.Cleanup(engine2.Close)

	startSrv(t, engine2, store)

	if !waitForModelEntry(engine2, id.String(), 15*time.Second) {
		t.Fatal("model did not appear in entries after restart")
	}

	// Metadata must be unchanged after the restarted daemon reseeds the model.
	meta2, err := store.LoadMeta(id)
	if err != nil {
		t.Fatalf("LoadMeta after restart: %v", err)
	}
	if meta2.Infohash != ih1 {
		t.Errorf("metadata infohash changed after restart: got %q, want %q", meta2.Infohash, ih1)
	}
}
