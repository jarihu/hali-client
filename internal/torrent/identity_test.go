package torrent

import (
	"errors"
	"strings"
	"testing"
)

func TestGetCanonicalInfohashPrefersV2(t *testing.T) {
	id := TorrentIdentity{
		InfohashV1: strings.Repeat("a", 40),
		InfohashV2: strings.Repeat("b", 64),
	}
	got, err := GetCanonicalInfohash(id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != id.InfohashV2 {
		t.Errorf("expected V2 %q, got %q", id.InfohashV2, got)
	}
}

func TestGetCanonicalInfohashV1Fallback(t *testing.T) {
	id := TorrentIdentity{InfohashV1: strings.Repeat("c", 40)}
	got, err := GetCanonicalInfohash(id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != id.InfohashV1 {
		t.Errorf("expected V1 %q, got %q", id.InfohashV1, got)
	}
}

func TestGetCanonicalInfohashEmptyReturnsError(t *testing.T) {
	_, err := GetCanonicalInfohash(TorrentIdentity{})
	if !errors.Is(err, ErrNoIdentity) {
		t.Errorf("expected ErrNoIdentity, got %v", err)
	}
}

func TestMergeNeverDowngradesV2(t *testing.T) {
	existing := TorrentIdentity{
		InfohashV1: strings.Repeat("a", 40),
		InfohashV2: strings.Repeat("b", 64),
	}
	incoming := TorrentIdentity{InfohashV1: strings.Repeat("c", 40)} // v1-only
	got := Merge(existing, incoming)
	if got.InfohashV2 != existing.InfohashV2 {
		t.Errorf("V2 was downgraded: got %q, want %q", got.InfohashV2, existing.InfohashV2)
	}
	if got.InfohashV1 != existing.InfohashV1 {
		t.Errorf("V1 was replaced: got %q, want %q", got.InfohashV1, existing.InfohashV1)
	}
}

func TestMergeUpgradesV1ToHybrid(t *testing.T) {
	existing := TorrentIdentity{InfohashV1: strings.Repeat("a", 40)}
	incoming := TorrentIdentity{
		InfohashV1: strings.Repeat("a", 40),
		InfohashV2: strings.Repeat("b", 64),
	}
	got := Merge(existing, incoming)
	if got.InfohashV2 != incoming.InfohashV2 {
		t.Errorf("V2 not adopted: got %q, want %q", got.InfohashV2, incoming.InfohashV2)
	}
	if got.InfohashV1 != existing.InfohashV1 {
		t.Errorf("V1 changed: got %q, want %q", got.InfohashV1, existing.InfohashV1)
	}
}

func TestMergeEmptyExisting(t *testing.T) {
	existing := TorrentIdentity{}
	incoming := TorrentIdentity{
		InfohashV1: strings.Repeat("a", 40),
		InfohashV2: strings.Repeat("b", 64),
	}
	got := Merge(existing, incoming)
	if got != incoming {
		t.Errorf("Merge into empty: got %+v, want %+v", got, incoming)
	}
}

func TestIdentityFromV1(t *testing.T) {
	ih := strings.Repeat("d", 40)
	id := IdentityFromV1(ih)
	if id.InfohashV1 != ih {
		t.Errorf("InfohashV1 = %q, want %q", id.InfohashV1, ih)
	}
	if id.InfohashV2 != "" {
		t.Errorf("InfohashV2 should be empty, got %q", id.InfohashV2)
	}
}
