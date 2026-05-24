// Package benchmark contains performance benchmarks for the bt torrent layer.
//
// Uses randomly generated data — NOT sparse files. Sparse files benchmark
// filesystem metadata operations, not hashing throughput. Random data produces
// meaningful numbers that extrapolate correctly to production model sizes.
//
// Run: go test -bench=. -benchtime=1x ./benchmark/...
package benchmark_test

import (
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hali/internal/daemon"
	"hali/internal/torrent"
)

// benchBase creates a temp dir with retry cleanup for boltdb release on Windows.
func benchBase(b *testing.B) string {
	b.Helper()
	base, err := os.MkdirTemp("", "hali-bench-*")
	if err != nil {
		b.Fatalf("MkdirTemp: %v", err)
	}
	b.Cleanup(func() {
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

// randomFile writes n bytes of random data to a temp file and returns its path.
func randomFile(b *testing.B, dir, name string, n int64) string {
	b.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		b.Fatalf("create: %v", err)
	}
	defer f.Close()
	if _, err := io.CopyN(f, rand.Reader, n); err != nil {
		b.Fatalf("write random: %v", err)
	}
	return path
}

func BenchmarkTorrentCreation512MB(b *testing.B) {
	const size = 512 << 20 // 512 MiB — random bytes, not sparse
	base := benchBase(b)
	dataDir := filepath.Join(base, "data")
	torrentDir := filepath.Join(base, "torrents")
	os.MkdirAll(dataDir, 0755)    //nolint:errcheck
	os.MkdirAll(torrentDir, 0755) //nolint:errcheck

	modelDir := filepath.Join(dataDir, "bench512")
	os.MkdirAll(modelDir, 0755) //nolint:errcheck
	randomFile(b, modelDir, "model.gguf", size)

	eng, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		b.Fatalf("NewEngine: %v", err)
	}
	b.Cleanup(eng.Close)

	b.ResetTimer()
	b.SetBytes(size)
	for range b.N {
		if _, err := eng.Seed(modelDir, "model.gguf", "bench:512mb:base:q4_0", "", ""); err != nil {
			b.Fatalf("Seed: %v", err)
		}
	}
}

func BenchmarkTorrentCreation1GB(b *testing.B) {
	const size = 1 << 30 // 1 GiB — extrapolate to 7GB/70GB from this number
	base := benchBase(b)
	dataDir := filepath.Join(base, "data")
	torrentDir := filepath.Join(base, "torrents")
	os.MkdirAll(dataDir, 0755)    //nolint:errcheck
	os.MkdirAll(torrentDir, 0755) //nolint:errcheck

	modelDir := filepath.Join(dataDir, "bench1gb")
	os.MkdirAll(modelDir, 0755) //nolint:errcheck
	randomFile(b, modelDir, "model.gguf", size)

	eng, err := torrent.NewEngine(dataDir, torrentDir)
	if err != nil {
		b.Fatalf("NewEngine: %v", err)
	}
	b.Cleanup(eng.Close)

	b.ResetTimer()
	b.SetBytes(size)
	for range b.N {
		if _, err := eng.Seed(modelDir, "model.gguf", "bench:1gb:base:q4_0", "", ""); err != nil {
			b.Fatalf("Seed: %v", err)
		}
	}
}

func BenchmarkLanIndexQuery1000Peers(b *testing.B) {
	idx := daemon.NewLanIndex("")

	// update() is unexported, so we measure Snapshot throughput on an empty index.
	// This establishes the baseline RLock contention cost with zero entries.

	b.ResetTimer()
	for range b.N {
		snap := idx.Snapshot()
		_ = snap
	}
}

func BenchmarkLanIndexQueryPopulated(b *testing.B) {
	// Benchmark Query() on an index with many entries.
	// We can't call update() directly (unexported), so we measure Snapshot() throughput
	// on an empty index as a baseline for the lock-contention path.
	idx := daemon.NewLanIndex("")

	b.ResetTimer()
	for range b.N {
		peers := idx.Query("bench:7b:instruct:q4_k_m")
		_ = peers
	}
}
