package daemon

import "testing"

func TestLANHMACValidSignatureAccepted(t *testing.T) {
	secret := []byte("testsecret12345678901234567890")
	msg := lanMessage{
		Version: "1",
		NodeID:  "node1",
		Ts:      123,
		Models: []lanModelAnnounce{
			{ModelID: "modela:1b:base:q4_0", Infohash: ih1},
		},
	}

	signed, err := signLANMessage(secret, msg)
	if err != nil {
		t.Fatalf("signLANMessage: %v", err)
	}

	if !verifyLANMessage(secret, signed) {
		t.Fatal("valid signature rejected")
	}
}

func TestLANHMACRejectsModifiedInfohash(t *testing.T) {
	secret := []byte("testsecret12345678901234567890")
	msg := lanMessage{
		Version: "1",
		NodeID:  "node1",
		Ts:      123,
		Models: []lanModelAnnounce{
			{ModelID: "modela:1b:base:q4_0", Infohash: ih1},
		},
	}

	signed, err := signLANMessage(secret, msg)
	if err != nil {
		t.Fatalf("signLANMessage: %v", err)
	}
	signed.Models[0].Infohash = ih2

	if verifyLANMessage(secret, signed) {
		t.Fatal("tampered infohash accepted")
	}
}

func TestLANHMACRejectsModifiedModelID(t *testing.T) {
	secret := []byte("testsecret12345678901234567890")
	msg := lanMessage{
		Version: "1",
		NodeID:  "node1",
		Ts:      123,
		Models: []lanModelAnnounce{
			{ModelID: "modela:1b:base:q4_0", Infohash: ih1},
		},
	}

	signed, err := signLANMessage(secret, msg)
	if err != nil {
		t.Fatalf("signLANMessage: %v", err)
	}
	signed.Models[0].ModelID = "modelb:1b:base:q4_0"

	if verifyLANMessage(secret, signed) {
		t.Fatal("tampered model ID accepted")
	}
}

func TestLANHMACRejectsModifiedTimestamp(t *testing.T) {
	secret := []byte("testsecret12345678901234567890")
	msg := lanMessage{
		Version: "1",
		NodeID:  "node1",
		Ts:      123,
		Models: []lanModelAnnounce{
			{ModelID: "modela:1b:base:q4_0", Infohash: ih1},
		},
	}

	signed, err := signLANMessage(secret, msg)
	if err != nil {
		t.Fatalf("signLANMessage: %v", err)
	}
	signed.Ts = 124

	if verifyLANMessage(secret, signed) {
		t.Fatal("tampered timestamp accepted")
	}
}

func TestLANHMACRejectsEmptySignature(t *testing.T) {
	secret := []byte("testsecret12345678901234567890")
	msg := lanMessage{
		Version: "1",
		NodeID:  "node1",
		Ts:      123,
		Models: []lanModelAnnounce{
			{ModelID: "modela:1b:base:q4_0", Infohash: ih1},
		},
	}

	if verifyLANMessage(secret, msg) {
		t.Fatal("message with empty signature accepted")
	}
}
