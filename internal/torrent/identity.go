package torrent

import "errors"

// TorrentIdentity carries v1 and/or v2 infohashes for one torrent.
// Both fields are optional. At least one must be non-empty for a usable identity.
// V2 (SHA256, 64 hex chars) is preferred when present; V1 (SHA1, 40 hex chars) is the fallback.
type TorrentIdentity struct {
	InfohashV1 string // 40-char hex SHA1, optional
	InfohashV2 string // 64-char hex SHA256, optional
}

// ErrNoIdentity is returned when both V1 and V2 fields are empty.
var ErrNoIdentity = errors.New("torrent identity: neither v1 nor v2 infohash present")

// GetCanonicalInfohash returns the best available infohash string.
// V2 is preferred; V1 is the fallback. Returns ErrNoIdentity if both are empty.
func GetCanonicalInfohash(id TorrentIdentity) (string, error) {
	if id.InfohashV2 != "" {
		return id.InfohashV2, nil
	}
	if id.InfohashV1 != "" {
		return id.InfohashV1, nil
	}
	return "", ErrNoIdentity
}

// Merge returns an identity that is at least as complete as both inputs.
// Never downgrades: if existing has V2, it is always preserved.
// If incoming has V2 and existing does not, it is adopted.
// V1 is preserved from whichever side has it; existing V1 is never replaced.
func Merge(existing, incoming TorrentIdentity) TorrentIdentity {
	result := existing
	if result.InfohashV2 == "" && incoming.InfohashV2 != "" {
		result.InfohashV2 = incoming.InfohashV2
	}
	if result.InfohashV1 == "" && incoming.InfohashV1 != "" {
		result.InfohashV1 = incoming.InfohashV1
	}
	return result
}

// IdentityFromV1 constructs a v1-only identity from a 40-char hex string.
func IdentityFromV1(ih string) TorrentIdentity {
	return TorrentIdentity{InfohashV1: ih}
}
