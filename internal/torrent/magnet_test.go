package torrent

import (
	"bytes"
	"encoding/hex"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func TestBuildMagnetMatchesCanonicalHashInfoBytes(t *testing.T) {
	info := &metainfo.Info{
		PieceLength: lanPieceLen,
		Pieces:      bytes.Repeat([]byte{0x01}, 20),
		Name:        "model.gguf",
		Length:      12,
	}

	m, err := BuildMagnet(info, nil)
	if err != nil {
		t.Fatalf("BuildMagnet: %v", err)
	}

	u, err := url.Parse(m)
	if err != nil {
		t.Fatalf("parse magnet: %v", err)
	}
	if u.Scheme != "magnet" {
		t.Fatalf("scheme = %q, want magnet", u.Scheme)
	}

	xt := u.Query().Get("xt")
	if xt == "" {
		t.Fatal("missing xt")
	}
	const prefix = "urn:btih:"
	if !strings.HasPrefix(xt, prefix) {
		t.Fatalf("xt = %q, missing prefix %q", xt, prefix)
	}

	infoBytes, err := bencode.Marshal(*info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	wantHash := (&metainfo.MetaInfo{InfoBytes: infoBytes}).HashInfoBytes().HexString()
	gotHash := strings.TrimPrefix(xt, prefix)
	if gotHash != wantHash {
		t.Fatalf("btih = %q, want %q", gotHash, wantHash)
	}
}

func TestBuildMagnetDeterministicOrderingAndNormalization(t *testing.T) {
	ih, _ := hex.DecodeString("00112233445566778899aabbccddeeff00112233")
	m, err := buildMagnetFromInfoHash(
		ih,
		"",
		"My Model.gguf",
		[]string{" udp://tracker.two:6969/announce ", "", "udp://tracker.one:1337/announce", "udp://tracker.two:6969/announce"},
		[]string{" https://seed.example/model.gguf ", "https://seed.example/model.gguf", "https://seed2.example/model.gguf"},
	)
	if err != nil {
		t.Fatalf("buildMagnetFromInfoHash: %v", err)
	}

	parts := strings.Split(m, "&")
	if len(parts) < 4 {
		t.Fatalf("unexpected magnet format: %s", m)
	}
	if parts[0] != "magnet:?xt=urn:btih:00112233445566778899aabbccddeeff00112233" {
		t.Fatalf("unexpected xt part: %s", parts[0])
	}
	if parts[1] != "dn=My+Model.gguf" {
		t.Fatalf("unexpected dn part: %s", parts[1])
	}

	// tr entries before ws entries, preserving first-seen order after normalization.
	want := []string{
		"tr=udp%3A%2F%2Ftracker.two%3A6969%2Fannounce",
		"tr=udp%3A%2F%2Ftracker.one%3A1337%2Fannounce",
		"ws=https%3A%2F%2Fseed.example%2Fmodel.gguf",
		"ws=https%3A%2F%2Fseed2.example%2Fmodel.gguf",
	}
	if !reflect.DeepEqual(parts[2:], want) {
		t.Fatalf("tail = %#v, want %#v", parts[2:], want)
	}
}

func TestBuildMagnetOmitTrackersWhenEmpty(t *testing.T) {
	info := &metainfo.Info{
		PieceLength: lanPieceLen,
		Pieces:      bytes.Repeat([]byte{0x01}, 20),
		Name:        "plain.gguf",
		Length:      7,
	}
	m, err := BuildMagnet(info, []string{"", "   "})
	if err != nil {
		t.Fatalf("BuildMagnet: %v", err)
	}
	u, err := url.Parse(m)
	if err != nil {
		t.Fatalf("parse magnet: %v", err)
	}
	if got := u.Query()["tr"]; len(got) != 0 {
		t.Fatalf("unexpected trackers: %#v", got)
	}
}

func TestBuildMagnetRejectsNilInfo(t *testing.T) {
	if _, err := BuildMagnet(nil, nil); err == nil {
		t.Fatal("expected error for nil info")
	}
}

func TestMagnetFromInfoHashInvalidLen(t *testing.T) {
	if got := MagnetFromInfoHash([]byte{1, 2, 3}, "x", nil); got != "" {
		t.Fatalf("got %q, want empty string", got)
	}
}

func TestBuildMagnetFromInfoHashIncludesV2WhenProvided(t *testing.T) {
	ih, _ := hex.DecodeString("00112233445566778899aabbccddeeff00112233")
	v2 := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	m, err := buildMagnetFromInfoHash(ih, v2, "model.gguf", nil, nil)
	if err != nil {
		t.Fatalf("buildMagnetFromInfoHash: %v", err)
	}
	if !strings.Contains(m, "xt=urn:btih:00112233445566778899aabbccddeeff00112233") {
		t.Fatalf("missing btih xt: %s", m)
	}
	if !strings.Contains(m, "xt=urn:btmh:1220"+v2) {
		t.Fatalf("missing btmh xt: %s", m)
	}
}
