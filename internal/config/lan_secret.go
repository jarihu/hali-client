package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func LanSecretPath() string {
	if d := strings.TrimSpace(os.Getenv("HALI_SERVICE_DATA_DIR")); d != "" {
		return filepath.Join(d, "lan.secret")
	}
	return filepath.Join(DataDir(), "lan.secret")
}

func LoadOrCreateLanSecret() ([]byte, error) {
	path := LanSecretPath()
	if data, err := os.ReadFile(path); err == nil {
		raw := strings.TrimSpace(string(data))
		secret := make([]byte, hex.DecodedLen(len(raw)))
		n, err := hex.Decode(secret, []byte(raw))
		if err != nil {
			return nil, fmt.Errorf("decode lan secret %s: %w", path, err)
		}
		if n != 32 {
			return nil, fmt.Errorf("decode lan secret %s: invalid size %d", path, n)
		}
		return secret, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read lan secret %s: %w", path, err)
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate lan secret: %w", err)
	}

	encoded := make([]byte, hex.EncodedLen(len(secret)))
	hex.Encode(encoded, secret)

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create lan secret dir: %w", err)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		return nil, fmt.Errorf("write lan secret %s: %w", path, err)
	}

	return secret, nil
}

// DecodeLANHMACSecret parses a 32-byte hex-encoded LAN HMAC shared secret.
func DecodeLANHMACSecret(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("lan_hmac_shared_secret cannot be empty")
	}
	decoded := make([]byte, hex.DecodedLen(len(trimmed)))
	n, err := hex.Decode(decoded, []byte(trimmed))
	if err != nil {
		return nil, fmt.Errorf("decode lan_hmac_shared_secret: %w", err)
	}
	if n != 32 {
		return nil, fmt.Errorf("decode lan_hmac_shared_secret: invalid size %d (want 32 bytes / 64 hex chars)", n)
	}
	return decoded[:n], nil
}

// ResolveLANHMACConfig returns whether LAN HMAC is enabled and which secret to use.
// When lan_hmac_enabled is true and lan_hmac_shared_secret is empty/default,
// it falls back to the legacy lan.secret file path for backward compatibility.
func ResolveLANHMACConfig(cfg File) (bool, []byte, error) {
	if !cfg.LANHMACEnabledValue() {
		return false, nil, nil
	}
	raw := strings.TrimSpace(cfg.LANHMACSharedSecret)
	if raw == "" || strings.EqualFold(raw, "default") {
		secret, err := LoadOrCreateLanSecret()
		if err != nil {
			return false, nil, err
		}
		return true, secret, nil
	}
	secret, err := DecodeLANHMACSecret(raw)
	if err != nil {
		return false, nil, err
	}
	return true, secret, nil
}

func defaultLANHMACSecretHex() string {
	secret, err := LoadOrCreateLanSecret()
	if err != nil {
		return ""
	}
	return hex.EncodeToString(secret)
}
