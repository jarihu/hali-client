package torrent

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// pieceHashesViaFileRead builds piece hashes the way BuildFromFilePath does,
// using a small controllable piece size for unit testing.
func pieceHashesViaFileRead(t *testing.T, path string, pieceSize int64) []byte {
	t.Helper()
	info := metainfo.Info{PieceLength: pieceSize}
	if err := info.BuildFromFilePath(path); err != nil {
		t.Fatalf("BuildFromFilePath: %v", err)
	}
	return info.Pieces
}

// streamPieceHashes runs PieceHasher over data in-memory (simulates MultiWriter).
func streamPieceHashes(t *testing.T, data []byte, pieceSize int64) []byte {
	t.Helper()
	ph := NewPieceHasher(pieceSize)
	if _, err := ph.Write(data); err != nil {
		t.Fatalf("PieceHasher.Write: %v", err)
	}
	pieces, err := ph.Finalize()
	if err != nil {
		t.Fatalf("PieceHasher.Finalize: %v", err)
	}
	return pieces
}

func writeTmpFile(t *testing.T, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.dat")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	return f.Name()
}

// TestPieceHasherEmptyInput verifies an empty write produces no pieces.
func TestPieceHasherEmptyInput(t *testing.T) {
	pieces := streamPieceHashes(t, nil, 64)
	if len(pieces) != 0 {
		t.Errorf("empty input: pieces len = %d, want 0", len(pieces))
	}
}

// TestPieceHasherSinglePartialPiece verifies a file smaller than one piece
// produces exactly one SHA1 digest matching manual SHA1 of the data.
func TestPieceHasherSinglePartialPiece(t *testing.T) {
	data := []byte("hello world")
	pieces := streamPieceHashes(t, data, 1024)

	expected := sha1.Sum(data)
	if len(pieces) != 20 {
		t.Fatalf("pieces len = %d, want 20 (one piece)", len(pieces))
	}
	if !bytes.Equal(pieces, expected[:]) {
		t.Errorf("single partial piece hash mismatch")
	}
}

// TestPieceHasherExactOnePiece verifies data exactly filling one piece.
func TestPieceHasherExactOnePiece(t *testing.T) {
	const pieceSize = 64
	data := make([]byte, pieceSize)
	for i := range data {
		data[i] = byte(i)
	}
	pieces := streamPieceHashes(t, data, pieceSize)

	expected := sha1.Sum(data)
	if len(pieces) != 20 {
		t.Fatalf("pieces len = %d, want 20", len(pieces))
	}
	if !bytes.Equal(pieces, expected[:]) {
		t.Errorf("exact one piece hash mismatch")
	}
}

// TestPieceHasherMatchesBuildFromFilePath is the critical correctness test:
// PieceHasher must produce byte-identical output to BuildFromFilePath for any data.
func TestPieceHasherMatchesBuildFromFilePath(t *testing.T) {
	cases := []struct {
		name      string
		size      int
		pieceSize int64
	}{
		{"sub-piece (17 bytes)", 17, 64},
		{"exact one piece (64 bytes)", 64, 64},
		{"two full pieces (128 bytes)", 128, 64},
		{"two full + partial (150 bytes)", 150, 64},
		{"many pieces with partial (1013 bytes)", 1013, 64},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.size)
			if _, err := io.ReadFull(rand.Reader, data); err != nil {
				t.Fatalf("rand: %v", err)
			}

			path := writeTmpFile(t, data)
			want := pieceHashesViaFileRead(t, path, tc.pieceSize)
			got := streamPieceHashes(t, data, tc.pieceSize)

			if !bytes.Equal(got, want) {
				t.Errorf("piece hash mismatch for %q (size=%d pieceSize=%d)\n  got  %x\n  want %x",
					tc.name, tc.size, tc.pieceSize, got, want)
			}
		})
	}
}

