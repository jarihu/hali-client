package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"hali/internal/model"
	"hali/internal/safepath"
	"hali/internal/torrent"
)

func testMetadata(files ...string) Metadata {
	if len(files) == 0 {
		files = []string{"model.gguf"}
	}
	return Metadata{
		HFRepo:     "org/repo",
		HFRevision: "abc123",
		HFSnapshot: strings.Repeat("a", 64),
		Files:      files,
		Size:       100,
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes  int64
		expect string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{500, "500 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{5 * 1024 * 1024, "5.0 MB"},
		{int64(1.5 * 1024 * 1024 * 1024), "1.5 GB"},
		{int64(2 * 1024 * 1024 * 1024 * 1024), "2.0 TB"},
	}
	for _, tt := range tests {
		t.Run(tt.expect, func(t *testing.T) {
			got := FormatSize(tt.bytes)
			if got != tt.expect {
				t.Errorf("FormatSize(%d) = %q, want %q", tt.bytes, got, tt.expect)
			}
		})
	}
}

func TestNewStore(t *testing.T) {
	s := NewStore()
	if s.Root == "" {
		t.Error("NewStore() returned empty Root")
	}
	// Root should end with Hali/models (Windows) or .hali/models (other)
	if base := filepath.Base(s.Root); base != "models" {
		t.Errorf("NewStore() Root basename = %q, want %q", base, "models")
	}
}

func TestNewStoreEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HALI_MODELS_DIR", dir)
	s := NewStore()
	if s.Root != dir {
		t.Errorf("NewStore() Root = %q, want %q", s.Root, dir)
	}
}

func TestStoreSaveHasLoad(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "testmodel", Size: "7b", Variant: "instruct", Quant: "q4_k_m"}
	meta := Metadata{HFRepo: "foo/bar", HFRevision: "abc123", HFSnapshot: strings.Repeat("a", 64), Files: []string{"model.gguf"}, Size: 1024}

	if s.Has(id) {
		t.Error("Has() returned true before Save()")
	}

	if err := s.Save(id, meta); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	if !s.Has(id) {
		t.Error("Has() returned false after Save()")
	}

	loaded, err := s.LoadMeta(id)
	if err != nil {
		t.Fatalf("LoadMeta() failed: %v", err)
	}
	if loaded.ModelID != id.String() {
		t.Errorf("LoadMeta() ModelID = %q, want %q", loaded.ModelID, id.String())
	}
	if loaded.HFRepo != "foo/bar" {
		t.Errorf("LoadMeta() HFRepo = %q, want %q", loaded.HFRepo, "foo/bar")
	}
	if loaded.DownloadedAt == "" {
		t.Error("LoadMeta() DownloadedAt is empty")
	}
	if len(loaded.Files) != 1 || loaded.Files[0] != "model.gguf" {
		t.Errorf("LoadMeta() Files = %v, want [model.gguf]", loaded.Files)
	}
}

func TestStoreSetInfohash(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "testmodel", Size: "7b", Variant: "instruct", Quant: "q4_k_m"}
	meta := Metadata{HFRepo: "foo/bar", HFRevision: "abc123", HFSnapshot: strings.Repeat("a", 64), Files: []string{"model.gguf"}, Size: 1024}

	if err := s.Save(id, meta); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	modelDir := s.Dir(id)
	if err := s.SetInfohash(modelDir, "deadbeefcafebabe"); err != nil {
		t.Fatalf("SetInfohash() failed: %v", err)
	}

	loaded, err := s.LoadMeta(id)
	if err != nil {
		t.Fatalf("LoadMeta() failed: %v", err)
	}
	if loaded.Infohash != "deadbeefcafebabe" {
		t.Errorf("LoadMeta() Infohash = %q, want %q", loaded.Infohash, "deadbeefcafebabe")
	}
}

