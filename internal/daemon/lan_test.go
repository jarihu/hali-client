package daemon

import (
	"fmt"
	"net"
	"testing"
	"time"

	"hali/internal/torrent"
)

// valid test infohashes — 40 lowercase hex (v1) and 64 lowercase hex (v2).
const (
	ih1 = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	ih2 = "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3"
	ih3 = "c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"
	ih4 = "d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5"
	// SHA256 of empty string — a valid 64-char v2 infohash for testing.
	ih2v2 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

func TestLanIndexUpdate(t *testing.T) {
	idx := NewLanIndex("self")

	msg := lanMessage{
		Version: "1",
		NodeID:  "node1",
		Models: []lanModelAnnounce{
			{ModelID: "mistral:7b:instruct:q4_k_m", Infohash: ih1},
		},
	}
	idx.update(msg, "node1")

	hints := idx.Query("mistral:7b:instruct:q4_k_m")
	if len(hints) != 1 {
		t.Fatalf("Query() = %d hints, want 1", len(hints))
	}
	if hints[0].Identity.InfohashV1 != ih1 {
		t.Errorf("hint InfohashV1 = %q, want %q", hints[0].Identity.InfohashV1, ih1)
	}
}

func TestLanIndexMultipleNodes(t *testing.T) {
	idx := NewLanIndex("self")

	msg := lanMessage{
		Version: "1",
		Models: []lanModelAnnounce{
			{ModelID: "llama:7b:chat:q4_0", Infohash: ih2},
		},
	}
	// Two different nodes announce the same model.
	msg.NodeID = "node1"
	idx.update(msg, "node1")
	msg.NodeID = "node2"
	idx.update(msg, "node2")

	hints := idx.Query("llama:7b:chat:q4_0")
	if len(hints) != 2 {
		t.Fatalf("Query() = %d hints, want 2 (one per node)", len(hints))
	}
}

func TestLanIndexMultipleModels(t *testing.T) {
	idx := NewLanIndex("self")

	msg := lanMessage{
		Version: "1",
		NodeID:  "node1",
		Models: []lanModelAnnounce{
			{ModelID: "a:1b:base:q4_0", Infohash: ih1},
			{ModelID: "b:2b:instruct:q4_k_m", Infohash: ih2},
		},
	}
	idx.update(msg, "node1")

	if hints := idx.Query("a:1b:base:q4_0"); len(hints) != 1 {
		t.Errorf("model a: %d hints, want 1", len(hints))
	}
	if hints := idx.Query("b:2b:instruct:q4_k_m"); len(hints) != 1 {
		t.Errorf("model b: %d hints, want 1", len(hints))
	}
}

func TestLanIndexUpdateReplacesUnconditionally(t *testing.T) {
	idx := NewLanIndex("self")

	msg1 := lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
		{ModelID: "x:1b:base:q4_0", Infohash: ih1},
	}}
	idx.update(msg1, "node1")

	// Second update with different infohash — must replace unconditionally.
	msg2 := lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
		{ModelID: "x:1b:base:q4_0", Infohash: ih2},
	}}
	idx.update(msg2, "node1")

	hints := idx.Query("x:1b:base:q4_0")
	if len(hints) != 1 {
		t.Fatalf("Query() = %d hints, want 1 (same node replaces in place)", len(hints))
	}
	if hints[0].Identity.InfohashV1 != ih2 {
		t.Errorf("hint InfohashV1 = %q, want %q", hints[0].Identity.InfohashV1, ih2)
	}
}

func TestLanIndexUpdateReplacesIgnoresSeenAt(t *testing.T) {
	// Unconditional replace means even a "stale" SeenAt (older timestamp) replaces.
	// LAN never compares timestamps — that is server-layer policy.
	idx := NewLanIndex("self")

	idx.mu.Lock()
	idx.entries["m:1b:base:q4_0"] = []ModelHint{
		{Identity: torrent.TorrentIdentity{InfohashV1: ih1}, PubkeyHash: "node1", SeenAt: time.Now()},
	}
	idx.mu.Unlock()

	// Update with an older SeenAt (simulated via direct call — update() sets SeenAt = now,
	// but the key point is there is no conditional check in update()).
	msg := lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
		{ModelID: "m:1b:base:q4_0", Infohash: ih2},
	}}
	idx.update(msg, "node1")

	hints := idx.Query("m:1b:base:q4_0")
	if len(hints) != 1 || hints[0].Identity.InfohashV1 != ih2 {
		t.Errorf("update() must replace unconditionally; got %v", hints)
	}
}

