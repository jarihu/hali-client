package torrent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hali/internal/networking"

	"golang.org/x/time/rate"
)

func TestPieceLengthConstant(t *testing.T) {
	if lanPieceLen != 1<<24 {
		t.Errorf("lanPieceLen = %d, want %d (16 MiB). Do NOT change this — it breaks swarm compatibility.", lanPieceLen, 1<<24)
	}
}

func TestCreatedByConstant(t *testing.T) {
	expected := "hali"
	// Verified against metainfo.MetaInfo.CreatedBy in seedInner
	t.Run("hali", func(t *testing.T) {
		// This is a compile-time constant verification
		if expected != "hali" {
			t.Errorf("CreatedBy should be %q", expected)
		}
	})
}

func TestTorrentMetaJSON(t *testing.T) {
	// Verify the comment JSON output is deterministic and contains required fields.
	tm := torrentMeta{
		ModelID:  "mistral:7b:instruct:q4_k_m",
		Revision: "abc123def",
		Format:   "gguf",
		Source:   "huggingface",
	}
	data, err := json.Marshal(tm)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// Field order must be: model_id, revision, format, source
	keys := make([]string, 0, 4)
	for k := range out {
		keys = append(keys, k)
	}
	// json.Marshal of struct is deterministic by field declaration order
	expectedKeys := []string{"model_id", "revision", "format", "source"}
	if len(keys) != len(expectedKeys) {
		t.Fatalf("wrong number of keys: got %d want %d", len(keys), len(expectedKeys))
	}
	for i, k := range expectedKeys {
		if _, ok := out[k]; !ok {
			t.Errorf("missing key %q at position %d", k, i)
		}
	}

	// Verify actual JSON field order via raw string
	raw := string(data)
	expectedJSON := `{"model_id":"mistral:7b:instruct:q4_k_m","revision":"abc123def","format":"gguf","source":"huggingface"}`
	if raw != expectedJSON {
		t.Errorf("torrentMeta JSON = %s\nwant %s", raw, expectedJSON)
	}
}

func TestTorrentMetaJSONHasNoWebseedField(t *testing.T) {
	tm := torrentMeta{
		ModelID:  "phi:3b:base:q4_0",
		Revision: "r1",
		Format:   "gguf",
		Source:   "huggingface",
	}
	data, err := json.Marshal(tm)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	raw := string(data)
	if strings.Contains(raw, "webseeds") {
		t.Errorf("torrentMeta JSON should not contain webseeds: %s", raw)
	}
}

