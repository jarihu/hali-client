package torrent

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"hali/internal/safepath"
)

var errInvalidInfoHash = errors.New("invalid infohash")

// BuildMagnet builds a BitTorrent v1 magnet URI from finalized in-memory info.
// It uses canonical metainfo helpers for infohash derivation.
func BuildMagnet(info *metainfo.Info, trackers []string) (string, error) {
	return buildMagnet(info, trackers, nil)
}

func buildMagnet(info *metainfo.Info, trackers, webseeds []string) (string, error) {
	if info == nil {
		return "", errors.New("nil metainfo info")
	}

	infoBytes, err := bencode.Marshal(*info)
	if err != nil {
		return "", fmt.Errorf("encoding info: %w", err)
	}
	if len(infoBytes) == 0 {
		return "", errors.New("empty info bytes")
	}

	mi := &metainfo.MetaInfo{InfoBytes: infoBytes}
	ih := mi.HashInfoBytes()

	return buildMagnetFromInfoHash(ih[:], "", info.Name, trackers, webseeds)
}

// MagnetFromInfoHash renders a BitTorrent v1 magnet URI from an existing
// infohash. Returns an empty string if infoHash is invalid.
func MagnetFromInfoHash(infoHash []byte, name string, trackers []string) string {
	m, err := buildMagnetFromInfoHash(infoHash, "", name, trackers, nil)
	if err != nil {
		return ""
	}
	return m
}

func buildMagnetFromInfoHash(infoHash []byte, infoHashV2, name string, trackers, webseeds []string) (string, error) {
	if len(infoHash) != 20 {
		return "", errInvalidInfoHash
	}

	v1xt := "urn:btih:" + hex.EncodeToString(infoHash)
	v2xt := ""
	v2 := strings.TrimSpace(infoHashV2)
	if v2 != "" {
		if !safepath.IsValidInfohashV2(v2) {
			return "", errInvalidInfoHash
		}
		v2xt = "urn:btmh:1220" + strings.ToLower(v2)
	}
	normTrackers := normalizeMagnetValues(trackers)
	normWebseeds := normalizeMagnetValues(webseeds)

	var builder strings.Builder
	builder.WriteString("magnet:?")
	builder.WriteString("xt=")
	builder.WriteString(v1xt)
	if v2xt != "" {
		builder.WriteString("&xt=")
		builder.WriteString(v2xt)
	}

	if strings.TrimSpace(name) != "" {
		builder.WriteString("&dn=")
		builder.WriteString(url.QueryEscape(strings.TrimSpace(name)))
	}

	for _, tracker := range normTrackers {
		builder.WriteString("&tr=")
		builder.WriteString(url.QueryEscape(tracker))
	}

	for _, webseed := range normWebseeds {
		builder.WriteString("&ws=")
		builder.WriteString(url.QueryEscape(webseed))
	}

	return builder.String(), nil
}

func normalizeMagnetValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