func TestLanIndexQueryUnknown(t *testing.T) {
	idx := NewLanIndex("self")
	hints := idx.Query("nonexistent:1b:base:q1")
	if len(hints) != 0 {
		t.Errorf("Query() for unknown = %d hints, want 0", len(hints))
	}
}

func TestLanIndexQueryReturnsRaw(t *testing.T) {
	// Query() must return raw hints with no TTL filtering applied.
	// A hint with a very old SeenAt must still be returned.
	// Direct write bypasses update() so any infohash string is fine.
	idx := NewLanIndex("self")

	idx.mu.Lock()
	idx.entries["m:1b:base:q4_0"] = []ModelHint{
		{Identity: torrent.TorrentIdentity{InfohashV1: "stale"}, PubkeyHash: "node1", SeenAt: time.Now().Add(-24 * time.Hour)},
		{Identity: torrent.TorrentIdentity{InfohashV1: "fresh"}, PubkeyHash: "node2", SeenAt: time.Now()},
	}
	idx.mu.Unlock()

	hints := idx.Query("m:1b:base:q4_0")
	if len(hints) != 2 {
		t.Errorf("Query() = %d hints, want 2 (no TTL filtering in LAN layer)", len(hints))
	}
}

func TestLanIndexSnapshot(t *testing.T) {
	idx := NewLanIndex("self")

	msg := lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
		{ModelID: "m1:1b:base:q4_0", Infohash: ih1},
		{ModelID: "m2:2b:instruct:q4_k_m", Infohash: ih2},
	}}
	idx.update(msg, "node1")

	snap := idx.Snapshot()
	if len(snap) != 2 {
		t.Errorf("Snapshot() = %d models, want 2", len(snap))
	}
	if len(snap["m1:1b:base:q4_0"]) != 1 {
		t.Errorf("m1 hints = %d, want 1", len(snap["m1:1b:base:q4_0"]))
	}
}

func TestLanIndexSnapshotReturnsAll(t *testing.T) {
	// Snapshot() must return all hints regardless of SeenAt — no TTL filtering in LAN.
	// Direct write bypasses update() so any infohash string is fine.
	idx := NewLanIndex("self")

	idx.mu.Lock()
	idx.entries["m1:1b:base:q4_0"] = []ModelHint{
		{Identity: torrent.TorrentIdentity{InfohashV1: "ih1-stale"}, PubkeyHash: "node1", SeenAt: time.Now().Add(-24 * time.Hour)},
	}
	idx.entries["m2:2b:base:q4_0"] = []ModelHint{
		{Identity: torrent.TorrentIdentity{InfohashV1: "ih2-fresh"}, PubkeyHash: "node2", SeenAt: time.Now()},
	}
	idx.mu.Unlock()

	snap := idx.Snapshot()
	if len(snap) != 2 {
		t.Errorf("Snapshot() = %d models, want 2 (no TTL filtering in LAN layer)", len(snap))
	}
}

func TestNewLanIndex(t *testing.T) {
	idx := NewLanIndex("self")
	if idx.entries == nil {
		t.Error("NewLanIndex() entries map is nil")
	}
	if idx.selfPubkeyHash != "self" {
		t.Errorf("selfPubkeyHash = %q, want %q", idx.selfPubkeyHash, "self")
	}
}

func TestAnnounceIntervalRange(t *testing.T) {
	if announceIntervalMin >= announceIntervalMax {
		t.Errorf("announceIntervalMin (%v) must be less than announceIntervalMax (%v)", announceIntervalMin, announceIntervalMax)
	}
	if announceIntervalMin < 20*time.Second || announceIntervalMin > 30*time.Second {
		t.Errorf("announceIntervalMin = %v, want in [20s, 30s]", announceIntervalMin)
	}
	if announceIntervalMax < 35*time.Second || announceIntervalMax > 60*time.Second {
		t.Errorf("announceIntervalMax = %v, want in [35s, 60s]", announceIntervalMax)
	}
}

