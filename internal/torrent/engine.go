package torrent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gotorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/merkle"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/anacrolix/torrent/types/infohash"
	infohashv2 "github.com/anacrolix/torrent/types/infohash-v2"
	"golang.org/x/time/rate"

	"hali/internal/config"
	"hali/internal/networking"
	"hali/internal/safepath"
)

// LanPieceLen is the legacy fixed piece length (16 MiB). Retained for test
// fixtures and backward-compatibility references. New torrents use ChoosePieceSize.
const LanPieceLen = 1 << 24 // 16 MiB

// keep unexported alias so existing internal code that references it still compiles.
const lanPieceLen = LanPieceLen

// ChoosePieceSize returns the piece length for a torrent of the given file size.
// Smaller files use smaller pieces to keep per-piece overhead low; large files
// use 16 MiB pieces to cap the piece-hash list size.
// The streaming hasher in pull.go must use the same value so piece hashes match.
func ChoosePieceSize(fileBytes int64) int64 {
	return choosePieceSize(fileBytes)
}

func choosePieceSize(fileBytes int64) int64 {
	const mib = 1 << 20
	const gib = 1 << 30
	switch {
	case fileBytes < 8*gib:
		return 2 * mib
	case fileBytes < 32*gib:
		return 4 * mib
	case fileBytes < 80*gib:
		return 8 * mib
	default:
		return 16 * mib
	}
}

type SeedStatus string

const (
	StatusHashing SeedStatus = "hashing"
	StatusSeeding SeedStatus = "seeding"
	StatusError   SeedStatus = "error"
)

type TorrentEntry struct {
	ModelID   string
	Identity  TorrentIdentity
	MagnetURI string
	Status    SeedStatus
	Error     string
	Peers     string
	t         *gotorrent.Torrent
}

// DownloadJob tracks an in-progress torrent download.
type DownloadJob struct {
	ID            string
	ModelID       string
	Identity      TorrentIdentity
	MagnetURI     string
	Filename      string
	Total         int64
	Written       int64
	RateBps       int64
	ElapsedSec    int64
	ETASeconds    int64
	ActivePeers   int
	PendingPeers  int
	HalfOpenPeers int
	TotalPeers    int
	Done          bool
	Error         string
	t             *gotorrent.Torrent
	startedAt     time.Time
	lastRateAt    time.Time
	lastRateBytes int64
}

// SeedJob tracks an in-progress seed finalization request.
type SeedJob struct {
	ID        string
	ModelID   string
	Identity  TorrentIdentity
	MagnetURI string
	Done      bool
	Error     string
}

// EngineModelState is the minimal per-model transfer state exposed to metrics.
type EngineModelState struct {
	ModelID   string
	MagnetURI string
	Status    SeedStatus
	Peers     int
	Down      int64 // cumulative bytes downloaded (payload data)
	Up        int64 // cumulative bytes uploaded (payload data)
	Written   int64 // bytes written to disk - jobs only
	Total     int64 // total file size - jobs only
	SizeBytes int64 // best-known model size when metadata is available
	IsJob     bool
}

type Engine struct {
	client     *gotorrent.Client
	torrentDir string // DataDir()/torrents/ — central store for <infohash>.torrent files
	mu         sync.RWMutex
	entries    map[string]*TorrentEntry // keyed by model_id
	jobs       map[string]*DownloadJob  // keyed by job_id
	seedJobs   map[string]*SeedJob      // keyed by job_id
	uploadRL   *rate.Limiter
	downloadRL *rate.Limiter
	maxDLJobs  int
	shutdownCh chan struct{}
	dlWG       sync.WaitGroup
	closed     atomic.Bool
}

const (
	defaultMaxConcurrentDownloads = 2
	seedAddMaxRetries             = 3
	newClientMaxRetries           = 24
	newClientRetryDelay           = 100 * time.Millisecond
	defaultLimiterBurst           = 1 << 20
)

func isRetryableClientListenErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "forbidden by its access permissions") ||
		strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "only one usage of each socket address")
}

