package torrent

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testEnv holds a real Engine with all paths under a manually managed base dir.
// We manage cleanup ourselves so we can retry removal on Windows after giving
// anacrolix/torrent's boltdb time to release its file handles.
type testEnv struct {
	engine     *Engine
	base       string
	torrentDir string
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	base, err := os.MkdirTemp("", "hali-torrent-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	torrentDir := filepath.Join(base, "torrents")
	dataDir := filepath.Join(base, "data")
	for _, d := range []string{torrentDir, dataDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			os.RemoveAll(base)
			t.Fatalf("MkdirAll %s: %v", d, err)
		}
	}

	e, err := NewEngine(dataDir, torrentDir)
	if err != nil {
		os.RemoveAll(base)
		t.Fatalf("NewEngine: %v", err)
	}

	t.Cleanup(func() {
		e.Close()
		for i := 0; i < 15; i++ {
			time.Sleep(200 * time.Millisecond)
			if err := os.RemoveAll(base); err == nil {
				return
			}
		}
		os.RemoveAll(base) //nolint:errcheck
	})

	return &testEnv{engine: e, base: base, torrentDir: torrentDir}
}

// modelDir creates and returns a subdirectory of env.base for storing model files.
func (env *testEnv) modelDir(name string) string {
	dir := filepath.Join(env.base, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		panic(err)
	}
	return dir
}

func writeModelFile(t *testing.T, dir string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), content, 0644); err != nil {
		t.Fatalf("write model file: %v", err)
	}
}

func TestSeedStatusTransitionsToSeeding(t *testing.T) {
	env := newEnv(t)
	dir := env.modelDir("seed-status")
	writeModelFile(t, dir, []byte("fake model content for seeding test"))

	ih, err := env.engine.Seed(dir, "model.gguf", "test:1b:base:q4_0", "", "")
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if ih == "" {
		t.Error("Seed returned empty infohash")
	}

	found := false
	for _, entry := range env.engine.Entries() {
		if entry.ModelID == "test:1b:base:q4_0" {
			found = true
			if entry.Status != StatusSeeding {
				t.Errorf("Status = %q after Seed, want %q", entry.Status, StatusSeeding)
			}
			if entry.Identity.InfohashV1 != ih {
				t.Errorf("Entry InfohashV1 = %q, want %q", entry.Identity.InfohashV1, ih)
			}
			if !strings.Contains(entry.MagnetURI, "xt=urn:btih:"+ih) {
				t.Errorf("entry magnet = %q, expected btih for %s", entry.MagnetURI, ih)
			}
		}
	}
	if !found {
		t.Error("model entry not found in Entries() after Seed")
	}
}

func TestSeedSameContentSameInfohash(t *testing.T) {
	env := newEnv(t)
	content := []byte("deterministic content - must produce identical infohash each time")

	dir1 := env.modelDir("same-1")
	writeModelFile(t, dir1, content)
	ih1, err := env.engine.Seed(dir1, "model.gguf", "same:1b:base:q4_0", "", "")
	if err != nil {
		t.Fatalf("first Seed: %v", err)
	}

	dir2 := env.modelDir("same-2")
	writeModelFile(t, dir2, content)
	ih2, err := env.engine.Seed(dir2, "model.gguf", "same:1b:base:q4_1", "", "")
	if err != nil {
		t.Fatalf("second Seed: %v", err)
	}

	if ih1 != ih2 {
		t.Errorf("same content produced different infohashes: %s vs %s", ih1, ih2)
	}
}

func TestSeedDifferentContentDifferentInfohash(t *testing.T) {
	env := newEnv(t)

	dir1 := env.modelDir("diff-a")
	writeModelFile(t, dir1, []byte("content-A"))
	ih1, err := env.engine.Seed(dir1, "model.gguf", "diff:1b:base:q4_0", "", "")
	if err != nil {
		t.Fatalf("Seed fileA: %v", err)
	}

	dir2 := env.modelDir("diff-b")
	writeModelFile(t, dir2, []byte("content-B"))
	ih2, err := env.engine.Seed(dir2, "model.gguf", "diff:1b:base:q4_1", "", "")
	if err != nil {
		t.Fatalf("Seed fileB: %v", err)
	}

	if ih1 == ih2 {
		t.Error("different content produced identical infohashes — identity stability rule violated")
	}
}