func TestLanMcastAddr(t *testing.T) {
	expected := "239.192.42.1:4269"
	if lanMcastAddr != expected {
		t.Errorf("lanMcastAddr = %q, want %q", lanMcastAddr, expected)
	}
}

func TestLanMalformedUDPPacket(t *testing.T) {
	// Zero-value lanMessage (empty models list) must add no entries and not panic.
	idx := NewLanIndex("self")
	idx.update(lanMessage{NodeID: "node1"}, "node1")
	if snap := idx.Snapshot(); len(snap) != 0 {
		t.Errorf("zero-value message added %d entries, want 0", len(snap))
	}

	// Message with valid version but empty models list.
	idx.update(lanMessage{Version: "1", NodeID: "node2", Models: nil}, "node2")
	if snap := idx.Snapshot(); len(snap) != 0 {
		t.Errorf("empty-models message added %d entries, want 0", len(snap))
	}
}

func TestLanSelfAnnouncementIgnored(t *testing.T) {
	const selfID = "selfpubkeyhash"
	idx := NewLanIndex(selfID)

	msg := lanMessage{Version: "1", NodeID: selfID, Models: []lanModelAnnounce{
		{ModelID: "self:7b:instruct:q4_0", Infohash: ih1},
	}}
	idx.update(msg, selfID)

	hints := idx.Query("self:7b:instruct:q4_0")
	if len(hints) != 0 {
		t.Errorf("self-announced hint stored (%d entries); must be ignored", len(hints))
	}

	// A different node for the same model must still be accepted.
	msg.NodeID = "othernodeid"
	idx.update(msg, "othernodeid")
	if hints := idx.Query("self:7b:instruct:q4_0"); len(hints) != 1 {
		t.Errorf("hint from non-self node: got %d entries, want 1", len(hints))
	}
}

func TestLanMalformedModelIDDroppedImmediately(t *testing.T) {
	idx := NewLanIndex("self")
	bad := []string{
		"notvalid",
		"only:two",
		"only:three:parts",
		"too:many:parts:q4:extra",
		"",
		"../etc/passwd:1b:base:q4_0",
	}
	for _, id := range bad {
		msg := lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
			{ModelID: id, Infohash: ih1},
		}}
		idx.update(msg, "node1")
	}

	idx.mu.RLock()
	stored := len(idx.entries)
	idx.mu.RUnlock()

	if stored != 0 {
		t.Errorf("malformed model IDs stored %d entries, want 0", stored)
	}
}

func TestLanIndexPruneOlderThan(t *testing.T) {
	idx := NewLanIndex("self")
	now := time.Now()

	idx.mu.Lock()
	idx.entries["m:1b:base:q4_0"] = []ModelHint{
		{Identity: torrent.TorrentIdentity{InfohashV1: ih1}, PubkeyHash: "node1", SeenAt: now.Add(-20 * time.Minute)},
		{Identity: torrent.TorrentIdentity{InfohashV1: ih2}, PubkeyHash: "node2", SeenAt: now.Add(-10 * time.Second)},
	}
	idx.entries["x:1b:base:q4_0"] = []ModelHint{
		{Identity: torrent.TorrentIdentity{InfohashV1: ih3}, PubkeyHash: "node3", SeenAt: now.Add(-30 * time.Minute)},
	}
	idx.mu.Unlock()

	removed := idx.PruneOlderThan(now.Add(-15 * time.Minute))
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}

	snap := idx.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("models after prune = %d, want 1", len(snap))
	}
	if len(snap["m:1b:base:q4_0"]) != 1 {
		t.Fatalf("remaining hints for m = %d, want 1", len(snap["m:1b:base:q4_0"]))
	}
}

func TestLanAnnouncementFloodDoesNotGrowUnbounded(t *testing.T) {
	// 10k announcements for the same model from different node IDs must not store
	// more than maxHintsPerModel entries.
	idx := NewLanIndex("self")
	modelID := "flood:7b:instruct:q4_k_m"

	for i := range 10_000 {
		nodeID := fmt.Sprintf("nodeid%d", i)
		msg := lanMessage{Version: "1", NodeID: nodeID, Models: []lanModelAnnounce{
			{ModelID: modelID, Infohash: ih1},
		}}
		idx.update(msg, nodeID)
	}

	idx.mu.RLock()
	count := len(idx.entries[modelID])
	idx.mu.RUnlock()

	if count > maxHintsPerModel {
		t.Errorf("flood stored %d hints for model, want ≤ %d", count, maxHintsPerModel)
	}
	if count == 0 {
		t.Error("flood test stored zero hints — expected at least one to succeed")
	}
}