func NewEngine(dataDir, torrentDir string) (*Engine, error) {
	cfg := gotorrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.Seed = true
	cfg.NoDefaultPortForwarding = true
	cfg.ListenPort = 0 // OS-assigned; avoids port conflicts across instances
	cfg.NoDHT = true

	uploadRL := rate.NewLimiter(rate.Inf, defaultLimiterBurst)
	downloadRL := rate.NewLimiter(rate.Inf, defaultLimiterBurst)
	cfg.UploadRateLimiter = uploadRL
	cfg.DownloadRateLimiter = downloadRL

	if err := os.MkdirAll(torrentDir, 0755); err != nil {
		return nil, err
	}

	var client *gotorrent.Client
	var err error
	for attempt := 0; attempt < newClientMaxRetries; attempt++ {
		client, err = gotorrent.NewClient(cfg)
		if err == nil {
			break
		}
		if !isRetryableClientListenErr(err) || attempt == newClientMaxRetries-1 {
			return nil, err
		}
		time.Sleep(newClientRetryDelay)
	}
	return &Engine{
		client:     client,
		torrentDir: torrentDir,
		entries:    make(map[string]*TorrentEntry),
		jobs:       make(map[string]*DownloadJob),
		seedJobs:   make(map[string]*SeedJob),
		uploadRL:   uploadRL,
		downloadRL: downloadRL,
		maxDLJobs:  defaultMaxConcurrentDownloads,
		shutdownCh: make(chan struct{}),
	}, nil
}

func (e *Engine) NetworkMode() networking.Mode {
	return networking.ModeLANOnly
}

func (e *Engine) NetworkCapabilities() networking.Capabilities {
	return networking.ResolveCapabilities(networking.ModeLANOnly)
}

func (e *Engine) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = e.Shutdown(ctx)
}

// Shutdown gracefully stops active download work and closes the torrent client.
// It waits for active download goroutines until ctx is done.
func (e *Engine) Shutdown(ctx context.Context) error {
	first := e.closed.CompareAndSwap(false, true)
	if first {
		close(e.shutdownCh)
	}

	done := make(chan struct{})
	go func() {
		e.dlWG.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		e.client.Close()
		return ctx.Err()
	}

	e.client.Close()
	return nil
}

// Port returns the port the torrent client is listening on.
func (e *Engine) Port() int {
	return e.client.LocalPort()
}

// TorrentDir returns the directory where <infohash>.torrent files are stored.
func (e *Engine) TorrentDir() string {
	return e.torrentDir
}

// ApplyRateLimits sets runtime upload/download ceilings in KB/s.
// Values <= 0 mean unlimited.
func (e *Engine) ApplyRateLimits(uploadKBps, downloadKBps int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	applyRateLimit(e.uploadRL, uploadKBps)
	applyRateLimit(e.downloadRL, downloadKBps)
}

// SetMaxConcurrentDownloads sets the active download limit. Values <= 0 disable limiting.
func (e *Engine) SetMaxConcurrentDownloads(limit int) {
	e.mu.Lock()
	e.maxDLJobs = limit
	e.mu.Unlock()
}

func applyRateLimit(l *rate.Limiter, kbps int) {
	if l == nil {
		return
	}
	if kbps <= 0 {
		l.SetLimit(rate.Inf)
		return
	}
	bps := kbps * 1024
	if bps < 1 {
		bps = 1
	}
	l.SetLimit(rate.Limit(bps))
	if l.Burst() < bps {
		l.SetBurst(bps)
	}
}

func (e *Engine) activeDownloadJobsLocked() int {
	active := 0
	for _, job := range e.jobs {
		if !job.Done {
			active++
		}
	}
	return active
}

func isRetryableSeedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection") ||
		strings.Contains(msg, "tempor") ||
		strings.Contains(msg, "reset")
}

func seedErrFromPanic(r any) error {
	return fmt.Errorf("internal seed panic: %v", r)
}

// Seed creates a torrent for the model file, saves it to torrentDir as
// <infohash>.torrent, and starts seeding. Hashing is done inline and may
// take minutes for large files. On success, returns the hex infohash.
func (e *Engine) Seed(modelDir, filename, modelID, hfRepo, revision string) (string, error) {
	e.mu.Lock()
	entry := &TorrentEntry{ModelID: modelID, Status: StatusHashing}
	e.entries[modelID] = entry
	e.mu.Unlock()

	ih, err := func() (ih string, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = seedErrFromPanic(r)
				slog.Error("seed panic", "model_id", modelID, "panic", r)
			}
		}()
		return e.seedInner(modelDir, filename, modelID, hfRepo, revision, entry, nil, 0)
	}()

	e.mu.Lock()
	if err != nil {
		entry.Status = StatusError
		entry.Error = err.Error()
	} else {
		entry.Status = StatusSeeding
	}
	e.mu.Unlock()

	return ih, err
}

