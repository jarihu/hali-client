package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"hali/internal/model"
	"hali/internal/safepath"
	"hali/internal/torrent"

	"golang.org/x/net/ipv4"
)

const (
	lanMcastGroup = "239.192.42.1"
	lanMcastPort  = 4269

	// Broadcast interval: random jitter per cycle (spec §8.3).
	announceIntervalMin = 25 * time.Second
	announceIntervalMax = 40 * time.Second

	// Startup phase offset: random delay before first broadcast to prevent boot storms (spec §8.2).
	startupOffsetMax = 25 * time.Second

	// maxHintsPerModel caps per-model hint count to bound memory under multicast floods.
	maxHintsPerModel = 100

	rateLimit = 10 // max events per second per pubkeyHash
)

var lanMcastAddr = fmt.Sprintf("%s:%d", lanMcastGroup, lanMcastPort)

type lanModelAnnounce struct {
	ModelID    string `json:"id"`
	Infohash   string `json:"ih"`            // v1 required — backward compat
	InfohashV2 string `json:"ih2,omitempty"` // v2 optional — new
	HFRepo     string `json:"repo,omitempty"`
	Revision   string `json:"rev,omitempty"`
}

// lanMessage is the UDP multicast payload. Semantic-only: node identity and
// model hints. Peer connectivity is handled entirely by anacrolix/torrent LSD.
type lanMessage struct {
	Version string             `json:"v"`
	NodeID  string             `json:"nid"` // pubkeyHash of the sending node
	Ts      int64              `json:"ts"`  // Unix timestamp — replay guard (±30 s window)
	Port    int                `json:"p,omitempty"`
	Models  []lanModelAnnounce `json:"models"`
	Sig     string             `json:"sig,omitempty"`
}

// ModelHint is a received identity hint for a model from one LAN node.
// All fields are metadata — LAN never uses them for filtering, ranking, or validation.
type ModelHint struct {
	Identity   torrent.TorrentIdentity
	HFRepo     string    // stored only — never used for logic
	Revision   string    // stored only — never used for logic
	PubkeyHash string    // node identity — stored as field, NOT a map key
	PeerAddr   string    // host:port endpoint for direct peer hinting
	SeenAt     time.Time // stored for server-layer use — LAN never interprets time
}

// rateBucket is a simple per-second event counter for rate limiting.
type rateBucket struct {
	count int
	reset time.Time
}

// LanIndex is an ephemeral hint cache: model_id → []ModelHint.
//
// It is a passive append-and-replace hint buffer. It performs zero
// interpretation of time, quality, or correctness. All policy (TTL,
// selection, ranking) belongs to the server layer.
type LanIndex struct {
	mu             sync.RWMutex
	entries        map[string][]ModelHint // model_id → flat list of received hints
	selfPubkeyHash string                 // own node identity — used only for self-filter
	rateLimiter    map[string]*rateBucket // pubkeyHash → rate bucket
}

type updateResult struct {
	Stored  int
	Dropped int
	Reason  string
}

// NewLanIndex returns an empty LanIndex. selfPubkeyHash is this node's identity;
// announcements carrying it are silently ignored.
func NewLanIndex(selfPubkeyHash string) *LanIndex {
	return &LanIndex{
		entries:        make(map[string][]ModelHint),
		selfPubkeyHash: selfPubkeyHash,
		rateLimiter:    make(map[string]*rateBucket),
	}
}