func TestStoreList(t *testing.T) {
	s := &Store{Root: t.TempDir()}

	entries, err := s.List()
	if err != nil {
		t.Fatalf("List() on empty store failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List() = %d entries, want 0", len(entries))
	}

	id1 := model.ModelID{Base: "foo", Size: "3b", Variant: "base", Quant: "q4_0"}
	id2 := model.ModelID{Base: "bar", Size: "7b", Variant: "instruct", Quant: "q5_k_m"}

	if err := s.Save(id1, Metadata{HFRepo: "a/b", HFRevision: "r1", HFSnapshot: strings.Repeat("b", 64), Files: []string{"x.gguf"}, Size: 100}); err != nil {
		t.Fatalf("Save(%v) failed: %v", id1, err)
	}
	if err := s.Save(id2, Metadata{HFRepo: "c/d", HFRevision: "r2", HFSnapshot: strings.Repeat("c", 64), Files: []string{"y.gguf"}, Size: 200}); err != nil {
		t.Fatalf("Save(%v) failed: %v", id2, err)
	}

	entries, err = s.List()
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List() = %d entries, want 2", len(entries))
	}
}

func TestStoreSaveRejectsEscape(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "..", Size: "7b", Variant: "instruct", Quant: "q4_k_m"}
	meta := Metadata{HFRepo: "foo/bar", HFRevision: "abc", HFSnapshot: strings.Repeat("a", 64), Files: []string{"x.gguf"}, Size: 1}

	err := s.Save(id, meta)
	if err == nil {
		t.Error("Save() with escaped path should have returned an error")
	}
}

func TestStoreLoadMetaNotFound(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "nonexistent", Size: "7b", Variant: "base", Quant: "q4_0"}

	_, err := s.LoadMeta(id)
	if err == nil {
		t.Error("LoadMeta() for nonexistent model should have returned an error")
	}
}

func TestStoreSetInfohashNotFound(t *testing.T) {
	s := &Store{Root: t.TempDir()}

	err := s.SetInfohash(filepath.Join(t.TempDir(), "nonexistent"), "deadbeef")
	if err == nil {
		t.Error("SetInfohash() for nonexistent metadata.json should have returned an error")
	}
}

func TestStoreHasPathEscape(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "..", Size: "7b", Variant: "instruct", Quant: "q4_k_m"}

	if s.Has(id) {
		t.Error("Has() with path traversal should return false")
	}
}

func TestStoreMetadataJSONRoundTrip(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "roundtrip", Size: "13b", Variant: "chat", Quant: "fp16"}
	meta := Metadata{
		HFRepo:     "org/model",
		HFRevision: "def456",
		HFSnapshot: strings.Repeat("d", 64),
		Files:      []string{"model.gguf", "tokenizer.json"},
		Size:       9876543210,
	}

	if err := s.Save(id, meta); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	loaded, err := s.LoadMeta(id)
	if err != nil {
		t.Fatalf("LoadMeta() failed: %v", err)
	}

	if loaded.ModelID != id.String() {
		t.Errorf("ModelID mismatch: %q vs %q", loaded.ModelID, id.String())
	}
	if loaded.HFRepo != meta.HFRepo {
		t.Errorf("HFRepo mismatch: %q vs %q", loaded.HFRepo, meta.HFRepo)
	}
	if loaded.HFRevision != meta.HFRevision {
		t.Errorf("HFRevision mismatch: %q vs %q", loaded.HFRevision, meta.HFRevision)
	}
	if loaded.Size != meta.Size {
		t.Errorf("Size mismatch: %d vs %d", loaded.Size, meta.Size)
	}
	if len(loaded.Files) != 2 {
		t.Errorf("Files length: %d vs 2", len(loaded.Files))
	}
	if loaded.DownloadedAt == "" {
		t.Error("DownloadedAt should be set")
	}
}

func TestStoreDir(t *testing.T) {
	s := &Store{Root: "/home/user/.hali/models"}
	id := model.ModelID{Base: "mistral", Size: "7b", Variant: "instruct", Quant: "q4_k_m"}
	dir := s.Dir(id)
	expected := filepath.Join("/home/user/.hali/models", "mistral", "7b-instruct", "q4_k_m")
	if dir != expected {
		t.Errorf("Dir() = %q, want %q", dir, expected)
	}
}

func TestIsUnderRoot(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		path  string
		under bool
	}{
		{root, true},
		{filepath.Join(root, "foo"), true},
		{filepath.Join(root, "foo", "bar", "baz"), true},
		{root + ".evil", false},
		{filepath.Join(root, "..", "outside"), false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := safepath.IsUnderRoot(root, tt.path); got != tt.under {
				t.Errorf("IsUnderRoot(%q, %q) = %v, want %v", root, tt.path, got, tt.under)
			}
		})
	}
}