// SeedFromPieceHashes creates a torrent using precomputed v1 piece hashes from
// PieceHasher.Finalize(). pieces must be the flat 20-bytes-per-piece
// SHA1 slice produced by PieceHasher with LanPieceLen as the piece size.
// fileSize is the total byte count of the file.
func (e *Engine) SeedFromPieceHashes(modelDir, filename, modelID, hfRepo, revision string, pieces []byte, fileSize int64) (string, error) {
	e.mu.Lock()
	entry := &TorrentEntry{ModelID: modelID, Status: StatusSeeding}
	e.entries[modelID] = entry
	e.mu.Unlock()

	ih, err := func() (ih string, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = seedErrFromPanic(r)
				slog.Error("seed from pieces panic", "model_id", modelID, "panic", r)
			}
		}()
		return e.seedInner(modelDir, filename, modelID, hfRepo, revision, entry, pieces, fileSize)
	}()

	e.mu.Lock()
	if err != nil {
		entry.Status = StatusError
		entry.Error = err.Error()
	} else {
		entry.Status = StatusSeeding
	}
	e.mu.Unlock()

	return ih, err
}

// torrentMeta is the comment field written into every torrent.
// Field order is fixed — JSON marshaling of structs is deterministic.
// FROZEN core fields (model_id, revision, format, source): renaming breaks parsers.
type torrentMeta struct {
	ModelID  string `json:"model_id"`
	Revision string `json:"revision"`
	Format   string `json:"format"`
	Source   string `json:"source"`
}

func (e *Engine) seedInner(modelDir, filename, modelID, hfRepo, revision string, entry *TorrentEntry, pieces []byte, fileSize int64) (string, error) {
	filePath := filepath.Join(modelDir, filename)
	info, rawFileTree, pieceLayers, err := buildHybridSingleFileInfo(filePath, filename, pieces, fileSize, nil)
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", filename, err)
	}

	infoBytes, err := marshalHybridSingleFileInfo(info, rawFileTree)
	if err != nil {
		return "", fmt.Errorf("encoding info: %w", err)
	}

	var webseeds []string
	if hfRepo != "" && revision != "" {
		webseeds = []string{fmt.Sprintf(
			"https://huggingface.co/%s/resolve/%s/%s",
			hfRepo, revision, url.PathEscape(filename),
		)}
		slog.Debug("torrent webseed attached", "model_id", modelID, "url", webseeds[0])
	}

	comment, err := json.Marshal(torrentMeta{
		ModelID:  modelID,
		Revision: revision,
		Format:   "gguf",
		Source:   "huggingface",
	})
	if err != nil {
		slog.Warn("failed to marshal torrent comment, using empty comment", "model_id", modelID, "error", err)
		comment = []byte("{}")
	}

	mi := &metainfo.MetaInfo{
		InfoBytes:   infoBytes,
		Comment:     string(comment),
		CreatedBy:   "hali",
		UrlList:     webseeds,
		PieceLayers: pieceLayers,
		// CreationDate intentionally omitted for deterministic output.
	}

	ih := mi.HashInfoBytes()
	v2 := infohashv2.HashBytes(infoBytes)
	v2Hex := v2.HexString()
	magnetURI, err := buildMagnetFromInfoHash(ih[:], v2Hex, info.Name, nil, webseeds)
	if err != nil {
		return "", fmt.Errorf("building magnet: %w", err)
	}

	torrentPath := filepath.Join(e.torrentDir, ih.HexString()+".torrent")
	if err := writeTorrent(mi, torrentPath); err != nil {
		return "", err
	}

	spec := gotorrent.TorrentSpecFromMetaInfo(mi)
	spec.Storage = modelDirStorage(modelDir)

	var t *gotorrent.Torrent
	for attempt := 0; attempt < seedAddMaxRetries; attempt++ {
		t, _, err = e.client.AddTorrentSpec(spec)
		if err == nil {
			break
		}
		if !isRetryableSeedErr(err) || attempt == seedAddMaxRetries-1 {
			return "", fmt.Errorf("adding torrent: %w", err)
		}
		backoff := time.Duration(1<<attempt) * time.Second
		slog.Warn("seed add torrent retry", "model_id", modelID, "attempt", attempt+1, "backoff", backoff, "error", err)
		time.Sleep(backoff)
	}

	e.mu.Lock()
	entry.t = t
	entry.Identity = TorrentIdentity{InfohashV1: ih.HexString(), InfohashV2: v2Hex}
	entry.MagnetURI = magnetURI
	e.mu.Unlock()

	<-t.GotInfo()
	t.DownloadAll() // triggers piece verification; seeding begins once all pieces are confirmed

	return ih.HexString(), nil
}