func (idx *LanIndex) update(msg lanMessage, pubkeyHash string, src ...*net.UDPAddr) updateResult {
	if pubkeyHash == "" || len(pubkeyHash) > 128 {
		return updateResult{Reason: "invalid_node_id"}
	}
	now := time.Now()
	// Replay guard: reject messages with timestamps outside ±30 s window.
	// HMAC auth is enforced at receive before update(); this guard is defense-in-depth.
	if msg.Ts != 0 {
		age := now.Sub(time.Unix(msg.Ts, 0))
		if age > 30*time.Second || age < -5*time.Second {
			slog.Debug("lan update: dropping stale/future message", "age_s", age.Seconds())
			return updateResult{Reason: "stale_or_future_timestamp"}
		}
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if pubkeyHash == idx.selfPubkeyHash {
		return updateResult{Reason: "self_announcement"}
	}
	if !idx.allowRate(pubkeyHash, now) {
		return updateResult{Reason: "rate_limited"}
	}
	peerAddr := ""
	var srcAddr *net.UDPAddr
	if len(src) > 0 {
		srcAddr = src[0]
	}
	if srcAddr != nil && msg.Port > 0 && msg.Port <= 65535 {
		if ip, ok := netip.AddrFromSlice(srcAddr.IP); ok {
			peerAddr = netip.AddrPortFrom(ip.Unmap(), uint16(msg.Port)).String()
		}
	}
	res := updateResult{}
	for _, m := range msg.Models {
		if _, err := model.Parse(m.ModelID); err != nil {
			slog.Debug("lan update: dropping invalid model_id", "model_id", m.ModelID)
			res.Dropped++
			continue
		}
		if !safepath.IsValidInfohash(m.Infohash) {
			slog.Debug("lan update: dropping invalid infohash", "ih", m.Infohash)
			res.Dropped++
			continue
		}
		incomingV2 := m.InfohashV2
		if incomingV2 != "" && !safepath.IsValidInfohashV2(incomingV2) {
			slog.Debug("lan update: dropping invalid ih2", "ih2", incomingV2)
			incomingV2 = ""
		}
		incomingID := torrent.TorrentIdentity{InfohashV1: m.Infohash, InfohashV2: incomingV2}
		incomingRepo := strings.TrimSpace(m.HFRepo)
		hints := idx.entries[m.ModelID]
		replaced := false
		for i, h := range hints {
			if h.PubkeyHash == pubkeyHash {
				// V1 always replaces — a re-announce from the same node supersedes.
				// V2 never downgrades: keep existing if incoming lacks it, upgrade if present.
				updatedV2 := h.Identity.InfohashV2
				if incomingID.InfohashV2 != "" {
					updatedV2 = incomingID.InfohashV2
				}
				updatedPeerAddr := h.PeerAddr
				if peerAddr != "" {
					updatedPeerAddr = peerAddr
				}
				updatedRepo := h.HFRepo
				if incomingRepo != "" {
					updatedRepo = incomingRepo
				}
				hints[i] = ModelHint{
					Identity:   torrent.TorrentIdentity{InfohashV1: incomingID.InfohashV1, InfohashV2: updatedV2},
					HFRepo:     updatedRepo,
					Revision:   m.Revision,
					PubkeyHash: pubkeyHash,
					PeerAddr:   updatedPeerAddr,
					SeenAt:     now,
				}
				replaced = true
				break
			}
		}
		if !replaced {
			if len(hints) >= maxHintsPerModel {
				slog.Debug("lan update: hint limit reached, dropping", "model_id", m.ModelID)
				res.Dropped++
				continue
			}
			hints = append(hints, ModelHint{
				Identity:   incomingID,
				HFRepo:     incomingRepo,
				Revision:   m.Revision,
				PubkeyHash: pubkeyHash,
				PeerAddr:   peerAddr,
				SeenAt:     now,
			})
		}
		idx.entries[m.ModelID] = hints
		res.Stored++
	}
	if res.Stored == 0 {
		if res.Dropped > 0 {
			res.Reason = "all_models_invalid_or_filtered"
		} else {
			res.Reason = "no_models"
		}
	}
	return res
}

// allowRate returns true if pubkeyHash is within the 10 events/sec rate limit.
// Must be called with idx.mu held.
func (idx *LanIndex) allowRate(pubkeyHash string, now time.Time) bool {
	b := idx.rateLimiter[pubkeyHash]
	if b == nil {
		idx.rateLimiter[pubkeyHash] = &rateBucket{count: 1, reset: now.Add(time.Second)}
		return true
	}
	if now.After(b.reset) {
		b.count = 1
		b.reset = now.Add(time.Second)
		return true
	}
	if b.count >= rateLimit {
		return false
	}
	b.count++
	return true
}

// Query returns a copy of all received hints for modelID.
// No filtering is applied — caller is responsible for TTL and selection policy.
func (idx *LanIndex) Query(modelID string) []ModelHint {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	hints := idx.entries[modelID]
	if len(hints) == 0 {
		return nil
	}
	out := make([]ModelHint, len(hints))
	copy(out, hints)
	return out
}

// Snapshot returns a copy of all received hints across all models.
// No filtering is applied.
func (idx *LanIndex) Snapshot() map[string][]ModelHint {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make(map[string][]ModelHint, len(idx.entries))
	for id, hints := range idx.entries {
		cp := make([]ModelHint, len(hints))
		copy(cp, hints)
		out[id] = cp
	}
	return out
}

// PruneOlderThan removes hints older than cutoff and drops empty model buckets.
// Returns the number of removed hints.
func (idx *LanIndex) PruneOlderThan(cutoff time.Time) int {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	removed := 0
	for modelID, hints := range idx.entries {
		keep := hints[:0]
		for _, h := range hints {
			if h.SeenAt.Before(cutoff) {
				removed++
				continue
			}
			keep = append(keep, h)
		}
		if len(keep) == 0 {
			delete(idx.entries, modelID)
			continue
		}
		idx.entries[modelID] = keep
	}
	return removed
}

// Announcer sends and receives periodic LAN model announcements via UDP multicast.
type Announcer struct {
	index     *LanIndex
	nodeID    string
	secret    []byte
	hmacAuth  bool
	debug     bool
	getModels func() []lanModelAnnounce
	getPort   func() int
	stopCh    chan struct{}
}

func NewAnnouncer(index *LanIndex, nodeID string, secret []byte, hmacAuth bool, debug bool, getModels func() []lanModelAnnounce, getPort func() int) *Announcer {
	return &Announcer{
		index:     index,
		nodeID:    nodeID,
		secret:    secret,
		hmacAuth:  hmacAuth,
		debug:     debug,
		getModels: getModels,
		getPort:   getPort,
		stopCh:    make(chan struct{}),
	}
}

// Run starts the announcer and listener in background goroutines.
func (a *Announcer) Run() {
	go a.runAnnounce()
	go a.runListen()
}

func (a *Announcer) Stop() {
	close(a.stopCh)
}

func (a *Announcer) runAnnounce() {
	// Random startup phase offset (0–25s) to prevent boot storms (spec §8.2).
	startupDelay := time.Duration(rand.Int63n(int64(startupOffsetMax)))
	select {
	case <-time.After(startupDelay):
	case <-a.stopCh:
		return
	}
	a.send()
	for {
		// Jittered interval: random between min and max per cycle (spec §8.3).
		jitter := time.Duration(rand.Int63n(int64(announceIntervalMax - announceIntervalMin)))
		select {
		case <-time.After(announceIntervalMin + jitter):
			a.send()
		case <-a.stopCh:
			return
		}
	}
}

func (a *Announcer) send() {
	models := a.getModels()
	if len(models) == 0 {
		return
	}
	port := 0
	if a.getPort != nil {
		port = a.getPort()
	}
	msg := lanMessage{Version: "1", NodeID: a.nodeID, Ts: time.Now().Unix(), Port: port, Models: models}
	if a.hmacAuth {
		signed, err := signLANMessage(a.secret, msg)
		if err != nil {
			slog.Debug("lan announce: failed to sign message", "error", err)
			return
		}
		msg = signed
	}
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Debug("lan announce: failed to marshal message", "error", err)
		return
	}
	addr, err := net.ResolveUDPAddr("udp4", lanMcastAddr)
	if err != nil {
		slog.Debug("lan announce: failed to resolve multicast address", "error", err)
		return
	}

	targets := lanAnnounceTargets()
	if len(targets) == 0 {
		if err := sendLANPacketFromSource(data, addr, nil); err != nil {
			slog.Debug("lan announce: failed to send", "error", err)
		}
		return
	}

	sent := 0
	for _, ip := range targets {
		if err := sendLANPacketFromSource(data, addr, ip); err != nil {
			slog.Debug("lan announce: interface send failed", "src_ip", ip.String(), "error", err)
			continue
		}
		sent++
	}
	if sent == 0 {
		slog.Debug("lan announce: failed to send on all interfaces", "targets", len(targets))
	}

	// Directed broadcast fallback for networks that drop multicast but allow
	// local subnet broadcasts.
	broadcastTargets := lanBroadcastTargets()
	broadcastSent := 0
	for _, t := range broadcastTargets {
		dst := &net.UDPAddr{IP: t.BroadcastIP, Port: lanMcastPort}
		if err := sendLANPacketFromSource(data, dst, t.SourceIP); err != nil {
			slog.Debug("lan announce: broadcast send failed", "src_ip", t.SourceIP.String(), "dst_ip", t.BroadcastIP.String(), "error", err)
			continue
		}
		broadcastSent++
	}
	if broadcastSent == 0 && len(broadcastTargets) > 0 {
		slog.Debug("lan announce: failed to send on all broadcast targets", "targets", len(broadcastTargets))
	}
}