// TestPieceHasherLargeRandomData stress-tests correctness against BuildFromFilePath
// with 2.5 pieces of random data.
func TestPieceHasherLargeRandomData(t *testing.T) {
	const pieceSize = int64(1 << 16) // 64 KiB — large enough to stress, small enough to be fast
	size := int(pieceSize*2 + pieceSize/2)
	data := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		t.Fatalf("rand: %v", err)
	}

	path := writeTmpFile(t, data)
	want := pieceHashesViaFileRead(t, path, pieceSize)
	got := streamPieceHashes(t, data, pieceSize)

	if !bytes.Equal(got, want) {
		t.Errorf("large random data: piece hashes do not match BuildFromFilePath")
	}
}

// TestPieceHasherSmallWritesProduceSameResult verifies that Write call granularity
// does not affect output — bytes written in 1-byte increments must hash identically
// to a single Write call.
func TestPieceHasherSmallWritesProduceSameResult(t *testing.T) {
	const pieceSize = 64
	data := make([]byte, 200)
	for i := range data {
		data[i] = byte(i * 3)
	}

	bulk := streamPieceHashes(t, data, pieceSize)

	ph := NewPieceHasher(pieceSize)
	for _, b := range data {
		if _, err := ph.Write([]byte{b}); err != nil {
			t.Fatalf("Write byte: %v", err)
		}
	}
	incremental, err := ph.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	if !bytes.Equal(bulk, incremental) {
		t.Error("small writes produced different hashes than bulk write")
	}
}

// TestPieceHasherPieceCountMatchesExpected verifies the number of pieces produced.
func TestPieceHasherPieceCountMatchesExpected(t *testing.T) {
	cases := []struct {
		dataSize   int
		pieceSize  int64
		wantPieces int
	}{
		{0, 64, 0},
		{63, 64, 1},
		{64, 64, 1},
		{65, 64, 2},
		{128, 64, 2},
		{129, 64, 3},
	}
	for _, tc := range cases {
		data := make([]byte, tc.dataSize)
		pieces := streamPieceHashes(t, data, tc.pieceSize)
		got := len(pieces) / 20
		if got != tc.wantPieces {
			t.Errorf("size=%d pieceSize=%d: got %d pieces, want %d",
				tc.dataSize, tc.pieceSize, got, tc.wantPieces)
		}
	}
}

// TestPieceHasherPieceCountMethod verifies PieceCount tracks boundaries correctly.
func TestPieceHasherPieceCountMethod(t *testing.T) {
	const pieceSize = 64
	ph := NewPieceHasher(pieceSize)

	if ph.PieceCount() != 0 {
		t.Errorf("initial PieceCount = %d, want 0", ph.PieceCount())
	}

	// Write one full piece — count increments at boundary.
	if _, err := ph.Write(make([]byte, pieceSize)); err != nil {
		t.Fatalf("Write full piece: %v", err)
	}
	if ph.PieceCount() != 1 {
		t.Errorf("after one full piece, PieceCount = %d, want 1", ph.PieceCount())
	}

	// Write partial second piece — count unchanged until Finalize.
	if _, err := ph.Write(make([]byte, 32)); err != nil {
		t.Fatalf("Write partial piece: %v", err)
	}
	if ph.PieceCount() != 1 {
		t.Errorf("after partial second piece, PieceCount = %d, want 1", ph.PieceCount())
	}

	if _, err := ph.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if ph.PieceCount() != 2 {
		t.Errorf("after Finalize, PieceCount = %d, want 2", ph.PieceCount())
	}
}

// TestPieceHasherFinalizeDoublePanics verifies the double-Finalize guard.
func TestPieceHasherFinalizeDoublePanics(t *testing.T) {
	ph := NewPieceHasher(64)
	if _, err := ph.Write([]byte("test")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := ph.Finalize(); err != nil {
		t.Fatalf("first Finalize: %v", err)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Error("second Finalize should panic")
		}
	}()
	ph.Finalize() //nolint:errcheck
}

// TestPieceHasherWriteAfterFinalizePanics verifies Write is rejected post-Finalize.
func TestPieceHasherWriteAfterFinalizePanics(t *testing.T) {
	ph := NewPieceHasher(64)
	if _, err := ph.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Error("Write after Finalize should panic")
		}
	}()
	ph.Write([]byte("test")) //nolint:errcheck
}

// TestNewPieceHasherZeroPieceSizePanics verifies invalid construction is caught early.
func TestNewPieceHasherZeroPieceSizePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewPieceHasher(0) should panic")
		}
	}()
	NewPieceHasher(0)
}

