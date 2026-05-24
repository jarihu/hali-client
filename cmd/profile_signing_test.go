package cmd

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"hali/internal/config"
	"hali/internal/crypto"
	"hali/internal/profiles"
)

func TestProfileSigningContract_HashThenSign(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())

	pubHex, err := config.LoadOrCreateNodePublicKeyHex()
	if err != nil {
		t.Fatalf("LoadOrCreateNodePublicKeyHex: %v", err)
	}
	pub, err := hex.DecodeString(pubHex)
	if err != nil {
		t.Fatalf("decode pubkey: %v", err)
	}

	p := profiles.Profile{
		PubKey:      pubHex,
		DisplayName: "Jari Huttunen",
		Description: "Just a Finnish guy",
		Timestamp:   1700000000,
	}

	canonical, err := crypto.Canonicalize(p)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	digest := sha256.Sum256(canonical)

	sigHex, err := config.SignNodePayloadHex(digest[:])
	if err != nil {
		t.Fatalf("SignNodePayloadHex: %v", err)
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}

	if !ed25519.Verify(ed25519.PublicKey(pub), digest[:], sig) {
		t.Fatal("hash-then-sign verification failed")
	}

	if ed25519.Verify(ed25519.PublicKey(pub), canonical, sig) {
		t.Fatal("signature unexpectedly verifies against raw canonical bytes; expected hash-then-sign contract")
	}
}