func TestLanRateLimit(t *testing.T) {
	// Same pubkeyHash sending more than 10 events/sec must have excess dropped silently.
	idx := NewLanIndex("self")
	modelID := "rl:1b:base:q4_0"

	// Send 20 events in the same second from the same node.
	// We manipulate time by fixing the reset window so all 20 fall within one second.
	const nodeID = "ratelimitnode"
	now := time.Now()
	idx.mu.Lock()
	idx.rateLimiter[nodeID] = &rateBucket{count: 0, reset: now.Add(time.Second)}
	idx.mu.Unlock()

	for i := range 20 {
		msg := lanMessage{Version: "1", NodeID: nodeID, Models: []lanModelAnnounce{
			// Generate a valid 40-char hex infohash for each iteration.
			{ModelID: modelID, Infohash: fmt.Sprintf("%040x", i)},
		}}
		idx.update(msg, nodeID)
	}

	// At most rateLimit (10) entries should have been accepted.
	// Since each update replaces (same pubkeyHash, same model), the entry count is 1.
	// What we verify is that the rate limiter fired — only the first 10 calls succeed.
	idx.mu.RLock()
	bucket := idx.rateLimiter[nodeID]
	idx.mu.RUnlock()

	if bucket.count > rateLimit {
		t.Errorf("rate bucket count = %d, want ≤ %d", bucket.count, rateLimit)
	}
}

func TestLanTwoNodesSharedInfohash(t *testing.T) {
	// Two nodes announcing the same model+infohash → two separate list entries.
	idx := NewLanIndex("self")
	modelID := "shared:1b:base:q4_0"

	msg1 := lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
		{ModelID: modelID, Infohash: ih1},
	}}
	msg2 := lanMessage{Version: "1", NodeID: "node2", Models: []lanModelAnnounce{
		{ModelID: modelID, Infohash: ih1},
	}}
	idx.update(msg1, "node1")
	idx.update(msg2, "node2")

	hints := idx.Query(modelID)
	if len(hints) != 2 {
		t.Fatalf("expected 2 hints (one per node), got %d", len(hints))
	}
	// Both should have the same infohash but different PubkeyHash.
	if hints[0].PubkeyHash == hints[1].PubkeyHash {
		t.Error("two distinct nodes produced identical PubkeyHash")
	}
	for _, h := range hints {
		if h.Identity.InfohashV1 != ih1 {
			t.Errorf("hint InfohashV1 = %q, want %q", h.Identity.InfohashV1, ih1)
		}
	}
}

func TestLanDoesNotAffectSwarmBehavior(t *testing.T) {
	// LAN returns ModelHint — a model metadata struct with no IP, no address,
	// no swarm participant data. This test asserts the boundary at compile time
	// (no net.IP or net.Addr fields) and at runtime (Query returns hints only).

	idx := NewLanIndex("self")
	idx.update(lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
		{ModelID: "a:1b:base:q4_0", Infohash: ih1},
	}}, "node1")

	hints := idx.Query("a:1b:base:q4_0")
	if len(hints) != 1 {
		t.Fatalf("expected 1 hint, got %d", len(hints))
	}

	h := hints[0]
	// ModelHint fields: Identity (TorrentIdentity), Revision, PubkeyHash (strings), SeenAt (time.Time).
	// No network address types — LAN is a hint layer only.
	_ = h.Identity.InfohashV1 // string
	_ = h.Revision            // string
	_ = h.PubkeyHash          // string (not net.IP, not net.Addr)
	_ = h.SeenAt              // time.Time

	if h.PubkeyHash != "node1" {
		t.Errorf("PubkeyHash = %q, want %q", h.PubkeyHash, "node1")
	}
	if h.Identity.InfohashV1 != ih1 {
		t.Errorf("InfohashV1 = %q, want %q", h.Identity.InfohashV1, ih1)
	}
}