// TestNewPieceHasherNegativePieceSizePanics verifies negative piece size is rejected.
func TestNewPieceHasherNegativePieceSizePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewPieceHasher(-1) should panic")
		}
	}()
	NewPieceHasher(-1)
}

// TestSeedFromPieceHashesMatchesSeed verifies that seeding with precomputed piece
// hashes produces an identical infohash to the standard Seed path.
func TestSeedFromPieceHashesMatchesSeed(t *testing.T) {
	const pieceSize = int64(1 << 16) // 64 KiB
	content := make([]byte, int(pieceSize)*2+1000)
	if _, err := io.ReadFull(rand.Reader, content); err != nil {
		t.Fatalf("rand: %v", err)
	}

	env := newEnv(t)
	const modelID1 = "hashtest:1b:base:q4_0"
	const modelID2 = "hashtest:1b:base:q4_1"

	// Standard path: Seed (BuildFromFilePath).
	dir1 := env.modelDir("seed-standard")
	writeModelFile(t, dir1, content)
	ih1, err := env.engine.Seed(dir1, "model.gguf", modelID1, "", "")
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// Streaming path: SeedFromPieceHashes with precomputed hashes.
	// Piece size must match what buildHybridSingleFileInfo uses (choosePieceSize).
	dir2 := env.modelDir("seed-streaming")
	writeModelFile(t, dir2, content)
	ph := NewPieceHasher(choosePieceSize(int64(len(content))))
	if _, err := ph.Write(content); err != nil {
		t.Fatalf("PieceHasher.Write: %v", err)
	}
	pieces, err := ph.Finalize()
	if err != nil {
		t.Fatalf("PieceHasher.Finalize: %v", err)
	}
	ih2, err := env.engine.SeedFromPieceHashes(dir2, "model.gguf", modelID2, "", "", pieces, int64(len(content)))
	if err != nil {
		t.Fatalf("SeedFromPieceHashes: %v", err)
	}

	if ih1 != ih2 {
		t.Errorf("infohash mismatch: Seed=%s SeedFromPieceHashes=%s — streaming path breaks swarm compatibility", ih1, ih2)
	}
}

// TestPieceHasherWithProductionPieceSize sanity-checks that LanPieceLen (16 MiB)
// can be used without error (no buffer overflow, no panic).
func TestPieceHasherWithProductionPieceSize(t *testing.T) {
	ph := NewPieceHasher(LanPieceLen)
	if ph == nil {
		t.Fatal("NewPieceHasher returned nil")
	}
	n, err := ph.Write([]byte{0x42})
	if n != 1 || err != nil {
		t.Errorf("Write: n=%d err=%v, want n=1 err=nil", n, err)
	}
	pieces, err := ph.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if len(pieces) != 20 {
		t.Errorf("partial final piece: pieces len = %d, want 20", len(pieces))
	}
}

// TestSeedFromPieceHashesTorrentFileWritten verifies that a .torrent file is
// written to the torrent directory, just as the standard Seed path does.
func TestSeedFromPieceHashesTorrentFileWritten(t *testing.T) {
	content := []byte("torrent file write test content")
	env := newEnv(t)
	dir := env.modelDir("torrent-written")
	writeModelFile(t, dir, content)

	ph := NewPieceHasher(LanPieceLen)
	if _, err := ph.Write(content); err != nil {
		t.Fatalf("PieceHasher.Write: %v", err)
	}
	pieces, err := ph.Finalize()
	if err != nil {
		t.Fatalf("PieceHasher.Finalize: %v", err)
	}

	ih, err := env.engine.SeedFromPieceHashes(dir, "model.gguf", "torrentfile:1b:base:q4_0", "", "", pieces, int64(len(content)))
	if err != nil {
		t.Fatalf("SeedFromPieceHashes: %v", err)
	}

	torrentPath := filepath.Join(env.torrentDir, ih+".torrent")
	if _, err := os.Stat(torrentPath); os.IsNotExist(err) {
		t.Errorf(".torrent file not written to %s", torrentPath)
	}
}
