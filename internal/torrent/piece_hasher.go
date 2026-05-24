package torrent

import (
	"crypto/sha1"
	"hash"
)

// PieceHasher accumulates a byte stream into fixed-size pieces and computes SHA1
// per piece. It implements io.Writer for use with io.MultiWriter alongside the
// file writer during streaming download — eliminating the post-download re-read
// that BuildFromFilePath requires.
//
// One PieceHasher instance = one sequential stream only.
// Not safe for concurrent use.
// Must be driven by a single writer stream; do not share across goroutines.
//
// Memory is bounded to the current SHA1 state plus completed piece digests
// (20 bytes × numPieces). For a 70 GB file at 16 MiB pieces: ~87 KB.
type PieceHasher struct {
	pieceSize  int64
	offset     int64 // bytes consumed in the current in-progress piece
	pieceIndex int   // count of fully finalized pieces (incremented at each boundary and at Finalize)
	hasher     hash.Hash
	pieces     []byte // flat 20-bytes-per-piece SHA1 digests, appended as pieces complete
	finalized  bool
}

// NewPieceHasher creates a PieceHasher with the given piece size.
// pieceSize must match the torrent piece length used when seeding (LanPieceLen).
// Panics if pieceSize <= 0.
func NewPieceHasher(pieceSize int64) *PieceHasher {
	if pieceSize <= 0 {
		panic("PieceHasher: pieceSize must be > 0")
	}
	return &PieceHasher{
		pieceSize: pieceSize,
		hasher:    sha1.New(),
	}
}

// Write feeds p into the piece hasher. Full pieces are finalized at exact
// pieceSize boundaries; the final partial piece is held until Finalize.
//
// Returns the number of bytes consumed before any error. In practice sha1.Hash
// never errors, but errors are propagated for interface correctness.
// Panics if called after Finalize.
func (ph *PieceHasher) Write(p []byte) (int, error) {
	if ph.finalized {
		panic("PieceHasher: Write called after Finalize")
	}
	written := 0
	for len(p) > 0 {
		space := ph.pieceSize - ph.offset
		take := int64(len(p))
		if take > space {
			take = space
		}
		n, err := ph.hasher.Write(p[:take])
		ph.offset += int64(n)
		written += n
		if err != nil {
			return written, err
		}
		// When err == nil, io.Writer guarantees n == take.
		p = p[take:]
		if ph.offset == ph.pieceSize {
			ph.pieces = append(ph.pieces, ph.hasher.Sum(nil)...)
			ph.hasher.Reset()
			ph.offset = 0
			ph.pieceIndex++
		}
	}
	return written, nil
}

// Finalize flushes any partial final piece and returns the flat 20-bytes-per-piece
// SHA1 slice ready to assign to metainfo.Info.Pieces.
//
// Must be called exactly once after all bytes have been written.
// Panics on double call.
func (ph *PieceHasher) Finalize() ([]byte, error) {
	if ph.finalized {
		panic("PieceHasher: Finalize called twice")
	}
	ph.finalized = true
	if ph.offset > 0 {
		ph.pieces = append(ph.pieces, ph.hasher.Sum(nil)...)
		ph.hasher.Reset()
		ph.offset = 0
		ph.pieceIndex++
	}
	return ph.pieces, nil
}

// PieceCount returns the number of pieces finalized so far.
// Full pieces are counted at each pieceSize boundary during Write.
// The partial final piece is counted only after Finalize is called.
func (ph *PieceHasher) PieceCount() int {
	return ph.pieceIndex
}