// SeedFromTorrentFile adds a previously created torrent to the client.
// It loads the torrent file from torrentDir by infohash.
func (e *Engine) SeedFromTorrentFile(modelDir, infohashHex, modelID string, identity TorrentIdentity) (ihHex string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = seedErrFromPanic(r)
			slog.Error("seed from torrent file panic", "model_id", modelID, "panic", r)
		}
	}()

	if !safepath.IsValidInfohash(infohashHex) {
		return "", fmt.Errorf("invalid infohash %q: must be 40 hex characters", infohashHex)
	}
	torrentPath := filepath.Join(e.torrentDir, infohashHex+".torrent")
	mi, err := metainfo.LoadFromFile(torrentPath)
	if err != nil {
		return "", fmt.Errorf("loading torrent: %w", err)
	}

	ih := mi.HashInfoBytes()
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return "", fmt.Errorf("decoding torrent info: %w", err)
	}
	parsedIdentity := IdentityFromV1(ih.HexString())
	if info.HasV2() {
		v2 := infohashv2.HashBytes(mi.InfoBytes)
		parsedIdentity.InfohashV2 = v2.HexString()
	}
	entryIdentity := Merge(identity, parsedIdentity)
	magnetURI, err := buildMagnetFromInfoHash(ih[:], entryIdentity.InfohashV2, info.Name, nil, mi.UrlList)
	if err != nil {
		return "", fmt.Errorf("building magnet: %w", err)
	}

	spec := gotorrent.TorrentSpecFromMetaInfo(mi)
	spec.Storage = modelDirStorage(modelDir)

	var t *gotorrent.Torrent
	for attempt := 0; attempt < seedAddMaxRetries; attempt++ {
		t, _, err = e.client.AddTorrentSpec(spec)
		if err == nil {
			break
		}
		if !isRetryableSeedErr(err) || attempt == seedAddMaxRetries-1 {
			return "", fmt.Errorf("adding torrent: %w", err)
		}
		backoff := time.Duration(1<<attempt) * time.Second
		slog.Warn("seed-from-file add torrent retry", "model_id", modelID, "attempt", attempt+1, "backoff", backoff, "error", err)
		time.Sleep(backoff)
	}

	<-t.GotInfo()
	t.DownloadAll()

	e.mu.Lock()
	e.entries[modelID] = &TorrentEntry{
		ModelID:   modelID,
		Identity:  entryIdentity,
		MagnetURI: magnetURI,
		Status:    StatusSeeding,
		t:         t,
	}
	e.mu.Unlock()

	return ih.HexString(), nil
}

// StartSeed finalizes torrent identity asynchronously and returns a pollable job ID.
func (e *Engine) StartSeed(modelDir, filename, modelID, hfRepo, revision string, pieces []byte, fileSize int64) string {
	jobID := fmt.Sprintf("seed-%x", time.Now().UnixNano())
	job := &SeedJob{ID: jobID, ModelID: modelID}

	e.mu.Lock()
	e.seedJobs[jobID] = job
	e.mu.Unlock()

	go func() {
		var (
			infohash string
			err      error
		)

		if len(pieces) > 0 && fileSize > 0 {
			infohash, err = e.SeedFromPieceHashes(modelDir, filename, modelID, hfRepo, revision, pieces, fileSize)
		} else {
			infohash, err = e.Seed(modelDir, filename, modelID, hfRepo, revision)
		}

		e.mu.Lock()
		defer e.mu.Unlock()
		job.Done = true
		if err != nil {
			slog.Error("seed job failed", "job_id", jobID, "model_id", modelID, "error", err)
			job.Error = err.Error()
			return
		}
		if entry, ok := e.entries[modelID]; ok {
			job.Identity = entry.Identity
			job.MagnetURI = entry.MagnetURI
			return
		}
		job.Identity = IdentityFromV1(infohash)
	}()

	return jobID
}

// SeedJobStatus returns a snapshot of a seed job, or (nil, false) if not found.
func (e *Engine) SeedJobStatus(jobID string) (*SeedJob, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	job, ok := e.seedJobs[jobID]
	if !ok {
		return nil, false
	}
	cp := *job
	return &cp, true
}

