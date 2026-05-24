package config

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateNodeIDCreatesAndReuses(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())

	id1, err := LoadOrCreateNodeID()
	if err != nil {
		t.Fatalf("LoadOrCreateNodeID create: %v", err)
	}
	if len(id1) != 64 {
		t.Fatalf("node id length = %d, want 64", len(id1))
	}
	if _, err := hex.DecodeString(id1); err != nil {
		t.Fatalf("node id should be hex: %v", err)
	}

	id2, err := LoadOrCreateNodeID()
	if err != nil {
		t.Fatalf("LoadOrCreateNodeID reuse: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("node id changed: %q -> %q", id1, id2)
	}
	if _, err := os.Stat(NodeKeyPath()); err != nil {
		t.Fatalf("node key file missing: %v", err)
	}
}

func TestLoadOrCreateNodeIDRejectsInvalidFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", root)

	path := filepath.Join(root, "node.id")
	if err := os.WriteFile(path, []byte("not-hex"), 0600); err != nil {
		t.Fatalf("WriteFile node.id: %v", err)
	}

	if _, err := LoadOrCreateNodeID(); err == nil {
		t.Fatal("LoadOrCreateNodeID should fail on invalid existing node.id")
	}
}

func TestLoadOrCreateNodeIDMigratesLegacyNodeID(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", root)

	legacy := "00112233445566778899aabbccddeeff"
	if err := os.WriteFile(NodeIDPath(), []byte(legacy), 0600); err != nil {
		t.Fatalf("WriteFile node.id: %v", err)
	}

	id, err := LoadOrCreateNodeID()
	if err != nil {
		t.Fatalf("LoadOrCreateNodeID migrate: %v", err)
	}
	if id == legacy {
		t.Fatal("legacy node id should be migrated to key-derived identity")
	}
	if len(id) != 64 {
		t.Fatalf("migrated node id length = %d, want 64", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("migrated node id should be hex: %v", err)
	}
	if _, err := os.Stat(NodeKeyPath()); err != nil {
		t.Fatalf("node key file missing after migration: %v", err)
	}
}

func TestLoadOrCreateNodePublicKeyHexCreatesAndReuses(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())

	pub1, err := LoadOrCreateNodePublicKeyHex()
	if err != nil {
		t.Fatalf("LoadOrCreateNodePublicKeyHex create: %v", err)
	}
	if len(pub1) != 64 {
		t.Fatalf("public key length = %d, want 64", len(pub1))
	}
	if _, err := hex.DecodeString(pub1); err != nil {
		t.Fatalf("public key should be hex: %v", err)
	}

	pub2, err := LoadOrCreateNodePublicKeyHex()
	if err != nil {
		t.Fatalf("LoadOrCreateNodePublicKeyHex reuse: %v", err)
	}
	if pub2 != pub1 {
		t.Fatalf("public key changed: %q -> %q", pub1, pub2)
	}
}

func TestSignNodePayloadHex(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())

	sig, err := SignNodePayloadHex([]byte("payload"))
	if err != nil {
		t.Fatalf("SignNodePayloadHex: %v", err)
	}
	if len(sig) != 128 {
		t.Fatalf("signature length = %d, want 128", len(sig))
	}
	if _, err := hex.DecodeString(sig); err != nil {
		t.Fatalf("signature should be hex: %v", err)
	}
}
