package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreateLanSecretCreatesAndLoads(t *testing.T) {
	t.Setenv("PROGRAMDATA", "")
	home := t.TempDir()
	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = old }()

	first, err := LoadOrCreateLanSecret()
	if err != nil {
		t.Fatalf("first LoadOrCreateLanSecret: %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("first secret len = %d, want 32", len(first))
	}

	path := LanSecretPath()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("secret path stat: %v", err)
	}

	second, err := LoadOrCreateLanSecret()
	if err != nil {
		t.Fatalf("second LoadOrCreateLanSecret: %v", err)
	}
	if len(second) != 32 {
		t.Fatalf("second secret len = %d, want 32", len(second))
	}
	if string(first) != string(second) {
		t.Fatal("loaded secret does not match created secret")
	}
}

func TestLoadOrCreateLanSecretRejectsInvalidHex(t *testing.T) {
	t.Setenv("PROGRAMDATA", "")
	home := t.TempDir()
	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = old }()

	path := LanSecretPath()
	if err := os.MkdirAll(DataDir(), 0755); err != nil {
		t.Fatalf("mkdir datadir: %v", err)
	}
	if err := os.WriteFile(path, []byte("not-hex"), 0600); err != nil {
		t.Fatalf("write invalid secret: %v", err)
	}

	if _, err := LoadOrCreateLanSecret(); err == nil {
		t.Fatal("expected invalid hex error, got nil")
	}
}

func TestDecodeLANHMACSecretRejectsInvalid(t *testing.T) {
	if _, err := DecodeLANHMACSecret("not-hex"); err == nil {
		t.Fatal("expected invalid decode error")
	}
	if _, err := DecodeLANHMACSecret(strings.Repeat("aa", 31)); err == nil {
		t.Fatal("expected invalid size error")
	}
}

func TestResolveLANHMACConfigDisabled(t *testing.T) {
	enabled := false
	cfg := File{LANHMACEnabled: &enabled}
	on, secret, err := ResolveLANHMACConfig(cfg)
	if err != nil {
		t.Fatalf("ResolveLANHMACConfig: %v", err)
	}
	if on {
		t.Fatal("enabled = true, want false")
	}
	if secret != nil {
		t.Fatal("secret should be nil when lan_hmac is disabled")
	}
}

func TestResolveLANHMACConfigUsesConfiguredSecret(t *testing.T) {
	secretHex := strings.Repeat("ab", 32)
	enabled := true
	on, secret, err := ResolveLANHMACConfig(File{LANHMACEnabled: &enabled, LANHMACSharedSecret: secretHex})
	if err != nil {
		t.Fatalf("ResolveLANHMACConfig: %v", err)
	}
	if !on {
		t.Fatal("enabled = false, want true")
	}
	if len(secret) != 32 {
		t.Fatalf("secret len = %d, want 32", len(secret))
	}
}

func TestLanSecretPathUsesServiceDataDirOverride(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", "/tmp/hali-service")
	if got, want := LanSecretPath(), filepath.Join("/tmp/hali-service", "lan.secret"); got != want {
		t.Fatalf("LanSecretPath() = %q, want %q", got, want)
	}
}