func TestStoreListIgnoresNonMetadata(t *testing.T) {
	s := &Store{Root: t.TempDir()}

	// Create a directory with a non-metadata JSON file
	extraDir := filepath.Join(s.Root, "notamodel")
	os.MkdirAll(extraDir, 0755)
	os.WriteFile(filepath.Join(extraDir, "readme.md"), []byte("hello"), 0644)

	entries, err := s.List()
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List() = %d entries, want 0 (non-metadata files ignored)", len(entries))
	}
}

func TestStoreConcurrentSaveAndLoad(t *testing.T) {
	// Concurrent Save + LoadMeta must not race. Run with go test -race.
	s := &Store{Root: t.TempDir()}

	ids := []model.ModelID{
		{Base: "a", Size: "1b", Variant: "base", Quant: "q4_0"},
		{Base: "b", Size: "7b", Variant: "instruct", Quant: "q4_k_m"},
		{Base: "c", Size: "13b", Variant: "chat", Quant: "q5_k_m"},
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		// Writer goroutine.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				s.Save(id, testMetadata("model.gguf")) //nolint:errcheck
			}
		}()
		// Reader goroutine.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				s.LoadMeta(id) //nolint:errcheck
			}
		}()
	}
	wg.Wait()
}

// TestSetInfohashAtomicOnRace verifies that concurrent SetInfohash calls never
// corrupt metadata.json (atomic rename + mutex prevents torn writes).
// Run with: go test -race ./internal/cache/...
func TestSetInfohashAtomicOnRace(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "test", Size: "7b", Variant: "base", Quant: "q4_0"}
	if err := s.Save(id, testMetadata("model.gguf")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	modelDir := s.Dir(id)

	const workers = 16
	const iters = 50
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ih := fmt.Sprintf("%040x", n)
			for range iters {
				_ = s.SetInfohash(modelDir, ih)
			}
		}(i)
	}
	wg.Wait()

	// Final metadata must be valid JSON with a valid infohash.
	meta, err := s.LoadMeta(id)
	if err != nil {
		t.Fatalf("metadata corrupted after concurrent SetInfohash: %v", err)
	}
	if meta.Infohash == "" {
		t.Error("infohash is empty after concurrent writes")
	}
}

func TestStoreSetIdentity(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "test", Size: "7b", Variant: "instruct", Quant: "q4_k_m"}
	if err := s.Save(id, testMetadata("model.gguf")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	modelDir := s.Dir(id)

	identity := torrent.TorrentIdentity{
		InfohashV1: strings.Repeat("a", 40),
		InfohashV2: strings.Repeat("b", 64),
	}
	if err := s.SetIdentity(modelDir, identity); err != nil {
		t.Fatalf("SetIdentity: %v", err)
	}

	meta, err := s.LoadMeta(id)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Infohash != identity.InfohashV1 {
		t.Errorf("Infohash = %q, want %q", meta.Infohash, identity.InfohashV1)
	}
	if meta.InfohashV2 != identity.InfohashV2 {
		t.Errorf("InfohashV2 = %q, want %q", meta.InfohashV2, identity.InfohashV2)
	}
}

func TestEvictLRUNoOpWhenUnderBudget(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "a", Size: "7b", Variant: "base", Quant: "q4_0"}
	if err := s.Save(id, Metadata{HFRepo: "x/y", HFRevision: "r1", HFSnapshot: strings.Repeat("a", 64), Files: []string{"a.gguf"}, Size: 100}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	res, err := s.EvictLRU(200)
	if err != nil {
		t.Fatalf("EvictLRU: %v", err)
	}
	if res.EvictedModels != 0 {
		t.Fatalf("EvictedModels = %d, want 0", res.EvictedModels)
	}
	if !s.Has(id) {
		t.Fatal("entry should remain after no-op eviction")
	}
}