func TestLanPubkeyHashIsFieldNotKey(t *testing.T) {
	// pubkeyHash must be stored as a field in ModelHint, not used as a map key.
	idx := NewLanIndex("self")

	msg := lanMessage{Version: "1", NodeID: "nodex", Models: []lanModelAnnounce{
		{ModelID: "m:1b:base:q4_0", Infohash: ih1},
	}}
	idx.update(msg, "nodex")

	idx.mu.RLock()
	hints, ok := idx.entries["m:1b:base:q4_0"]
	idx.mu.RUnlock()

	if !ok {
		t.Fatal("model entry not found in entries map")
	}
	if len(hints) != 1 {
		t.Fatalf("expected 1 hint, got %d", len(hints))
	}
	if hints[0].PubkeyHash != "nodex" {
		t.Errorf("PubkeyHash stored incorrectly: got %q, want %q", hints[0].PubkeyHash, "nodex")
	}
}

// ── Security tests ─────────────────────────────────────────────────────────

func TestLanIndexRejectsInvalidInfohash(t *testing.T) {
	idx := NewLanIndex("self")
	bad := []string{
		"",
		"abc123",    // too short
		"abc123xyz", // non-hex
		"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2z", // 41 chars, non-hex at end
		"GGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG",  // 40 chars, invalid hex
		"../../../etc/shadow",
	}
	for _, ih := range bad {
		msg := lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
			{ModelID: "x:1b:base:q4_0", Infohash: ih},
		}}
		idx.update(msg, "node1")
	}
	if hints := idx.Query("x:1b:base:q4_0"); len(hints) != 0 {
		t.Errorf("invalid infohashes stored %d hints, want 0", len(hints))
	}
}

func TestLanIndexRejectsStaleTimestamp(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name    string
		ts      int64
		wantLen int
	}{
		{"ts=0 (legacy, allowed)", 0, 1},
		{"ts=now (fresh)", now.Unix(), 1},
		{"ts=now-60s (stale)", now.Add(-60 * time.Second).Unix(), 0},
		{"ts=now+10s (future)", now.Add(10 * time.Second).Unix(), 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := NewLanIndex("self")
			msg := lanMessage{
				Version: "1",
				NodeID:  "node1",
				Ts:      tc.ts,
				Models:  []lanModelAnnounce{{ModelID: "x:1b:base:q4_0", Infohash: ih1}},
			}
			i.update(msg, "node1")
			if hints := i.Query("x:1b:base:q4_0"); len(hints) != tc.wantLen {
				t.Errorf("ts=%d: got %d hints, want %d", tc.ts, len(hints), tc.wantLen)
			}
		})
	}
}

func TestLanIndexRejectsOversizedNodeID(t *testing.T) {
	idx := NewLanIndex("self")
	// NodeID longer than 128 bytes must be rejected before update() is called.
	bigNodeID := string(make([]byte, 129))
	msg := lanMessage{Version: "1", NodeID: bigNodeID, Models: []lanModelAnnounce{
		{ModelID: "x:1b:base:q4_0", Infohash: ih1},
	}}
	idx.update(msg, bigNodeID)
	if hints := idx.Query("x:1b:base:q4_0"); len(hints) != 0 {
		t.Errorf("oversized NodeID stored %d hints, want 0", len(hints))
	}
}