func sendLANPacketFromSource(data []byte, dst *net.UDPAddr, srcIP net.IP) error {
	var src *net.UDPAddr
	if srcIP != nil {
		src = &net.UDPAddr{IP: srcIP}
	}
	conn, err := net.DialUDP("udp4", src, dst)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	_, err = conn.Write(data)
	return err
}

func lanAnnounceTargets() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	out := make([]net.IP, 0, len(ifaces))
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip := ipv4FromAddr(addr)
			if ip == nil || !isUsableLANIPv4(ip) {
				continue
			}
			key := ip.String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, ip)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].String() < out[j].String()
	})
	return out
}

type lanBroadcastTarget struct {
	SourceIP    net.IP
	BroadcastIP net.IP
}

func lanBroadcastTargets() []lanBroadcastTarget {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	out := make([]lanBroadcastTarget, 0, len(ifaces))
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			srcIP, bcastIP := broadcastFromAddr(addr)
			if srcIP == nil || bcastIP == nil || !isUsableLANIPv4(srcIP) {
				continue
			}
			key := srcIP.String() + "|" + bcastIP.String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, lanBroadcastTarget{SourceIP: srcIP, BroadcastIP: bcastIP})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceIP.String() == out[j].SourceIP.String() {
			return out[i].BroadcastIP.String() < out[j].BroadcastIP.String()
		}
		return out[i].SourceIP.String() < out[j].SourceIP.String()
	})
	return out
}