func TestDeterministicInfohashTwoEngineInstances(t *testing.T) {
	// Two independent Engine instances with identical content must produce the same infohash.
	// This is the cross-machine interoperability invariant: any nondeterminism (path, timestamp,
	// field ordering) would break swarm compatibility across machines.
	content := make([]byte, 32*1024)
	if _, err := io.ReadFull(rand.Reader, content); err != nil {
		t.Fatalf("rand: %v", err)
	}

	const (
		modelID  = "crossmachine:7b:instruct:q4_k_m"
		revision = "abc123def456"
		hfRepo   = "org/Test-Model-GGUF"
	)

	env1 := newEnv(t)
	dir1 := env1.modelDir("model")
	writeModelFile(t, dir1, content)
	ih1, err := env1.engine.Seed(dir1, "model.gguf", modelID, hfRepo, revision)
	if err != nil {
		t.Fatalf("engine1 Seed: %v", err)
	}

	env2 := newEnv(t)
	dir2 := env2.modelDir("model")
	writeModelFile(t, dir2, content)
	ih2, err := env2.engine.Seed(dir2, "model.gguf", modelID, hfRepo, revision)
	if err != nil {
		t.Fatalf("engine2 Seed: %v", err)
	}

	if ih1 != ih2 {
		t.Errorf(
			"two Engine instances produced different infohashes for identical content:\n  engine1: %s\n  engine2: %s\nThis breaks cross-machine swarm compatibility.",
			ih1, ih2,
		)
	}
}

func TestSeedFromTorrentFileMatchesOriginal(t *testing.T) {
	// Verify SeedFromTorrentFile loads the existing .torrent and returns the same infohash.
	// This is the restart invariant: daemon restart must reuse .torrent files, not rehash.
	env1 := newEnv(t)
	content := []byte("restart recovery test content")
	dir1 := env1.modelDir("model")
	writeModelFile(t, dir1, content)

	ih1, err := env1.engine.Seed(dir1, "model.gguf", "restore:1b:base:q4_0", "", "")
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// Engine2 uses the same torrentDir (where the .torrent file was written) but a fresh
	// model dir — avoiding a boltdb conflict on the same directory.
	// This simulates a daemon restart where the daemon re-opens the torrent files.
	env2 := newEnv(t)
	dir2 := env2.modelDir("model")
	writeModelFile(t, dir2, content) // identical content

	// Move the .torrent file into env2's torrentDir so SeedFromTorrentFile can find it.
	torrentSrc := filepath.Join(env1.torrentDir, ih1+".torrent")
	torrentDst := filepath.Join(env2.torrentDir, ih1+".torrent")
	torrentData, err := os.ReadFile(torrentSrc)
	if err != nil {
		t.Fatalf("read torrent file: %v", err)
	}
	if err := os.WriteFile(torrentDst, torrentData, 0644); err != nil {
		t.Fatalf("write torrent file: %v", err)
	}

	ih2, err := env2.engine.SeedFromTorrentFile(dir2, ih1, "restore:1b:base:q4_0-restart", TorrentIdentity{})
	if err != nil {
		t.Fatalf("SeedFromTorrentFile: %v", err)
	}

	if ih1 != ih2 {
		t.Errorf("SeedFromTorrentFile returned different infohash: %s vs %s — restart would break seeding", ih1, ih2)
	}
}

func TestStartDownloadBadInfohashRejected(t *testing.T) {
	env := newEnv(t)
	_, err := env.engine.StartDownload(env.modelDir("bad"), "bad:1b:base:q4_0", "notahexinfohash", "", nil)
	if err == nil {
		t.Error("StartDownload should reject invalid infohash")
	}
}

