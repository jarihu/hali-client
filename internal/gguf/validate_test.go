package gguf

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// writeHeader writes a 24-byte GGUF header to a temp file and returns its path.
func writeHeader(t *testing.T, magic string, version uint32, tensorCount, metaKVCount uint64) string {
	t.Helper()
	buf := make([]byte, headerSize)
	copy(buf[:4], magic)
	binary.LittleEndian.PutUint32(buf[4:8], version)
	binary.LittleEndian.PutUint64(buf[8:16], tensorCount)
	binary.LittleEndian.PutUint64(buf[16:24], metaKVCount)

	path := filepath.Join(t.TempDir(), "test.gguf")
	if err := os.WriteFile(path, buf, 0644); err != nil {
		t.Fatalf("writeHeader: %v", err)
	}
	return path
}

func TestValidGGUFHeaderAccepted(t *testing.T) {
	path := writeHeader(t, "GGUF", 3, 128, 42)
	if err := ValidateHeader(path); err != nil {
		t.Errorf("ValidateHeader on valid file: %v", err)
	}
}

func TestValidGGUFVersion1Accepted(t *testing.T) {
	path := writeHeader(t, "GGUF", 1, 1, 1)
	if err := ValidateHeader(path); err != nil {
		t.Errorf("ValidateHeader version 1: %v", err)
	}
}

func TestInvalidGGUFMagicRejected(t *testing.T) {
	path := writeHeader(t, "FAKE", 3, 1, 1)
	if err := ValidateHeader(path); err == nil {
		t.Error("ValidateHeader should reject invalid magic")
	}
}

func TestShortGGUFHeaderRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.gguf")
	if err := os.WriteFile(path, []byte("GGUF"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := ValidateHeader(path); err == nil {
		t.Error("ValidateHeader should reject file shorter than 24 bytes")
	}
}

func TestEmptyFileRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.gguf")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := ValidateHeader(path); err == nil {
		t.Error("ValidateHeader should reject empty file")
	}
}

func TestInvalidVersionRejected(t *testing.T) {
	tests := []uint32{0, 4, 100, math.MaxUint32}
	for _, v := range tests {
		path := writeHeader(t, "GGUF", v, 1, 1)
		if err := ValidateHeader(path); err == nil {
			t.Errorf("ValidateHeader should reject version %d", v)
		}
	}
}

func TestInvalidTensorCountRejected(t *testing.T) {
	path := writeHeader(t, "GGUF", 3, math.MaxUint64, 1)
	if err := ValidateHeader(path); err == nil {
		t.Error("ValidateHeader should reject absurd tensor_count")
	}
}

func TestInvalidMetadataCountRejected(t *testing.T) {
	path := writeHeader(t, "GGUF", 3, 1, math.MaxUint64)
	if err := ValidateHeader(path); err == nil {
		t.Error("ValidateHeader should reject absurd metadata_kv_count")
	}
}

func TestMissingFileRejected(t *testing.T) {
	if err := ValidateHeader("/nonexistent/path/model.gguf"); err == nil {
		t.Error("ValidateHeader should return error for missing file")
	}
}