func broadcastFromAddr(addr net.Addr) (net.IP, net.IP) {
	ipNet, ok := addr.(*net.IPNet)
	if !ok {
		return nil, nil
	}
	ip := ipNet.IP.To4()
	if ip == nil || len(ipNet.Mask) != net.IPv4len {
		return nil, nil
	}
	bcast := make(net.IP, net.IPv4len)
	for i := 0; i < net.IPv4len; i++ {
		bcast[i] = ip[i] | ^ipNet.Mask[i]
	}
	if bcast.Equal(ip) {
		return nil, nil
	}
	return ip, bcast
}

func ipv4FromAddr(addr net.Addr) net.IP {
	switch v := addr.(type) {
	case *net.IPNet:
		if ip4 := v.IP.To4(); ip4 != nil {
			return ip4
		}
	case *net.IPAddr:
		if ip4 := v.IP.To4(); ip4 != nil {
			return ip4
		}
	}
	return nil
}

func isUsableLANIPv4(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return false
	}
	return ip.IsGlobalUnicast()
}

func (a *Announcer) runListen() {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: lanMcastPort})
	if err != nil {
		slog.Debug("lan listen: failed to bind UDP", "error", err)
		return
	}
	defer conn.Close()

	packetConn := ipv4.NewPacketConn(conn)
	groupIP := net.ParseIP(lanMcastGroup).To4()
	if groupIP != nil {
		joined := 0
		ifaces, ifaceErr := net.Interfaces()
		if ifaceErr == nil {
			for _, iface := range ifaces {
				if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
					continue
				}
				if err := packetConn.JoinGroup(&iface, &net.UDPAddr{IP: groupIP}); err != nil {
					continue
				}
				joined++
			}
		}
		if joined == 0 {
			slog.Debug("lan listen: no multicast interfaces joined; relying on direct/broadcast UDP only")
		}
	}

	go func() {
		<-a.stopCh
		conn.Close()
	}()

	// Use max UDP payload size to avoid JSON truncation on large model lists.
	buf := make([]byte, 65535)
	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			select {
			case <-a.stopCh:
				return
			default:
				slog.Debug("lan listen: read error", "error", err)
				continue
			}
		}
		var msg lanMessage
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			a.logLANReject("invalid_json", lanMessage{}, "", err)
			continue
		}
		a.logLANSeen(msg)
		if a.hmacAuth {
			if msg.Sig == "" {
				slog.Warn("lan_announcement_hmac_mismatch",
					"reason", "missing_signature",
					"node_id", msg.NodeID,
					"version", msg.Version,
					"models", len(msg.Models),
				)
				a.logLANReject("missing_signature", msg, "", nil)
				continue
			}
			if !verifyLANMessage(a.secret, msg) {
				slog.Warn("lan_announcement_hmac_mismatch",
					"reason", "invalid_signature",
					"node_id", msg.NodeID,
					"version", msg.Version,
					"models", len(msg.Models),
				)
				a.logLANReject("invalid_signature", msg, "", nil)
				continue
			}
		}
		if msg.Version != "1" {
			a.logLANReject("unsupported_version", msg, "", nil)
			continue
		}
		if len(msg.NodeID) == 0 || len(msg.NodeID) > 128 {
			a.logLANReject("invalid_node_id", msg, "", nil)
			continue
		}
		res := a.index.update(msg, msg.NodeID, src)
		if res.Stored > 0 {
			a.logLANAccept(msg, res.Stored, res.Dropped)
			continue
		}
		a.logLANReject(res.Reason, msg, "", nil)
	}
}