// Entries returns a snapshot of all tracked torrent entries.
func (e *Engine) Entries() []TorrentEntry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]TorrentEntry, 0, len(e.entries))
	for _, v := range e.entries {
		entry := *v
		if v.t != nil {
			entry.Peers = fmt.Sprintf("%d peers", v.t.Stats().ActivePeers)
		}
		out = append(out, entry)
	}
	return out
}

// StartDownload joins a torrent swarm via magnet URI (v1 + optional v2 xt),
// then optionally injects direct peer endpoints for faster LAN bootstrap.
// Returns a job ID that can be polled with JobStatus.
func (e *Engine) StartDownload(modelDir, modelID, ihHex, ihV2 string, peerAddrs []string) (string, error) {
	e.mu.Lock()
	if e.maxDLJobs > 0 && e.activeDownloadJobsLocked() >= e.maxDLJobs {
		e.mu.Unlock()
		return "", fmt.Errorf("too many concurrent downloads (max %d)", e.maxDLJobs)
	}
	e.mu.Unlock()

	var ih infohash.T
	if err := ih.FromHexString(ihHex); err != nil {
		return "", fmt.Errorf("invalid infohash %q: %w", ihHex, err)
	}
	ihV2 = strings.TrimSpace(strings.ToLower(ihV2))
	if ihV2 != "" && !safepath.IsValidInfohashV2(ihV2) {
		return "", fmt.Errorf("invalid infohash_v2 %q: must be 64 hex characters", ihV2)
	}

	if err := os.MkdirAll(modelDir, 0755); err != nil {
		return "", err
	}

	magnetURI, err := buildMagnetFromInfoHash(ih[:], ihV2, modelID, nil, nil)
	if err != nil {
		return "", fmt.Errorf("building magnet: %w", err)
	}

	spec, err := gotorrent.TorrentSpecFromMagnetUri(magnetURI)
	if err != nil {
		return "", fmt.Errorf("parsing magnet: %w", err)
	}
	spec.Storage = modelDirStorage(modelDir)
	spec.PeerAddrs = append(spec.PeerAddrs, peerAddrs...)

	t, _, err := e.client.AddTorrentSpec(spec)
	if err != nil {
		return "", fmt.Errorf("adding torrent: %w", err)
	}

	if len(peerAddrs) > 0 {
		peers := make([]gotorrent.PeerInfo, 0, len(peerAddrs))
		for _, raw := range peerAddrs {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			ap, parseErr := netip.ParseAddrPort(raw)
			if parseErr != nil {
				continue
			}
			peers = append(peers, gotorrent.PeerInfo{
				Addr:    ap,
				Source:  gotorrent.PeerSourceDirect,
				Trusted: true,
			})
		}
		if len(peers) > 0 {
			t.AddPeers(peers)
		}
	}

	jobID := fmt.Sprintf("%x", time.Now().UnixNano())
	job := &DownloadJob{
		ID:         jobID,
		ModelID:    modelID,
		Identity:   TorrentIdentity{InfohashV1: ihHex, InfohashV2: ihV2},
		MagnetURI:  magnetURI,
		ETASeconds: 0,
		t:          t,
		startedAt:  time.Now(),
		lastRateAt: time.Now(),
	}

	e.mu.Lock()
	e.jobs[jobID] = job
	e.mu.Unlock()

	e.dlWG.Add(1)
	go func() {
		defer e.dlWG.Done()
		defer func() {
			if r := recover(); r != nil {
				e.mu.Lock()
				job.Done = true
				job.Error = fmt.Sprintf("internal panic: %v", r)
				e.mu.Unlock()
				slog.Error("download goroutine panic", "job_id", jobID, "model_id", modelID, "panic", r)
			}
		}()

		// Wait for torrent metadata from peers.
		// Keep the default timeout behavior when no peers are present, but allow
		// bounded extensions when peers are connected yet metadata is delayed.
		metaTimeout := config.DefaultTorrentMetaTimeout
		if cfg, cfgErr := config.Load(); cfgErr == nil {
			metaTimeout = cfg.TorrentMetaTimeout()
		}
		if metaTimeout <= 0 {
			metaTimeout = config.DefaultTorrentMetaTimeout
		}
		deadline := time.Now().Add(metaTimeout)
		maxDeadline := time.Now().Add(3 * metaTimeout)
		gotInfo := false
		for !gotInfo {
			if e.isDownloadCancelled(jobID) {
				return
			}
			window := time.Until(deadline)
			if window <= 0 {
				if e.isDownloadCancelled(jobID) {
					return
				}
				e.mu.Lock()
				job.Done = true
				job.Error = "timeout waiting for torrent metadata from peers"
				e.mu.Unlock()
				return
			}
			if window > 5*time.Second {
				window = 5 * time.Second
			}
			select {
			case <-t.GotInfo():
				gotInfo = true
			case <-time.After(window):
				// If there are active peers, allow additional bounded time for
				// metadata exchange; otherwise preserve the default timeout.
				if t.Stats().ActivePeers > 0 {
					extended := time.Now().Add(metaTimeout)
					if extended.After(maxDeadline) {
						extended = maxDeadline
					}
					if extended.After(deadline) {
						deadline = extended
					}
				}
				continue
			case <-e.shutdownCh:
				e.mu.Lock()
				job.Done = true
				job.Error = "engine shutdown"
				e.mu.Unlock()
				return
			}
		}

		if e.closed.Load() {
			e.mu.Lock()
			job.Done = true
			job.Error = "engine shutdown"
			e.mu.Unlock()
			return
		}

		info := t.Info()
		total := info.TotalLength()

		name := info.BestName()
		if !safepath.IsSafeFilename(name) {
			e.mu.Lock()
			job.Done = true
			job.Error = fmt.Sprintf("torrent rejected: unsafe filename %q from peer metadata", name)
			e.mu.Unlock()
			return
		}

		e.mu.Lock()
		job.Total = total
		job.Filename = name
		if magnetURI, magnetErr := buildMagnetFromInfoHash(ih[:], job.Identity.InfohashV2, job.Filename, nil, nil); magnetErr == nil {
			job.MagnetURI = magnetURI
		}
		e.mu.Unlock()

		t.DownloadAll()

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			if e.isDownloadCancelled(jobID) {
				return
			}
			if e.closed.Load() {
				e.mu.Lock()
				if !job.Done {
					job.Done = true
					job.Error = "engine shutdown"
				}
				e.mu.Unlock()
				return
			}
			select {
			case <-e.shutdownCh:
				e.mu.Lock()
				if !job.Done {
					job.Done = true
					job.Error = "engine shutdown"
				}
				e.mu.Unlock()
				return
			case <-ticker.C:
			}
			missing := t.BytesMissing()
			e.mu.Lock()
			if job.Done {
				e.mu.Unlock()
				return
			}
			job.Written = total - missing
			job.ElapsedSec = int64(time.Since(job.startedAt).Seconds())
			if job.ElapsedSec < 0 {
				job.ElapsedSec = 0
			}
			now := time.Now()
			if now.After(job.lastRateAt) {
				deltaSec := now.Sub(job.lastRateAt).Seconds()
				if deltaSec > 0 {
					deltaBytes := job.Written - job.lastRateBytes
					if deltaBytes < 0 {
						deltaBytes = 0
					}
					job.RateBps = int64(float64(deltaBytes) / deltaSec)
				}
				job.lastRateAt = now
				job.lastRateBytes = job.Written
			}
			if job.RateBps > 0 && total > job.Written {
				job.ETASeconds = (total - job.Written) / job.RateBps
			} else {
				job.ETASeconds = 0
			}
			if missing == 0 {
				job.Done = true
				job.ETASeconds = 0
				e.entries[modelID] = &TorrentEntry{
					ModelID:   modelID,
					Identity:  job.Identity,
					MagnetURI: job.MagnetURI,
					Status:    StatusSeeding,
					t:         t,
				}
				e.mu.Unlock()
				break
			}
			e.mu.Unlock()
		}
	}()

	return jobID, nil
}

