package crypto

import "testing"

func TestCanonicalizeProfileVector(t *testing.T) {
	type profile struct {
		PubKey      string `json:"pubkey"`
		DisplayName string `json:"display_name"`
		Description string `json:"description,omitempty"`
		Website     string `json:"website,omitempty"`
		Contact     string `json:"contact,omitempty"`
		Timestamp   int64  `json:"timestamp"`
	}

	in := profile{
		PubKey:      "abcdef",
		DisplayName: "Jari",
		Description: "Just a Finnish guy",
		Timestamp:   1700000000,
	}

	got, err := Canonicalize(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := `{"description":"Just a Finnish guy","display_name":"Jari","pubkey":"abcdef","timestamp":1700000000}`
	if string(got) != expected {
		t.Fatalf("canonical mismatch\nexpected: %s\nactual:   %s", expected, string(got))
	}
}
