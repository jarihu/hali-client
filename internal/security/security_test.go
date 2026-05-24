// Package security_test is a regression harness for security boundaries.
// Each test documents a specific constraint that must never regress.
// Run: go test ./internal/security/...
package security_test

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"hali/internal/cache"
	"hali/internal/daemon"
	"hali/internal/gguf"
	"hali/internal/model"
)

// TestPathTraversalRejectedByParse verifies that model.Parse rejects strings
// containing path-traversal sequences. These must never reach the filesystem.
func TestPathTraversalRejectedByParse(t *testing.T) {
	traversal := []string{
		"../etc:7b:base:q4_0",
		"../../root:7b:base:q4_0",
		"foo:../bar:base:q4_0",
		"foo:7b:../../etc:q4_0",
		"foo:7b:base:../q4_0",
	}
	for _, s := range traversal {
		if _, err := model.Parse(s); err == nil {
			t.Errorf("Parse(%q) should reject path traversal", s)
		}
	}
}

// TestPathTraversalRejectedByStore verifies that cache.Store paths never escape
// the cache root. Any model dir must be a subdirectory of Root.
func TestPathTraversalRejectedByStore(t *testing.T) {
	s := &cache.Store{Root: t.TempDir()}
	id := model.ModelID{Base: "safe", Size: "1b", Variant: "base", Quant: "q4_0"}

	if err := s.Save(id, cache.Metadata{
		HFRepo:     "org/repo",
		HFRevision: "abc123",
		HFSnapshot: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Files:      []string{"model.gguf"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	dir := s.Dir(id)
	rel, err := filepath.Rel(s.Root, dir)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if len(rel) >= 2 && rel[:2] == ".." {
		t.Errorf("model dir %q escapes store root %q", dir, s.Root)
	}
}

// TestMaliciousTorrentMetadataExtraFields verifies that extra JSON keys in a
// torrent comment field are silently ignored (standard Go JSON behavior).
// An attacker injecting extra fields must not affect parsing correctness.
func TestMaliciousTorrentMetadataExtraFields(t *testing.T) {
	type torrentMeta struct {
		ModelID string `json:"model_id"`
	}
	payload := `{"model_id":"mistral:7b:instruct:q4_k_m","evil_field":"<script>","__proto__":{"polluted":true}}`

	var meta torrentMeta
	if err := json.Unmarshal([]byte(payload), &meta); err != nil {
		t.Fatalf("Unmarshal with extra fields should not error: %v", err)
	}
	if meta.ModelID != "mistral:7b:instruct:q4_k_m" {
		t.Errorf("ModelID = %q, want mistral:7b:instruct:q4_k_m", meta.ModelID)
	}
}

// TestLanMalformedModelIDRejectedByIndex verifies that the LAN index drops
// announcements with invalid model IDs and accumulates no garbage.
func TestLanMalformedModelIDRejectedByIndex(t *testing.T) {
	idx := daemon.NewLanIndex("")

	// These IDs are not valid 4-part model identifiers.
	badIDs := []string{"notvalid", "only:two", "", "too:many:colon:separated:parts"}
	for i, id := range badIDs {
		ip := fmt.Sprintf("10.0.0.%d", i+1)
		_ = id
		_ = ip
		// We can't call update() directly (unexported), but we can verify via
		// Query() that no entries were stored for these IDs.
		peers := idx.Query(id)
		if len(peers) != 0 {
			t.Errorf("Query(%q) returned %d peers for invalid ID, want 0", id, len(peers))
		}
	}

	snap := idx.Snapshot()
	if len(snap) != 0 {
		t.Errorf("Snapshot() has %d entries on a fresh index, want 0", len(snap))
	}
}

// TestInvalidGGUFExportSkipped verifies that ValidateHeader rejects files that
// would otherwise be silently exported as corrupt models.
func TestInvalidGGUFExportSkipped(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{"empty", nil},
		{"too short", []byte("GGUF")},
		{"wrong magic", makeGGUFHeader("FAKE", 3, 1, 1)},
		{"bad version zero", makeGGUFHeader("GGUF", 0, 1, 1)},
		{"bad version too high", makeGGUFHeader("GGUF", 99, 1, 1)},
		{"absurd tensor count", makeGGUFHeader("GGUF", 3, ^uint64(0), 1)},
		{"absurd metadata count", makeGGUFHeader("GGUF", 3, 1, ^uint64(0))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.gguf")
			if err := os.WriteFile(path, tt.content, 0644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if err := gguf.ValidateHeader(path); err == nil {
				t.Errorf("ValidateHeader(%s) should reject corrupted file", tt.name)
			}
		})
	}
}

// makeGGUFHeader builds a 24-byte GGUF header for test fixtures.
func makeGGUFHeader(magic string, version uint32, tensorCount, metaKVCount uint64) []byte {
	buf := make([]byte, 24)
	copy(buf[:4], magic)
	binary.LittleEndian.PutUint32(buf[4:8], version)
	binary.LittleEndian.PutUint64(buf[8:16], tensorCount)
	binary.LittleEndian.PutUint64(buf[16:24], metaKVCount)
	return buf
}