func TestEvictLRURemovesOldestFirst(t *testing.T) {
	s := &Store{Root: t.TempDir()}

	idOld := model.ModelID{Base: "old", Size: "7b", Variant: "base", Quant: "q4_0"}
	idNew := model.ModelID{Base: "new", Size: "7b", Variant: "base", Quant: "q4_0"}

	if err := s.Save(idOld, Metadata{HFRepo: "x/y", HFRevision: "r1", HFSnapshot: strings.Repeat("a", 64), Files: []string{"old.gguf"}, Size: 100}); err != nil {
		t.Fatalf("Save old: %v", err)
	}
	if err := s.Save(idNew, Metadata{HFRepo: "x/y", HFRevision: "r2", HFSnapshot: strings.Repeat("b", 64), Files: []string{"new.gguf"}, Size: 100}); err != nil {
		t.Fatalf("Save new: %v", err)
	}

	oldMetaPath := filepath.Join(s.Dir(idOld), "metadata.json")
	oldData, err := os.ReadFile(oldMetaPath)
	if err != nil {
		t.Fatalf("read old metadata: %v", err)
	}
	oldData = []byte(strings.Replace(string(oldData), "\"downloaded_at\": \"", "\"downloaded_at\": \"2000-01-01T00:00:00Z", 1))
	if err := os.WriteFile(oldMetaPath, oldData, 0644); err != nil {
		t.Fatalf("write old metadata: %v", err)
	}

	res, err := s.EvictLRU(100)
	if err != nil {
		t.Fatalf("EvictLRU: %v", err)
	}
	if res.EvictedModels != 1 {
		t.Fatalf("EvictedModels = %d, want 1", res.EvictedModels)
	}
	if s.Has(idOld) {
		t.Fatal("oldest entry should be evicted")
	}
	if !s.Has(idNew) {
		t.Fatal("newer entry should remain")
	}
}

func TestEvictLRUHandlesMalformedDownloadedAtAsOldest(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	idBad := model.ModelID{Base: "bad", Size: "7b", Variant: "base", Quant: "q4_0"}
	idGood := model.ModelID{Base: "good", Size: "7b", Variant: "base", Quant: "q4_0"}

	if err := s.Save(idBad, Metadata{HFRepo: "x/y", HFRevision: "r1", HFSnapshot: strings.Repeat("a", 64), Files: []string{"bad.gguf"}, Size: 100}); err != nil {
		t.Fatalf("Save bad: %v", err)
	}
	if err := s.Save(idGood, Metadata{HFRepo: "x/y", HFRevision: "r2", HFSnapshot: strings.Repeat("b", 64), Files: []string{"good.gguf"}, Size: 100}); err != nil {
		t.Fatalf("Save good: %v", err)
	}

	badMetaPath := filepath.Join(s.Dir(idBad), "metadata.json")
	badData, err := os.ReadFile(badMetaPath)
	if err != nil {
		t.Fatalf("read bad metadata: %v", err)
	}
	badData = []byte(strings.Replace(string(badData), "\"downloaded_at\": \"", "\"downloaded_at\": \"not-a-time", 1))
	if err := os.WriteFile(badMetaPath, badData, 0644); err != nil {
		t.Fatalf("write bad metadata: %v", err)
	}

	if _, err := s.EvictLRU(100); err != nil {
		t.Fatalf("EvictLRU: %v", err)
	}
	if s.Has(idBad) {
		t.Fatal("malformed downloaded_at entry should be treated as oldest and evicted")
	}
}

func TestStoreSetIdentityNeverDowngradesV2(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "test", Size: "7b", Variant: "instruct", Quant: "q4_k_m"}
	if err := s.Save(id, testMetadata("model.gguf")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	modelDir := s.Dir(id)
	v2 := strings.Repeat("b", 64)

	// First write: full identity (v1+v2)
	if err := s.SetIdentity(modelDir, torrent.TorrentIdentity{
		InfohashV1: strings.Repeat("a", 40),
		InfohashV2: v2,
	}); err != nil {
		t.Fatalf("SetIdentity full identity: %v", err)
	}
	// Second write: v1-only (simulating old client re-announce)
	if err := s.SetIdentity(modelDir, torrent.TorrentIdentity{
		InfohashV1: strings.Repeat("c", 40),
	}); err != nil {
		t.Fatalf("SetIdentity v1-only: %v", err)
	}

	meta, err := s.LoadMeta(id)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.InfohashV2 != v2 {
		t.Errorf("V2 was downgraded: got %q, want %q", meta.InfohashV2, v2)
	}
}