func (a *Announcer) logLANSeen(msg lanMessage) {
	if !a.debug {
		return
	}
	slog.Info("lan_announcement_seen",
		"node_id", msg.NodeID,
		"version", msg.Version,
		"models", len(msg.Models),
		"hmac_required", a.hmacAuth,
	)
}

func (a *Announcer) logLANAccept(msg lanMessage, storedModels, droppedModels int) {
	if !a.debug {
		return
	}
	slog.Info("lan_announcement_accepted",
		"node_id", msg.NodeID,
		"version", msg.Version,
		"models", len(msg.Models),
		"stored_models", storedModels,
		"dropped_models", droppedModels,
	)
}

func (a *Announcer) logLANReject(reason string, msg lanMessage, nodeID string, err error) {
	if !a.debug {
		return
	}
	if nodeID == "" {
		nodeID = msg.NodeID
	}
	if err != nil {
		slog.Info("lan_announcement_rejected",
			"reason", reason,
			"node_id", nodeID,
			"version", msg.Version,
			"models", len(msg.Models),
			"error", err,
		)
		return
	}
	slog.Info("lan_announcement_rejected",
		"reason", reason,
		"node_id", nodeID,
		"version", msg.Version,
		"models", len(msg.Models),
	)
}

func signLANMessage(secret []byte, msg lanMessage) (lanMessage, error) {
	unsigned := msg
	unsigned.Sig = ""
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return lanMessage{}, err
	}
	msg.Sig = Sign(secret, payload)
	return msg, nil
}

func verifyLANMessage(secret []byte, msg lanMessage) bool {
	if msg.Sig == "" {
		return false
	}
	unsigned := msg
	unsigned.Sig = ""
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return false
	}
	return Verify(secret, payload, msg.Sig)
}