// CancelDownload marks a download job as canceled and drops the underlying torrent.
// Returns false when jobID is unknown.
func (e *Engine) CancelDownload(jobID string) bool {
	e.mu.Lock()
	job, ok := e.jobs[jobID]
	if !ok {
		e.mu.Unlock()
		return false
	}
	if !job.Done {
		job.Done = true
		job.Error = "canceled by user"
	}
	t := job.t
	e.mu.Unlock()

	if t != nil {
		t.Drop()
	}
	return true
}

func (e *Engine) isDownloadCancelled(jobID string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	job, ok := e.jobs[jobID]
	if !ok {
		return false
	}
	return job.Done && job.Error == "canceled by user"
}

// JobStatus returns a snapshot of a download job, or (nil, false) if not found.
func (e *Engine) JobStatus(jobID string) (*DownloadJob, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, ok := e.jobs[jobID]
	if !ok {
		return nil, false
	}
	job.ElapsedSec = int64(time.Since(job.startedAt).Seconds())
	if job.ElapsedSec < 0 {
		job.ElapsedSec = 0
	}
	if job.RateBps > 0 && job.Total > job.Written {
		job.ETASeconds = (job.Total - job.Written) / job.RateBps
	} else if job.Done {
		job.ETASeconds = 0
	}
	cp := *job
	if job.t != nil {
		ts := job.t.Stats()
		cp.ActivePeers = ts.ActivePeers
		cp.PendingPeers = ts.PendingPeers
		cp.HalfOpenPeers = ts.HalfOpenPeers
		cp.TotalPeers = ts.TotalPeers
	}
	return &cp, true
}