func TestStartDownloadJobStatusTracked(t *testing.T) {
	env := newEnv(t)
	fakeIH := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	fakeV2 := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	jobID, err := env.engine.StartDownload(env.modelDir("tracked"), "track:1b:base:q4_0", fakeIH, fakeV2, nil)
	if err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	if jobID == "" {
		t.Error("StartDownload returned empty jobID")
	}

	job, ok := env.engine.JobStatus(jobID)
	if !ok {
		t.Fatal("JobStatus returned false for active job")
	}
	if job.ModelID != "track:1b:base:q4_0" {
		t.Errorf("JobStatus ModelID = %q, want track:1b:base:q4_0", job.ModelID)
	}
	if job.Identity.InfohashV1 != fakeIH {
		t.Errorf("JobStatus InfohashV1 = %q, want %q", job.Identity.InfohashV1, fakeIH)
	}
	if !strings.Contains(job.MagnetURI, "xt=urn:btih:"+fakeIH) {
		t.Errorf("JobStatus MagnetURI = %q, want xt for %q", job.MagnetURI, fakeIH)
	}
	if !strings.Contains(job.MagnetURI, "xt=urn:btmh:1220"+fakeV2) {
		t.Errorf("JobStatus MagnetURI = %q, want btmh xt for %q", job.MagnetURI, fakeV2)
	}
}

func TestStartDownloadMetadataTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("timeout test skipped in short mode (takes ~60s)")
	}
	// StartDownload with a valid infohash but no peers/webseeds must eventually
	// mark the job done with a timeout error — it must not hang forever.
	env := newEnv(t)
	fakeIH := "cafebabecafebabecafebabecafebabecafebabe"
	jobID, err := env.engine.StartDownload(env.modelDir("timeout"), "timeout:1b:base:q4_0", fakeIH, "", nil)
	if err != nil {
		t.Fatalf("StartDownload: %v", err)
	}

	deadline := time.Now().Add(65 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := env.engine.JobStatus(jobID)
		if !ok {
			t.Fatal("job disappeared")
		}
		if job.Done {
			if job.Error == "" {
				t.Error("timed-out job should have non-empty error")
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Error("download job did not time out within 65 seconds")
}

func TestStartDownloadWithDirectPeerAddr(t *testing.T) {
	if strings.TrimSpace(os.Getenv("HALI_E2E_DIRECT_PEER")) == "" {
		t.Skip("set HALI_E2E_DIRECT_PEER=1 to run direct peer transfer e2e")
	}
	if testing.Short() {
		t.Skip("integration-like transfer test skipped in short mode")
	}

	seeder := newEnv(t)
	downloader := newEnv(t)

	content := []byte("direct-peer-bootstrap-e2e-content")
	seedDir := seeder.modelDir("seed-direct")
	writeModelFile(t, seedDir, content)

	modelID := "direct:1b:base:q4_0"
	ih, err := seeder.engine.Seed(seedDir, "model.gguf", modelID, "", "")
	if err != nil {
		t.Fatalf("seed engine Seed: %v", err)
	}
	ihV2 := ""
	for _, entry := range seeder.engine.Entries() {
		if entry.ModelID != modelID {
			continue
		}
		ihV2 = entry.Identity.InfohashV2
		break
	}

	downloadDir := downloader.modelDir("download-direct")
	port := seeder.engine.Port()
	peerAddrs := []string{
		fmt.Sprintf("127.0.0.1:%d", port),
		fmt.Sprintf("[::1]:%d", port),
	}
	jobID, err := downloader.engine.StartDownload(downloadDir, modelID, ih, ihV2, peerAddrs)
	if err != nil {
		t.Fatalf("downloader StartDownload: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := downloader.engine.JobStatus(jobID)
		if !ok {
			t.Fatal("download job disappeared")
		}
		if job.Done {
			if job.Error != "" {
				t.Fatalf("download job failed: %s", job.Error)
			}
			if job.Filename != "model.gguf" {
				t.Fatalf("filename = %q, want model.gguf", job.Filename)
			}
			if job.Total != int64(len(content)) {
				t.Fatalf("total = %d, want %d", job.Total, len(content))
			}

			got, err := os.ReadFile(filepath.Join(downloadDir, "model.gguf"))
			if err != nil {
				t.Fatalf("ReadFile(downloaded): %v", err)
			}
			if !bytes.Equal(got, content) {
				t.Fatalf("downloaded content mismatch: got %q, want %q", string(got), string(content))
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("direct peer bootstrap transfer did not complete within 30s")
}
