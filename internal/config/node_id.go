package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lukechampine.com/blake3"
)

// NodeIDPath returns the daemon node identity file path used for LAN dedupe.
func NodeIDPath() string {
	return filepath.Join(ServiceDataDir(), "node.id")
}

// NodeKeyPath returns the daemon node private key file path.
func NodeKeyPath() string {
	return filepath.Join(ServiceDataDir(), "node.key")
}

func deriveNodeID(pub ed25519.PublicKey) string {
	sum := blake3.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

func loadOrCreateNodePrivateKey(path string) (ed25519.PrivateKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		raw := strings.TrimSpace(string(data))
		decoded, decErr := hex.DecodeString(raw)
		if decErr != nil {
			return nil, fmt.Errorf("invalid node key in %s", path)
		}
		if len(decoded) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("invalid node key length in %s", path)
		}
		return ed25519.PrivateKey(decoded), nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read node key %s: %w", path, err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate node key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create node key dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv)), 0600); err != nil {
		return nil, fmt.Errorf("write node key %s: %w", path, err)
	}
	return priv, nil
}

// LoadOrCreateNodeID returns a stable local node ID persisted on disk.
//
// This ID is an observability dedupe identity only. It is not an auth/trust
// identity and should not be used for authorization decisions.
func LoadOrCreateNodeID() (string, error) {
	keyPath := NodeKeyPath()
	priv, err := loadOrCreateNodePrivateKey(keyPath)
	if err != nil {
		return "", err
	}
	id := deriveNodeID(priv.Public().(ed25519.PublicKey))

	path := NodeIDPath()
	if data, err := os.ReadFile(path); err == nil {
		raw := strings.TrimSpace(string(data))
		if raw == id {
			return id, nil
		}
		decoded, decErr := hex.DecodeString(raw)
		if decErr != nil || (len(decoded) != 16 && len(decoded) != 32) {
			return "", fmt.Errorf("invalid node id in %s", path)
		}
		// Legacy/random node.id is transparently migrated to key-derived identity.
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read node id %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("create node id dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(id), 0600); err != nil {
		return "", fmt.Errorf("write node id %s: %w", path, err)
	}
	return id, nil
}

// LoadOrCreateNodePublicKeyHex returns the stable node public key as lowercase hex.
//
// This is derived from node.key and is suitable for identifying publisher/node
// ownership metadata where a 32-byte Ed25519 public key is required.
func LoadOrCreateNodePublicKeyHex() (string, error) {
	priv, err := loadOrCreateNodePrivateKey(NodeKeyPath())
	if err != nil {
		return "", err
	}
	pub := priv.Public().(ed25519.PublicKey)
	return hex.EncodeToString(pub), nil
}

// SignNodePayloadHex signs payload with the persistent node private key and
// returns a lowercase hex-encoded Ed25519 signature.
func SignNodePayloadHex(payload []byte) (string, error) {
	priv, err := loadOrCreateNodePrivateKey(NodeKeyPath())
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, payload)
	return hex.EncodeToString(sig), nil
}