// TotalBytes returns cumulative aggregate byte counters across all tracked torrents and jobs.
func (e *Engine) TotalBytes() (down int64, up int64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, entry := range e.entries {
		if entry.t == nil {
			continue
		}
		ts := entry.t.Stats()
		down += ts.BytesReadData.Int64()
		up += ts.BytesWrittenData.Int64()
	}
	for _, job := range e.jobs {
		if job.t == nil {
			continue
		}
		ts := job.t.Stats()
		down += ts.BytesReadData.Int64()
		up += ts.BytesWrittenData.Int64()
	}
	return down, up
}

// ActiveModels returns per-model transfer state used by StatsCollector.
func (e *Engine) ActiveModels() []EngineModelState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]EngineModelState, 0, len(e.entries)+len(e.jobs))
	for _, entry := range e.entries {
		state := EngineModelState{
			ModelID:   entry.ModelID,
			MagnetURI: entry.MagnetURI,
			Status:    entry.Status,
		}
		if entry.t != nil {
			ts := entry.t.Stats()
			state.Down = ts.BytesReadData.Int64()
			state.Up = ts.BytesWrittenData.Int64()
			state.Peers = ts.ActivePeers
			state.SizeBytes = entry.t.Length()
		}
		out = append(out, state)
	}
	for _, job := range e.jobs {
		state := EngineModelState{
			ModelID:   job.ModelID,
			MagnetURI: job.MagnetURI,
			Written:   job.Written,
			Total:     job.Total,
			IsJob:     true,
		}
		if job.t != nil {
			ts := job.t.Stats()
			state.Down = ts.BytesReadData.Int64()
			state.Up = ts.BytesWrittenData.Int64()
			state.Peers = ts.ActivePeers
			if state.Total <= 0 {
				state.Total = job.t.Length()
			}
		}
		state.SizeBytes = state.Total
		out = append(out, state)
	}
	return out
}

// modelDirStorage returns a file storage backend rooted at modelDir where each
// torrent file is stored at modelDir/<filename> with no extra subdirectory.
// storage.NewFile's default FilePathMaker prepends info.Name to the file path,
// producing modelDir/<name>/<name> for single-file torrents — this fixes that.
func modelDirStorage(modelDir string) storage.ClientImplCloser {
	return storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir: modelDir,
		TorrentDirMaker: func(baseDir string, _ *metainfo.Info, _ metainfo.Hash) string {
			return baseDir
		},
		FilePathMaker: func(opts storage.FilePathMakerOpts) string {
			return filepath.Join(opts.File.BestPath()...)
		},
	})
}