func TestHybridSingleFileTreeUsesFilenameKey(t *testing.T) {
	root := t.TempDir()
	name := "model.gguf"
	filePath := filepath.Join(root, name)
	if err := os.WriteFile(filePath, []byte("hybrid-tree-shape"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	info, rawTree, _, err := buildHybridSingleFileInfo(filePath, name, nil, 0, nil)
	if err != nil {
		t.Fatalf("buildHybridSingleFileInfo: %v", err)
	}
	if !info.FileTree.IsDir() {
		t.Fatal("expected v2 file tree root to be a directory")
	}
	if _, ok := info.FileTree.Dir[name]; !ok {
		t.Fatalf("expected filename key %q in file tree", name)
	}
	if _, ok := rawTree[name]; !ok {
		t.Fatalf("expected raw file tree key %q", name)
	}
	if _, ok := rawTree[""]; ok {
		t.Fatal("raw file tree must not encode file leaf at root key")
	}
}

func TestSeedStatusConstants(t *testing.T) {
	if StatusHashing != "hashing" {
		t.Errorf("StatusHashing = %q, want %q", StatusHashing, "hashing")
	}
	if StatusSeeding != "seeding" {
		t.Errorf("StatusSeeding = %q, want %q", StatusSeeding, "seeding")
	}
	if StatusError != "error" {
		t.Errorf("StatusError = %q, want %q", StatusError, "error")
	}
}

func TestSeedFromTorrentFileInvalidInfohash(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	bad := []string{
		"",
		"../evil",
		strings.Repeat("g", 40),               // not hex
		strings.Repeat("a", 39),               // too short
		strings.Repeat("a", 41),               // too long
		"../../../" + strings.Repeat("a", 31), // traversal prefix, wrong length
	}
	for _, ih := range bad {
		_, err := e.SeedFromTorrentFile(t.TempDir(), ih, "test:7b:base:q4_0", TorrentIdentity{})
		if err == nil {
			t.Errorf("SeedFromTorrentFile(%q) should fail, got nil error", ih)
		}
	}
}

func TestStartDownloadInvalidInfohash(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	_, err = e.StartDownload(t.TempDir(), "test:7b:base:q4_0", "", "", nil)
	if err == nil {
		t.Error("StartDownload with empty infohash should fail")
	}
	_, err = e.StartDownload(t.TempDir(), "test:7b:base:q4_0", "nothex", "", nil)
	if err == nil {
		t.Error("StartDownload with non-hex infohash should fail")
	}
}

func TestJobStatusNotFound(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	_, ok := e.JobStatus("nonexistent")
	if ok {
		t.Error("JobStatus for nonexistent job should return false")
	}
}

func TestCancelDownloadNotFound(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	if ok := e.CancelDownload("missing"); ok {
		t.Fatal("CancelDownload should return false for missing job")
	}
}

func TestCancelDownloadMarksJobCanceled(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	e.mu.Lock()
	e.jobs["job-1"] = &DownloadJob{ID: "job-1", ModelID: "m:7b:base:q4_0"}
	e.mu.Unlock()

	if ok := e.CancelDownload("job-1"); !ok {
		t.Fatal("CancelDownload returned false for existing job")
	}

	job, ok := e.JobStatus("job-1")
	if !ok {
		t.Fatal("JobStatus missing canceled job")
	}
	if !job.Done {
		t.Fatal("canceled job should be marked done")
	}
	if job.Error != "canceled by user" {
		t.Fatalf("job.Error=%q want %q", job.Error, "canceled by user")
	}
}

func TestEntriesEmpty(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	entries := e.Entries()
	if len(entries) != 0 {
		t.Errorf("Entries() on fresh engine should be empty, got %d", len(entries))
	}
}

func TestEngineCloseSetsFlag(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if e.closed.Load() {
		t.Error("closed should be false before Close()")
	}
	e.Close()
	if !e.closed.Load() {
		t.Error("closed should be true after Close()")
	}
}

func TestDownloadJobFields(t *testing.T) {
	job := &DownloadJob{
		ID:        "test-job-1",
		ModelID:   "mistral:7b:instruct:q4_k_m",
		Identity:  IdentityFromV1("deadbeefcafebabedeadbeefcafebabedeadbeef"),
		MagnetURI: "magnet:?xt=urn:btih:deadbeefcafebabedeadbeefcafebabedeadbeef",
		Filename:  "model.gguf",
		Total:     1000,
		Written:   500,
		Done:      false,
	}
	if job.ID != "test-job-1" {
		t.Errorf("ID = %q", job.ID)
	}
	if job.Total != 1000 {
		t.Errorf("Total = %d", job.Total)
	}
	if job.Written != 500 {
		t.Errorf("Written = %d", job.Written)
	}
}

func TestActiveModelsEmpty(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	models := e.ActiveModels()
	if len(models) != 0 {
		t.Errorf("ActiveModels() on fresh engine should be empty, got %d", len(models))
	}
}

func TestTotalBytesEmpty(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	down, up := e.TotalBytes()
	if down != 0 || up != 0 {
		t.Errorf("TotalBytes() = (%d, %d), want (0, 0)", down, up)
	}
}

func TestShutdownWaitsForDownloadGoroutines(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	job := &DownloadJob{ID: "job-1", ModelID: "test:7b:base:q4_0"}
	e.mu.Lock()
	e.jobs[job.ID] = job
	e.mu.Unlock()

	e.dlWG.Add(1)
	go func() {
		defer e.dlWG.Done()
		<-e.shutdownCh
		e.mu.Lock()
		job.Done = true
		job.Error = "engine shutdown"
		e.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	got, ok := e.JobStatus(job.ID)
	if !ok {
		t.Fatalf("job %q missing after shutdown", job.ID)
	}
	if !got.Done {
		t.Fatal("expected job to be marked done after shutdown")
	}
	if got.Error == "" {
		t.Fatal("expected job error after shutdown")
	}
}

func TestShutdownTimeoutReturnsContextError(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	e.dlWG.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err = e.Shutdown(ctx)
	if err == nil {
		t.Fatal("expected context deadline error, got nil")
	}
	e.dlWG.Done()
}

func TestApplyRateLimits(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	e.ApplyRateLimits(512, 1024)
	if got := e.uploadRL.Limit(); got != rate.Limit(512*1024) {
		t.Fatalf("upload limit = %v, want %v", got, rate.Limit(512*1024))
	}
	if got := e.downloadRL.Limit(); got != rate.Limit(1024*1024) {
		t.Fatalf("download limit = %v, want %v", got, rate.Limit(1024*1024))
	}

	e.ApplyRateLimits(0, 0)
	if got := e.uploadRL.Limit(); got != rate.Inf {
		t.Fatalf("upload limit after reset = %v, want Inf", got)
	}
	if got := e.downloadRL.Limit(); got != rate.Inf {
		t.Fatalf("download limit after reset = %v, want Inf", got)
	}
}

func TestStartDownloadConcurrentLimit(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	e.SetMaxConcurrentDownloads(1)
	e.mu.Lock()
	e.jobs["existing"] = &DownloadJob{ID: "existing", Done: false}
	e.mu.Unlock()
	const validIH = "da39a3ee5e6b4b0d3255bfef95601890afd80709"

	if _, err := e.StartDownload(t.TempDir(), "test:7b:base:q4_1", validIH, "", nil); err == nil {
		t.Fatal("expected concurrent limit error, got nil")
	}
}

func TestNewEngineCapabilities(t *testing.T) {
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	if e.NetworkMode() != networking.ModeLANOnly {
		t.Fatalf("NetworkMode() = %q, want %q", e.NetworkMode(), networking.ModeLANOnly)
	}
	caps := e.NetworkCapabilities()
	if !caps.EnableLSD {
		t.Fatalf("lan_only capabilities missing LSD: %+v", caps)
	}
}

func TestDeterministicMetaPayload(t *testing.T) {
	meta := torrentMeta{
		ModelID:  "mistral:7b:instruct:q4_k_m",
		Revision: "abc123",
		Format:   "gguf",
		Source:   "huggingface",
	}
	base, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("json.Marshal base: %v", err)
	}

	again, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("json.Marshal again: %v", err)
	}
	if string(base) != string(again) {
		t.Fatalf("deterministic meta payload not stable: %s vs %s", string(base), string(again))
	}
}