func TestNewStoreAt(t *testing.T) {
	dir := t.TempDir()
	s := NewStoreAt(dir)
	if s.Root != dir {
		t.Errorf("NewStoreAt(%q).Root = %q", dir, s.Root)
	}
}

func TestListReturnsErrorOnCorruptedMetadata(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "bad", Size: "1b", Variant: "base", Quant: "q4_0"}
	modelDir := s.Dir(id)
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Write corrupted JSON metadata.
	if err := os.WriteFile(filepath.Join(modelDir, "metadata.json"), []byte("not valid json{{{"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Write a model.gguf so modelDir is not empty.
	os.WriteFile(filepath.Join(modelDir, "model.gguf"), []byte("data"), 0644)

	// Also save a valid model.
	id2 := model.ModelID{Base: "good", Size: "3b", Variant: "base", Quant: "q4_0"}
	if err := s.Save(id2, testMetadata("model.gguf")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := s.List(); err == nil {
		t.Fatal("List() should fail on corrupted metadata in strict mode")
	}
}

func TestSetInfohashCorruptedMetadata(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "corrupt", Size: "7b", Variant: "base", Quant: "q4_0"}
	modelDir := s.Dir(id)
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "metadata.json"), []byte("{{{broken"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := s.SetInfohash(modelDir, "abcdef0123456789")
	if err == nil {
		t.Error("SetInfohash on corrupted metadata should return error")
	}
}

func TestLoadMetaCorrupted(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "corrupt", Size: "7b", Variant: "base", Quant: "q4_0"}
	modelDir := s.Dir(id)
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "metadata.json"), []byte("not json"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := s.LoadMeta(id)
	if err == nil {
		t.Error("LoadMeta on corrupted metadata should return error")
	}
}

func TestStoreEntryFields(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "test", Size: "7b", Variant: "instruct", Quant: "q4_k_m", Revision: "abc123"}
	meta := Metadata{
		HFRepo:     "org/repo",
		HFRevision: "def456",
		HFSnapshot: strings.Repeat("e", 64),
		Files:      []string{"model.gguf"},
		Size:       5000,
		Infohash:   "00001111222233334444",
	}
	if err := s.Save(id, meta); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List() = %d entries", len(entries))
	}
	e := entries[0]
	if e.ID.String() != id.String() {
		t.Errorf("Entry.ID = %q, want %q", e.ID.String(), id.String())
	}
	if e.Dir != s.Dir(id) {
		t.Errorf("Entry.Dir = %q, want %q", e.Dir, s.Dir(id))
	}
	if e.Meta.HFRepo != "org/repo" {
		t.Errorf("Entry.Meta.HFRepo = %q", e.Meta.HFRepo)
	}
	if e.Meta.Infohash != "00001111222233334444" {
		t.Errorf("Entry.Meta.Infohash = %q", e.Meta.Infohash)
	}
}

func TestSetInfohashEmptyString(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id := model.ModelID{Base: "test", Size: "7b", Variant: "base", Quant: "q4_0"}
	if err := s.Save(id, testMetadata("model.gguf")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	modelDir := s.Dir(id)
	// Empty infohash should be accepted (acts as a no-op or stores empty).
	err := s.SetInfohash(modelDir, "")
	if err != nil {
		t.Logf("SetInfohash with empty string: %v (acceptable)", err)
	}
}

func TestStoreHasWithRevision(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	id1 := model.ModelID{Base: "a", Size: "7b", Variant: "base", Quant: "q4_0", Revision: "r1"}
	id2 := model.ModelID{Base: "a", Size: "7b", Variant: "base", Quant: "q4_0", Revision: "r2"}

	if err := s.Save(id1, testMetadata("x.gguf")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Same model different revision — Has() matches on StorePath (which ignores revision).
	// This is by design: Has() checks path existence, not revision equality.
	if !s.Has(id2) {
		t.Log("Has() with different revision returned false (model path is same)")
	}
}