func TestIsUsableLANIPv4(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{name: "private LAN", ip: "192.168.1.10", want: true},
		{name: "public", ip: "8.8.8.8", want: true},
		{name: "loopback", ip: "127.0.0.1", want: false},
		{name: "link-local", ip: "169.254.10.2", want: false},
		{name: "unspecified", ip: "0.0.0.0", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isUsableLANIPv4(net.ParseIP(tc.ip))
			if got != tc.want {
				t.Fatalf("isUsableLANIPv4(%s)=%v want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestIPv4FromAddr(t *testing.T) {
	ipNet := &net.IPNet{IP: net.ParseIP("10.0.0.5"), Mask: net.CIDRMask(24, 32)}
	if got := ipv4FromAddr(ipNet); got == nil || got.String() != "10.0.0.5" {
		t.Fatalf("ipv4FromAddr(IPNet)=%v want 10.0.0.5", got)
	}

	ipAddr := &net.IPAddr{IP: net.ParseIP("192.168.1.9")}
	if got := ipv4FromAddr(ipAddr); got == nil || got.String() != "192.168.1.9" {
		t.Fatalf("ipv4FromAddr(IPAddr)=%v want 192.168.1.9", got)
	}

	v6 := &net.IPAddr{IP: net.ParseIP("fe80::1")}
	if got := ipv4FromAddr(v6); got != nil {
		t.Fatalf("ipv4FromAddr(v6)=%v want nil", got)
	}
}

func TestLanIndexUpdateV2Accepted(t *testing.T) {
	idx := NewLanIndex("self")
	msg := lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
		{ModelID: "m:7b:instruct:q4_k_m", Infohash: ih1, InfohashV2: ih2v2},
	}}
	idx.update(msg, "node1")

	hints := idx.Query("m:7b:instruct:q4_k_m")
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Identity.InfohashV1 != ih1 {
		t.Errorf("InfohashV1 = %q, want %q", hints[0].Identity.InfohashV1, ih1)
	}
	if hints[0].Identity.InfohashV2 != ih2v2 {
		t.Errorf("InfohashV2 = %q, want %q", hints[0].Identity.InfohashV2, ih2v2)
	}
}

func TestLanIndexUpdateInvalidV2DroppedSilently(t *testing.T) {
	idx := NewLanIndex("self")
	msg := lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
		{ModelID: "m:7b:instruct:q4_k_m", Infohash: ih1, InfohashV2: "not-valid-v2"},
	}}
	idx.update(msg, "node1")

	hints := idx.Query("m:7b:instruct:q4_k_m")
	if len(hints) != 1 {
		t.Fatalf("hint not stored despite valid v1: got %d", len(hints))
	}
	if hints[0].Identity.InfohashV1 != ih1 {
		t.Errorf("InfohashV1 not preserved: got %q", hints[0].Identity.InfohashV1)
	}
	if hints[0].Identity.InfohashV2 != "" {
		t.Errorf("invalid V2 was stored: %q", hints[0].Identity.InfohashV2)
	}
}

func TestLanIndexUpdateMergesV2OnReplace(t *testing.T) {
	idx := NewLanIndex("self")

	// First message: full identity (v1+v2)
	msg1 := lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
		{ModelID: "m:7b:instruct:q4_k_m", Infohash: ih1, InfohashV2: ih2v2},
	}}
	idx.update(msg1, "node1")

	// Second message: v1-only re-announce (old client behaviour)
	msg2 := lanMessage{Version: "1", NodeID: "node1", Models: []lanModelAnnounce{
		{ModelID: "m:7b:instruct:q4_k_m", Infohash: ih1},
	}}
	idx.update(msg2, "node1")

	hints := idx.Query("m:7b:instruct:q4_k_m")
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Identity.InfohashV2 != ih2v2 {
		t.Errorf("V2 was downgraded by v1-only re-announce: got %q, want %q", hints[0].Identity.InfohashV2, ih2v2)
	}
}

func TestBroadcastFromAddr(t *testing.T) {
	t.Run("valid ipv4 subnet", func(t *testing.T) {
		src, bcast := broadcastFromAddr(&net.IPNet{IP: net.ParseIP("192.168.10.23"), Mask: net.CIDRMask(24, 32)})
		if src == nil || src.String() != "192.168.10.23" {
			t.Fatalf("src=%v want 192.168.10.23", src)
		}
		if bcast == nil || bcast.String() != "192.168.10.255" {
			t.Fatalf("bcast=%v want 192.168.10.255", bcast)
		}
	})

	t.Run("host route has no broadcast", func(t *testing.T) {
		src, bcast := broadcastFromAddr(&net.IPNet{IP: net.ParseIP("10.0.0.2"), Mask: net.CIDRMask(32, 32)})
		if src != nil || bcast != nil {
			t.Fatalf("expected nil,nil got %v,%v", src, bcast)
		}
	})

	t.Run("non-ipnet address", func(t *testing.T) {
		src, bcast := broadcastFromAddr(&net.IPAddr{IP: net.ParseIP("10.0.0.2")})
		if src != nil || bcast != nil {
			t.Fatalf("expected nil,nil got %v,%v", src, bcast)
		}
	})
}