func buildHybridSingleFileInfo(filePath, filename string, pieces []byte, fileSize int64, private *bool) (metainfo.Info, map[string]bencode.Bytes, map[string]string, error) {
	var info metainfo.Info
	if len(pieces) > 0 {
		if fileSize <= 0 {
			fi, err := os.Stat(filePath)
			if err != nil {
				return metainfo.Info{}, nil, nil, err
			}
			fileSize = fi.Size()
		}
		// pieces were hashed at choosePieceSize(fileSize); PieceLength must match
		// so that v1 piece count and the v2 merkle tree below are consistent.
		info = metainfo.Info{
			PieceLength: choosePieceSize(fileSize),
			Pieces:      pieces,
			Name:        filepath.Base(filename),
			Length:      fileSize,
			Private:     private,
		}
	} else {
		fi, err := os.Stat(filePath)
		if err != nil {
			return metainfo.Info{}, nil, nil, err
		}
		info = metainfo.Info{PieceLength: choosePieceSize(fi.Size()), Private: private}
		if err := info.BuildFromFilePath(filePath); err != nil {
			return metainfo.Info{}, nil, nil, err
		}
	}

	pl := info.PieceLength   // shorthand; v2 merkle must use the same piece size as v1
	plInt := int(info.PieceLength) // SumMinLength takes int

	file, err := os.Open(filePath)
	if err != nil {
		return metainfo.Info{}, nil, nil, err
	}
	defer file.Close()

	fileHasher := merkle.NewHash()
	pieceHasher := merkle.NewHash()
	pieceLayerHashes := make([][32]byte, 0, max(1, int((info.Length+pl-1)/pl)))
	remainingInPiece := pl
	buf := make([]byte, 1<<20)

	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			if _, err := fileHasher.Write(buf[:n]); err != nil {
				return metainfo.Info{}, nil, nil, err
			}

			offset := 0
			for offset < n {
				chunk := int64(n - offset)
				if chunk > remainingInPiece {
					chunk = remainingInPiece
				}
				if _, err := pieceHasher.Write(buf[offset : offset+int(chunk)]); err != nil {
					return metainfo.Info{}, nil, nil, err
				}
				offset += int(chunk)
				remainingInPiece -= chunk
				if remainingInPiece == 0 {
					var pieceHash [32]byte
					copy(pieceHash[:], pieceHasher.SumMinLength(nil, plInt))
					pieceLayerHashes = append(pieceLayerHashes, pieceHash)
					pieceHasher.Reset()
					remainingInPiece = pl
				}
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return metainfo.Info{}, nil, nil, readErr
		}
	}

	if info.Length > 0 && remainingInPiece != pl {
		var tailHash [32]byte
		copy(tailHash[:], pieceHasher.SumMinLength(nil, plInt))
		pieceLayerHashes = append(pieceLayerHashes, tailHash)
	}

	var piecesRoot [32]byte
	copy(piecesRoot[:], fileHasher.Sum(nil))
	pieceLayers := map[string]string{}
	if info.Length > pl {
		piecesRoot = merkle.RootWithPadHash(pieceLayerHashes, metainfo.HashForPiecePad(pl))
		var layer strings.Builder
		layer.Grow(len(pieceLayerHashes) * 32)
		for _, h := range pieceLayerHashes {
			layer.Write(h[:])
		}
		pieceLayers[string(piecesRoot[:])] = layer.String()
	}

	info.MetaVersion = 2
	leaf := metainfo.FileTree{File: metainfo.FileTreeFile{Length: info.Length, PiecesRoot: string(piecesRoot[:])}}
	info.FileTree = metainfo.FileTree{Dir: map[string]metainfo.FileTree{info.Name: leaf}}
	fileEntry, err := bencode.Marshal(metainfo.FileTreeFile{Length: info.Length, PiecesRoot: string(piecesRoot[:])})
	if err != nil {
		return metainfo.Info{}, nil, nil, err
	}
	leafBytes, err := bencode.Marshal(map[string]bencode.Bytes{"": fileEntry})
	if err != nil {
		return metainfo.Info{}, nil, nil, err
	}
	rawFileTree := map[string]bencode.Bytes{info.Name: leafBytes}

	if err := metainfo.ValidatePieceLayers(pieceLayers, &info.FileTree, info.PieceLength); err != nil {
		return metainfo.Info{}, nil, nil, fmt.Errorf("invalid v2 piece layers: %w", err)
	}

	return info, rawFileTree, pieceLayers, nil
}

func marshalHybridSingleFileInfo(info metainfo.Info, rawFileTree map[string]bencode.Bytes) ([]byte, error) {
	type hybridInfo struct {
		PieceLength int64                    `bencode:"piece length"`
		Pieces      []byte                   `bencode:"pieces,omitempty"`
		Name        string                   `bencode:"name"`
		Length      int64                    `bencode:"length,omitempty"`
		Private     *bool                    `bencode:"private,omitempty"`
		MetaVersion int64                    `bencode:"meta version,omitempty"`
		FileTree    map[string]bencode.Bytes `bencode:"file tree,omitempty"`
	}

	return bencode.Marshal(hybridInfo{
		PieceLength: info.PieceLength,
		Pieces:      info.Pieces,
		Name:        info.Name,
		Length:      info.Length,
		Private:     info.Private,
		MetaVersion: info.MetaVersion,
		FileTree:    rawFileTree,
	})
}

func writeTorrent(mi *metainfo.MetaInfo, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating torrent file: %w", err)
	}
	defer f.Close()
	return mi.Write(f)
}
