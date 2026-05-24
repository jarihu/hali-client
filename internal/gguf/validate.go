package gguf

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const (
	magic = "GGUF"

	// GGUF spec supports versions 1–3.
	minVersion uint32 = 1
	maxVersion uint32 = 3

	// Sanity bounds — a real model won't have millions of tensors or KV pairs.
	maxSaneTensorCount uint64 = 1_000_000
	maxSaneMetaKVCount uint64 = 1_000_000

	// Header layout: magic(4) + version(4) + tensor_count(8) + metadata_kv_count(8) = 24 bytes.
	headerSize = 24
)

// ValidateHeader reads the first 24 bytes of the GGUF file at path and verifies:
//   - magic bytes match "GGUF"
//   - version is in the supported range [1, 3]
//   - tensor_count and metadata_kv_count are within sane bounds
//
// It does not parse tensors or metadata. Callers should run this before seeding
// or exporting any model file.
func ValidateHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("gguf: open %q: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, headerSize)
	n, err := io.ReadFull(f, buf)
	if err != nil || n < headerSize {
		return fmt.Errorf("gguf: %q: file too short (got %d bytes, need %d)", path, n, headerSize)
	}

	if string(buf[:4]) != magic {
		return fmt.Errorf("gguf: %q: invalid magic %q (want %q)", path, buf[:4], magic)
	}

	version := binary.LittleEndian.Uint32(buf[4:8])
	if version < minVersion || version > maxVersion {
		return fmt.Errorf("gguf: %q: unsupported version %d (want %d–%d)", path, version, minVersion, maxVersion)
	}

	tensorCount := binary.LittleEndian.Uint64(buf[8:16])
	if tensorCount > maxSaneTensorCount {
		return fmt.Errorf("gguf: %q: tensor_count %d exceeds sanity limit %d", path, tensorCount, maxSaneTensorCount)
	}

	metaKVCount := binary.LittleEndian.Uint64(buf[16:24])
	if metaKVCount > maxSaneMetaKVCount {
		return fmt.Errorf("gguf: %q: metadata_kv_count %d exceeds sanity limit %d", path, metaKVCount, maxSaneMetaKVCount)
	}

	return nil
}
