package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hali/internal/cache"
	"hali/internal/config"
	"hali/internal/events"
	"hali/internal/model"
	"hali/internal/networking"
	"hali/internal/policy"
	"hali/internal/publishing"
	"hali/internal/safepath"
	qbseeder "hali/internal/seeding/qbittorrent"
	txseeder "hali/internal/seeding/transmission"
	"hali/internal/torrent"
)

// Server is the daemon's IPC server.
type Server struct {
	engine      *torrent.Engine
	store       *cache.Store
	lanIndex    *LanIndex
	announcer   *Announcer
	lanSecret   []byte
	lanDebug    bool
	nodeID      string
	ipcSecret   string // non-empty on Windows: shared-secret for IPC auth
	webToken    string // non-empty when listenAddr is 0.0.0.0: bearer token for web auth
	configPath  string // path to config.json for persisting settings
	stats       *torrent.StatsCollector
	activity    ActivityBuffer
	eventWorker *events.Worker
	startedAt   time.Time
	ipcLn       net.Listener
	webLn       net.Listener
	webSrv      *http.Server
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup // long-lived goroutines owned for server lifetime
	connWG      sync.WaitGroup // transient IPC connection handlers

	paused   atomic.Bool
	lanShare atomic.Bool
	pauseMu  sync.Mutex
	pauseEnd time.Time
	pauseTmr *time.Timer
	settings sync.RWMutex
	cfg      policy.Settings // raw user intent (not policy-clamped); reads go through applySettings

	resolver   policy.Resolver
	resolverMu sync.RWMutex
	stopPolicy func() // cancels the policy watcher goroutine

	restartRequested atomic.Bool
}

var ErrRestartRequested = errors.New("daemon restart requested")

const (
	lanObservedSoftTTL = 2 * time.Minute
	lanObservedHardTTL = 15 * time.Minute
	maxLANSeenRows     = 500
)

func NewServer(engine *torrent.Engine, store *cache.Store, stats *torrent.StatsCollector) *Server {
	nodeID := generateNodeID()
	s := &Server{
		engine:     engine,
		store:      store,
		lanIndex:   NewLanIndex(nodeID),
		nodeID:     nodeID,
		stats:      stats,
		startedAt:  time.Now(),
		stopCh:     make(chan struct{}),
		resolver:   policy.NewResolver(policy.Policy{}), // no-op until Start loads HKLM
		stopPolicy: func() {},
	}
	s.lanShare.Store(true)
	return s
}

// applySettings is the single enforcement gate for all Settings changes.
// It enforces the current system policy as a ceiling over user input.
// Every path that produces runtime Settings must go through this method.
func (s *Server) applySettings(user policy.Settings) policy.Settings {
	s.resolverMu.RLock()
	defer s.resolverMu.RUnlock()
	return s.resolver.Apply(user)
}

// effectiveSettings returns the current effective Settings (policy applied to stored user intent).
func (s *Server) effectiveSettings() policy.Settings {
	s.settings.RLock()
	cfg := s.cfg
	s.settings.RUnlock()
	return s.applySettings(cfg)
}

func (s *Server) applyEngineSettings(reason string) {
	effective := s.effectiveSettings()
	if s.paused.Load() {
		// Use a tiny non-zero ceiling while paused to effectively halt transfers.
		s.engine.ApplyRateLimits(1, 1)
		slog.Info("engine_limits_applied",
			"max_upload_kbps", 1,
			"max_download_kbps", 1,
			"reason", reason,
			"paused", true,
		)
		return
	}
	upload := effective.MaxUploadKBps
	if !s.lanShare.Load() {
		// LAN sharing disabled: effectively stop uploads while keeping downloads configurable.
		upload = 1
	}
	s.engine.ApplyRateLimits(upload, effective.MaxDownloadKBps)
	slog.Info("engine_limits_applied",
		"max_upload_kbps", upload,
		"max_download_kbps", effective.MaxDownloadKBps,
		"reason", reason,
		"lan_sharing", s.lanShare.Load(),
		"paused", false,
	)
}

func (s *Server) pauseFor(minutes int) int {
	if minutes < 0 {
		minutes = 0
	}
	s.pauseMu.Lock()
	defer s.pauseMu.Unlock()

	if s.pauseTmr != nil {
		s.pauseTmr.Stop()
		s.pauseTmr = nil
	}

	s.paused.Store(true)
	if minutes > 0 {
		d := time.Duration(minutes) * time.Minute
		s.pauseEnd = time.Now().Add(d)
		s.pauseTmr = time.AfterFunc(d, func() {
			s.resumeNow("pause_timeout")
		})
	} else {
		s.pauseEnd = time.Time{}
	}
	return minutes
}

func (s *Server) resumeNow(reason string) bool {
	s.pauseMu.Lock()
	if s.pauseTmr != nil {
		s.pauseTmr.Stop()
		s.pauseTmr = nil
	}
	s.pauseEnd = time.Time{}
	wasPaused := s.paused.Swap(false)
	s.pauseMu.Unlock()
	if !wasPaused {
		return false
	}
	s.applyEngineSettings(reason)
	return true
}

func (s *Server) pauseState() (paused bool, untilUnix int64) {
	paused = s.paused.Load()
	s.pauseMu.Lock()
	defer s.pauseMu.Unlock()
	if s.pauseEnd.IsZero() {
		return paused, 0
	}
	return paused, s.pauseEnd.Unix()
}

func readyFilePath() string {
	return filepath.Join(config.ServiceDataDir(), ".ready")
}

// generateNodeID returns a random hex string used as this node's LAN identity.
// Will be replaced by blake3(ed25519_pubkey) when the signing layer is implemented.
func generateNodeID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("generateNodeID: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Start initializes daemon listeners on the fixed contract ports.
// IPC uses a Unix domain socket on Linux/macOS (0600 permissions — OS-enforced
// access control) and TCP with a shared-secret token on Windows.
func (s *Server) Start() error {
	httpHost, err := config.DaemonListenAddr()
	if err != nil {
		return err
	}
	nodeID, err := config.LoadOrCreateNodeID()
	if err != nil {
		return fmt.Errorf("load node id: %w", err)
	}
	s.nodeID = nodeID
	s.lanIndex = NewLanIndex(nodeID)
	s.configPath = filepath.Join(config.ServiceDataDir(), "config.json")

	cfg, err := config.LoadService()
	if err != nil {
		return fmt.Errorf("load service config: %w", err)
	}
	enabled, secret, err := config.ResolveLANHMACConfig(cfg)
	if err != nil {
		return fmt.Errorf("resolve LAN HMAC config: %w", err)
	}
	if enabled {
		s.lanSecret = secret
		slog.Info("lan_hmac_enabled", "config_path", s.configPath)
	} else {
		s.lanSecret = nil
	}
	s.lanDebug = cfg.DebugValue()

	// Load system policy (HKLM on Windows; no-op on other platforms).
	pstore := policy.DefaultStore()
	if initialPolicy, loadErr := policy.Load(pstore); loadErr == nil {
		s.resolverMu.Lock()
		s.resolver = policy.NewResolver(initialPolicy)
		s.resolverMu.Unlock()
		if initialPolicy.Managed() {
			slog.Info("policy_loaded")
		}
	}
	s.stopPolicy = policy.Watch(pstore, func() {
		p, _ := policy.Load(pstore)
		s.resolverMu.Lock()
		s.resolver = policy.NewResolver(p)
		s.resolverMu.Unlock()

		slog.Info("policy_reloaded")

		s.settings.RLock()
		prevCfg := s.cfg
		s.settings.RUnlock()
		effective := s.applySettings(prevCfg)
		if effective.MaxUploadKBps != prevCfg.MaxUploadKBps && prevCfg.MaxUploadKBps != 0 {
			slog.Info("settings_clamped",
				"field", "max_upload_kbps",
				"user", prevCfg.MaxUploadKBps,
				"applied", effective.MaxUploadKBps,
				"reason", "policy_reloaded",
			)
		}
		s.applyEngineSettings("policy_reloaded")
	})

	ipcLn, err := listenIPC(IPCAddr())
	if err != nil {
		return fmt.Errorf("IPC listen on %s: %w", IPCAddr(), err)
	}

	httpAddr := fmt.Sprintf("%s:%d", httpHost, config.HTTPPort)
	webLn, err := net.Listen("tcp", httpAddr)
	if err != nil {
		ipcLn.Close()
		return fmt.Errorf("port %s in use — run 'hali service status' to check if Hali is already running: %w", httpAddr, err)
	}

	// Windows: load or generate shared-secret for IPC defense-in-depth.
	if runtime.GOOS == "windows" {
		s.ipcSecret = loadOrGenerateIPCSecret()
	}

	return s.bindListeners(ipcLn, webLn, httpAddr)
}

// startOnAddrs binds ipc and http listeners on the given TCP addresses.
// Used by tests only — no IPC authentication, TCP transport regardless of OS.
func (s *Server) startOnAddrs(ipcAddr, httpAddr string) error {
	ipcLn, err := net.Listen("tcp", ipcAddr)
	if err != nil {
		return fmt.Errorf("port %s in use — run 'hali service status' to check if Hali is already running: %w", ipcAddr, err)
	}

	webLn, err := net.Listen("tcp", httpAddr)
	if err != nil {
		ipcLn.Close()
		return fmt.Errorf("port %s in use — run 'hali service status' to check if Hali is already running: %w", httpAddr, err)
	}

	return s.bindListeners(ipcLn, webLn, httpAddr)
}

// bindListeners completes server initialisation after listeners are created.
func (s *Server) bindListeners(ipcLn, webLn net.Listener, httpAddr string) error {
	s.ipcLn = ipcLn
	s.webLn = webLn
	s.webSrv = &http.Server{Handler: s.secureHandler(s.webMux(), httpAddr)}

	// Load persisted settings from config so they survive daemon restarts.
	if cfg, err := config.LoadService(); err == nil {
		s.settings.Lock()
		s.cfg.MaxUploadKBps = cfg.MaxUploadKBpsValue()
		s.cfg.MaxDownloadKBps = cfg.MaxDownloadKBpsValue()
		s.settings.Unlock()
	}
	s.applyEngineSettings("startup")

	// Generate web bearer token when listening on a non-loopback address.
	if httpAddr != "" && !strings.HasPrefix(httpAddr, "127.0.0.1:") && httpAddr != "localhost:"+fmt.Sprint(config.HTTPPort) {
		s.webToken = loadOrGenerateWebToken()
		slog.Info("web_token_generated", "token_path", webTokenPath())
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = s.webSrv.Serve(s.webLn)
	}()

	s.stats.Start()
	s.eventWorker = events.NewWorker(events.DefaultQueueDir(), config.LoadService)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.eventWorker.Run()
	}()

	// Start LAN announcer once the torrent port is known.
	s.announcer = NewAnnouncer(s.lanIndex, s.nodeID, s.lanSecret, len(s.lanSecret) > 0, s.lanDebug, s.seedingModels, s.engine.Port)
	s.announcer.Run()

	// Seed existing models in background.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.seedExisting()
	}()

	// Register optional publishing hooks (e.g. qBittorrent seeding).
	s.setupPublishingHooks()

	s.activity.Append(Event{Kind: "service.start", Message: "Hali daemon started"})

	caps := s.engine.NetworkCapabilities()
	slog.Info("network_mode_initialized", "network_mode", "lan_only", "lsd", caps.EnableLSD)

	slog.Info("daemon_started", "port", s.engine.Port(), "ipc_addr", IPCAddr(), "http_addr", httpAddr)

	// Write .ready sentinel so tray knows the HTTP server is accepting.
	os.WriteFile(readyFilePath(), []byte("1"), 0600) //nolint:errcheck

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.watchConfigAndRestart()
	}()

	return nil
}

func (s *Server) watchConfigAndRestart() {
	path := strings.TrimSpace(s.configPath)
	if path == "" {
		return
	}
	baseline, ok := fileFingerprint(path)
	if !ok {
		slog.Warn("config_watch_init_failed", "path", path)
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			fp, exists := fileFingerprint(path)
			if !exists {
				continue
			}
			if !ok {
				baseline = fp
				ok = true
				continue
			}
			if fp == baseline {
				continue
			}
			slog.Info("config_changed_restarting", "path", path)
			s.requestRestart("service_config_changed")
			return
		}
	}
}

func fileFingerprint(path string) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d", fi.ModTime().UnixNano(), fi.Size()), true
}

func (s *Server) requestRestart(reason string) {
	if !s.restartRequested.CompareAndSwap(false, true) {
		return
	}
	slog.Info("daemon_restart_requested", "reason", reason)
	go s.Stop()
}

// loadOrGenerateIPCSecret reads the IPC shared-secret from disk, generating
// and persisting a new one if none exists. Called on Windows only.
func loadOrGenerateIPCSecret() string {
	path := config.IPCSecretPath()
	if data, err := os.ReadFile(path); err == nil {
		secret := strings.TrimSpace(string(data))
		if len(secret) == 64 {
			return secret
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	secret := hex.EncodeToString(b)
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte(secret), 0600)
	return secret
}

// webTokenPath returns the path to the daemon web auth token file.
func webTokenPath() string {
	return filepath.Join(config.DataDir(), "daemon.token")
}

// loadOrGenerateWebToken reads or creates a bearer token for web dashboard auth
// when the daemon is listening on a non-loopback address.
func loadOrGenerateWebToken() string {
	path := webTokenPath()
	if data, err := os.ReadFile(path); err == nil {
		token := strings.TrimSpace(string(data))
		if len(token) >= 32 {
			return token
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	token := hex.EncodeToString(b)
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte(token), 0600)
	return token
}

// persistSettings writes the current user settings into config.json so they
// survive daemon restarts. Called from web API after a settings change.
func (s *Server) persistSettings() {
	s.settings.RLock()
	cfg := s.cfg
	s.settings.RUnlock()

	existing, err := config.LoadService()
	if err != nil {
		slog.Warn("failed to load config for settings persist", "error", err)
		return
	}
	existing.SetMaxUploadKBps(cfg.MaxUploadKBps)
	existing.SetMaxDownloadKBps(cfg.MaxDownloadKBps)
	if err := config.SaveService(existing); err != nil {
		slog.Warn("failed to persist settings to config", "error", err)
	}
}

// Run starts all services on the fixed contract ports and serves IPC until Stop.
func (s *Server) Run() error {
	if err := s.Start(); err != nil {
		return err
	}
	defer s.Stop()

	errCh := make(chan error, 1)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		errCh <- s.serveIPC()
	}()
	err := <-errCh
	if s.restartRequested.Load() {
		return ErrRestartRequested
	}
	return err
}

// serveIPC loops on the IPC listener until Stop is called.
// Callers must have already called Start or startOnAddrs.
// The caller is responsible for WaitGroup tracking when launching in a goroutine.
func (s *Server) serveIPC() error {
	sem := make(chan struct{}, 32) // bound concurrent IPC connections to prevent slow-loris
	for {
		conn, err := s.ipcLn.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return nil
			default:
				return err
			}
		}
		sem <- struct{}{}
		s.connWG.Add(1)
		go func() {
			defer s.connWG.Done()
			defer func() { <-sem }()
			s.handleConn(conn)
		}()
	}
}

func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		slog.Info("daemon_stopped")

		// Remove .ready before closing listeners so tray detects shutdown promptly.
		os.Remove(readyFilePath()) //nolint:errcheck

		close(s.stopCh)

		s.stopPolicy()

		if s.webSrv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			s.webSrv.Shutdown(ctx) //nolint:errcheck
			cancel()
		}
		if s.webLn != nil {
			s.webLn.Close() //nolint:errcheck
		}
		if s.ipcLn != nil {
			s.ipcLn.Close() //nolint:errcheck
		}

		if s.announcer != nil {
			s.announcer.Stop()
		}
		if s.eventWorker != nil {
			s.eventWorker.Stop()
		}

		// Wait for server-owned lifecycle goroutines (web/watchers/seeding/serveIPC)
		// and then in-flight IPC handlers before tearing down stats/engine.
		s.wg.Wait()
		s.connWG.Wait()

		s.stats.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.engine.Shutdown(ctx); err != nil {
			slog.Warn("engine_shutdown_incomplete", "error", err)
		}
		cancel()
	})
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	deadline := config.DefaultIPCDeadline
	if cfg, err := config.LoadService(); err == nil {
		deadline = cfg.IPCDeadline()
	}
	conn.SetDeadline(time.Now().Add(deadline))

	var req Request
	limitedReader := io.LimitReader(bufio.NewReader(conn), 1<<20) // 1 MiB max request size
	if err := json.NewDecoder(limitedReader).Decode(&req); err != nil {
		writeResp(conn, Response{OK: false, Error: "invalid request: " + err.Error()})
		return
	}
	// Windows IPC defense-in-depth: verify shared secret.
	// On Linux/macOS the Unix socket (0600) provides OS-level enforcement.
	if s.ipcSecret != "" && req.Token != s.ipcSecret {
		writeResp(conn, Response{OK: false, Error: "unauthorized"})
		return
	}
	writeResp(conn, s.handle(req))
}

func (s *Server) handle(req Request) Response {
	switch req.Cmd {
	case CmdStatus:
		return s.handleStatus()
	case CmdList:
		return Response{OK: false, Error: "list is not served by daemon IPC; use local cache list"}
	case CmdSeed:
		return s.handleSeed(req)
	case CmdSeedStatus:
		return s.handleSeedStatus(req)
	case CmdEnqueueEvent:
		return s.handleEnqueueEvent(req)
	case CmdLanQuery:
		return s.handleLanQuery(req)
	case CmdLanSeen:
		return s.handleLanSeen()
	case CmdDownload:
		return s.handleDownload(req)
	case CmdCancelJob:
		return s.handleCancelJob(req)
	case CmdJobStatus:
		return s.handleJobStatus(req)
	case CmdStats:
		return s.handleStats()
	case CmdStop:
		go s.Stop()
		return Response{OK: true}
	default:
		return Response{OK: false, Error: "unknown command: " + string(req.Cmd)}
	}
}

func (s *Server) handleStatus() Response {
	s.lanIndex.PruneOlderThan(time.Now().Add(-lanObservedHardTTL))

	entries := s.engine.Entries()
	seeding := make([]SeedInfo, len(entries))
	for i, e := range entries {
		seeding[i] = SeedInfo{
			ModelID:    e.ModelID,
			Infohash:   e.Identity.InfohashV1,
			InfohashV2: e.Identity.InfohashV2,
			MagnetURI:  e.MagnetURI,
			Status:     string(e.Status),
			Peers:      e.Peers,
		}
	}

	lanSnap := s.lanIndex.Snapshot()
	lanEntries := summarizeLANEntries(lanSnap, time.Now())
	caps := s.engine.NetworkCapabilities()

	s.resolverMu.RLock()
	managed := s.resolver.Managed()
	lockedFields := s.resolver.LockedFields()
	s.resolverMu.RUnlock()

	return Response{OK: true, Data: StatusData{
		PID:    os.Getpid(),
		Uptime: time.Since(s.startedAt).Round(time.Second).String(),
		Port:   s.engine.Port(),
		Network: NetworkStatusData{
			Mode: s.engine.NetworkMode().String(),
			Capabilities: NetworkCapabilitiesData{
				LSD: caps.EnableLSD,
			},
		},
		Seeding:      seeding,
		LAN:          lanEntries,
		Managed:      managed,
		LockedFields: lockedFields,
	}}
}

func (s *Server) handleStats() Response {
	return Response{OK: true, Data: s.statsSnapshot()}
}

func (s *Server) statsSnapshot() torrent.StatsSnapshot {
	return s.stats.Snapshot(time.Since(s.startedAt))
}

func (s *Server) handleSeed(req Request) Response {
	if req.Dir == "" || req.Filename == "" || req.ModelID == "" {
		return Response{OK: false, Error: "seed requires dir, filename, model_id"}
	}
	canonDir, err := safepath.Canonical(s.store.Root, req.Dir)
	if err != nil {
		return Response{OK: false, Error: "invalid dir: " + err.Error()}
	}
	req.Dir = canonDir
	if !safepath.IsSafeFilename(req.Filename) {
		return Response{OK: false, Error: "unsafe filename"}
	}
	jobID := s.engine.StartSeed(req.Dir, req.Filename, req.ModelID, req.HFRepo, req.HFRevision, req.Pieces, req.FileSize)
	return Response{OK: true, Data: map[string]string{"status": "hashing", "job_id": jobID}}
}

func (s *Server) handleSeedStatus(req Request) Response {
	if req.JobID == "" {
		return Response{OK: false, Error: "seed_status requires job_id"}
	}
	job, ok := s.engine.SeedJobStatus(req.JobID)
	if !ok {
		return Response{OK: false, Error: "seed job not found: " + req.JobID}
	}
	data := SeedStatusData{
		JobID:     job.ID,
		ModelID:   job.ModelID,
		Infohash:  job.Identity.InfohashV1,
		MagnetURI: job.MagnetURI,
		Done:      job.Done,
		Error:     job.Error,
	}
	if job.Done && job.Error == "" && req.Dir != "" {
		s.store.SetIdentity(req.Dir, job.Identity)
		slog.Debug("qbittorrent: seed job done, emitting TorrentPublishedEvent",
			"infohash", job.Identity.InfohashV1,
			"content_dir", req.Dir,
		)
		// Emit unconditionally — independent of telemetry/reachability gates.
		if job.Identity.InfohashV1 != "" {
			publishing.Emit(context.Background(), publishing.TorrentPublishedEvent{
				InfoHash:   job.Identity.InfohashV1,
				ContentDir: req.Dir,
			})
		} else {
			slog.Warn("qbittorrent: seed job done but InfohashV1 is empty, skipping emit")
		}
	} else if job.Done {
		slog.Debug("qbittorrent: seed job done but skipping emit",
			"has_error", job.Error != "",
			"has_dir", req.Dir != "",
		)
	}
	return Response{OK: true, Data: data}
}

func (s *Server) handleEnqueueEvent(req Request) Response {
	if req.Event == nil {
		return Response{OK: false, Error: "enqueue_event requires event"}
	}
	if s.eventWorker == nil {
		return Response{OK: false, Error: "event worker not initialized"}
	}
	cfg, err := config.LoadService()
	if err != nil {
		return Response{OK: false, Error: "load config: " + err.Error()}
	}
	if !cfg.TelemetryEnabledValue() {
		return Response{OK: true, Data: map[string]string{"status": "disabled"}}
	}
	policy := networking.PublishReachabilityPolicy{RequiresInternetReachability: true}
	if networking.PublishRequiresConfirmation(policy) && !req.AllowUnreachablePublish {
		return Response{OK: false, Error: "publishing requires internet reachability in current context; set --allow-unreachable-publish to override"}
	}
	if err := s.eventWorker.Enqueue(*req.Event); err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	s.activity.Append(Event{Kind: "telemetry.enqueue", Message: "queued model pull event", ModelID: req.Event.ModelID})
	return Response{OK: true, Data: map[string]string{"status": "queued"}}
}

func (s *Server) setupPublishingHooks() {
	slog.Debug("daemon: setupPublishingHooks called")
	s.setupQBittorrentHook()
	s.setupTransmissionHook()
}

func (s *Server) setupQBittorrentHook() {
	cfg, err := config.LoadService()
	if err != nil {
		slog.Warn("qbittorrent: failed to load service config", "err", err)
		return
	}
	slog.Debug("qbittorrent: config loaded", "enabled", cfg.QBittorrentEnabled())
	if !cfg.QBittorrentEnabled() {
		slog.Info("qbittorrent: integration disabled (enabled=false or url missing)")
		return
	}
	slog.Debug("qbittorrent: creating seeder", "url", cfg.QBittorrent.URL, "torrent_dir", s.engine.TorrentDir())
	seeder, err := qbseeder.NewSeeder(*cfg.QBittorrent, s.engine.TorrentDir())
	if err != nil {
		slog.Warn("qbittorrent: invalid config, hook disabled", "err", err)
		return
	}
	publishing.Register(&qbseeder.QBittorrentHook{Seeder: seeder})
	slog.Info("qbittorrent: seeding hook registered", "url", cfg.QBittorrent.URL)
}

func (s *Server) setupTransmissionHook() {
	cfg, err := config.LoadService()
	if err != nil {
		slog.Warn("transmission: failed to load service config", "err", err)
		return
	}
	if !cfg.TransmissionEnabled() {
		slog.Info("transmission: integration disabled (enabled=false or url missing)")
		return
	}
	slog.Debug("transmission: creating seeder", "url", cfg.Transmission.URL, "torrent_dir", s.engine.TorrentDir())
	seeder, err := txseeder.NewSeeder(*cfg.Transmission, s.engine.TorrentDir())
	if err != nil {
		slog.Warn("transmission: invalid config, hook disabled", "err", err)
		return
	}
	publishing.Register(&txseeder.TransmissionHook{Seeder: seeder})
	slog.Info("transmission: seeding hook registered", "url", cfg.Transmission.URL)
}

func (s *Server) handleLanQuery(req Request) Response {
	if req.ModelID == "" {
		return Response{OK: false, Error: "lan_query requires model_id"}
	}
	s.lanIndex.PruneOlderThan(time.Now().Add(-lanObservedHardTTL))
	hints := s.lanIndex.Query(req.ModelID)
	// selectHint applies TTL policy and picks one candidate.
	// Torrent engine receives infohash only — no LAN structs passed further.
	best := selectHint(hints)
	if best == nil {
		return Response{OK: true, Data: nil}
	}
	peerCount, lastSeen := peerCountAndLastSeen(hints, best.Identity.InfohashV1, time.Now())
	return Response{OK: true, Data: LanQueryData{
		ModelID:     req.ModelID,
		Revision:    best.Revision,
		Infohash:    best.Identity.InfohashV1,
		InfohashV2:  best.Identity.InfohashV2,
		HFRepo:      best.HFRepo,
		ArtifactKey: artifactKey(req.ModelID, best.Revision),
		PeerCount:   peerCount,
		LastSeen:    lastSeen.Unix(),
		PeerAddrs:   peerAddrsFromHints(hints, best.Identity.InfohashV1, time.Now()),
	}}
}

func (s *Server) handleLanSeen() Response {
	s.lanIndex.PruneOlderThan(time.Now().Add(-lanObservedHardTTL))
	snap := s.lanIndex.Snapshot()
	rows := summarizeLANSeen(snap, time.Now())
	if len(rows) > maxLANSeenRows {
		rows = rows[:maxLANSeenRows]
	}
	return Response{OK: true, Data: LanSeenData{Announcements: rows}}
}

// selectHint picks one candidate infohash from received LAN hints.
// Prefers hints seen within 2 minutes; falls back to any within 15 minutes.
// Selection policy lives here, not in LanIndex.
func selectHint(hints []ModelHint) *ModelHint {
	return pickBestByFreshness(hints, time.Now())
}

// summarizeLANEntries projects a LAN hint snapshot into status-line rows.
//
// This is observational metadata only and is not an authority for transfer
// correctness or reachability.
func summarizeLANEntries(snap map[string][]ModelHint, now time.Time) []LanEntry {
	out := make([]LanEntry, 0, len(snap))
	for modelID, hints := range snap {
		best := pickBestObservedHint(hints, now)
		if best == nil {
			continue
		}
		peers, _ := peerCountAndLastSeen(hints, best.Identity.InfohashV1, now)
		if peers == 0 {
			continue
		}
		out = append(out, LanEntry{
			ModelID:    modelID,
			Infohash:   best.Identity.InfohashV1,
			InfohashV2: best.Identity.InfohashV2,
			Peers:      peers,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModelID < out[j].ModelID })
	return out
}

func summarizeLANSeen(snap map[string][]ModelHint, now time.Time) []LanSeenEntry {
	rows := make([]LanSeenEntry, 0, len(snap))
	for modelID, hints := range snap {
		best := pickBestObservedHint(hints, now)
		if best == nil {
			continue
		}
		peerCount, lastSeen := peerCountAndLastSeen(hints, best.Identity.InfohashV1, now)
		if peerCount == 0 {
			continue
		}
		rows = append(rows, LanSeenEntry{
			ModelID:     modelID,
			HFRepo:      best.HFRepo,
			Revision:    best.Revision,
			Infohash:    best.Identity.InfohashV1,
			InfohashV2:  best.Identity.InfohashV2,
			ArtifactKey: artifactKey(modelID, best.Revision),
			PeerCount:   peerCount,
			LastSeen:    lastSeen.Unix(),
			PeerAddrs:   peerAddrsFromHints(hints, best.Identity.InfohashV1, now),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].LastSeen == rows[j].LastSeen {
			return rows[i].ModelID < rows[j].ModelID
		}
		return rows[i].LastSeen > rows[j].LastSeen
	})
	return rows
}

func artifactKey(modelID, revision string) string {
	if strings.TrimSpace(revision) == "" {
		return modelID
	}
	return modelID + "@" + strings.TrimSpace(revision)
}

func pickBestObservedHint(hints []ModelHint, now time.Time) *ModelHint {
	return pickBestByFreshness(hints, now)
}

func pickBestByFreshness(hints []ModelHint, now time.Time) *ModelHint {
	var fresh *ModelHint
	var freshAge = lanObservedSoftTTL + 1
	var stale *ModelHint
	var staleAge = lanObservedHardTTL + 1
	for i := range hints {
		age := now.Sub(hints[i].SeenAt)
		if age > lanObservedHardTTL {
			continue
		}
		if age <= lanObservedSoftTTL {
			if fresh == nil || age < freshAge {
				fresh = &hints[i]
				freshAge = age
			}
			continue
		}
		if stale == nil || age < staleAge {
			stale = &hints[i]
			staleAge = age
		}
	}
	if fresh != nil {
		return fresh
	}
	return stale
}

func peerCountAndLastSeen(hints []ModelHint, infohash string, now time.Time) (int, time.Time) {
	seenNodes := make(map[string]struct{}, len(hints))
	var lastSeen time.Time
	for _, h := range hints {
		if h.Identity.InfohashV1 != infohash || h.PubkeyHash == "" {
			continue
		}
		if age := now.Sub(h.SeenAt); age > lanObservedHardTTL {
			continue
		}
		seenNodes[h.PubkeyHash] = struct{}{}
		if h.SeenAt.After(lastSeen) {
			lastSeen = h.SeenAt
		}
	}
	return len(seenNodes), lastSeen
}

func (s *Server) handleDownload(req Request) Response {
	if req.ModelID == "" || req.Infohash == "" || req.Dir == "" {
		return Response{OK: false, Error: "download requires model_id, infohash, dir"}
	}
	canonDir, err := safepath.Canonical(s.store.Root, req.Dir)
	if err != nil {
		return Response{OK: false, Error: "invalid dir: " + err.Error()}
	}
	req.Dir = canonDir
	peerAddrs := make([]string, 0, len(req.PeerAddrs))
	for _, addr := range req.PeerAddrs {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		peerAddrs = append(peerAddrs, addr)
	}
	if len(peerAddrs) == 0 {
		peerAddrs = s.peerAddrsFor(req.ModelID, req.Infohash, time.Now())
	}
	jobID, err := s.engine.StartDownload(req.Dir, req.ModelID, req.Infohash, req.InfohashV2, peerAddrs)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true, Data: map[string]interface{}{"job_id": jobID, "peer_addrs": peerAddrs}}
}

func (s *Server) peerAddrsFor(modelID, infohash string, now time.Time) []string {
	hints := s.lanIndex.Query(modelID)
	return peerAddrsFromHints(hints, infohash, now)
}

func peerAddrsFromHints(hints []ModelHint, infohash string, now time.Time) []string {
	if len(hints) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(hints))
	out := make([]string, 0, len(hints))
	for _, h := range hints {
		if h.Identity.InfohashV1 != infohash {
			continue
		}
		if now.Sub(h.SeenAt) > lanObservedHardTTL {
			continue
		}
		addr := strings.TrimSpace(h.PeerAddr)
		if addr == "" {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out
}

func (s *Server) handleJobStatus(req Request) Response {
	if req.JobID == "" {
		return Response{OK: false, Error: "job_status requires job_id"}
	}
	job, ok := s.engine.JobStatus(req.JobID)
	if !ok {
		return Response{OK: false, Error: "job not found: " + req.JobID}
	}
	data := JobStatusData{
		JobID:         job.ID,
		ModelID:       job.ModelID,
		MagnetURI:     job.MagnetURI,
		Filename:      job.Filename,
		Written:       job.Written,
		Total:         job.Total,
		RateBps:       job.RateBps,
		ElapsedSec:    job.ElapsedSec,
		ETASeconds:    job.ETASeconds,
		ActivePeers:   job.ActivePeers,
		PendingPeers:  job.PendingPeers,
		HalfOpenPeers: job.HalfOpenPeers,
		TotalPeers:    job.TotalPeers,
		Done:          job.Done,
		Error:         job.Error,
	}
	// On completion, persist identity and write metadata.
	if job.Done && job.Error == "" {
		s.store.SetIdentity(req.Dir, job.Identity)
	}
	return Response{OK: true, Data: data}
}

func (s *Server) handleCancelJob(req Request) Response {
	if req.JobID == "" {
		return Response{OK: false, Error: "cancel_job requires job_id"}
	}
	if ok := s.engine.CancelDownload(req.JobID); !ok {
		return Response{OK: false, Error: "job not found: " + req.JobID}
	}
	return Response{OK: true, Data: map[string]string{"status": "canceled", "job_id": req.JobID}}
}

func (s *Server) seedExisting() {
	entries, err := s.store.List()
	if err != nil {
		return
	}
	for _, e := range entries {
		if len(e.Meta.Files) == 0 {
			continue
		}
		var ih string
		if e.Meta.Infohash != "" {
			ih, err = s.engine.SeedFromTorrentFile(e.Dir, e.Meta.Infohash, e.ID.String(), torrent.TorrentIdentity{
				InfohashV1: e.Meta.Infohash,
				InfohashV2: e.Meta.InfohashV2,
			})
		}
		if e.Meta.Infohash == "" || err != nil {
			// No recorded infohash or torrent file missing — rehash.
			ih, err = s.engine.Seed(e.Dir, e.Meta.Files[0], e.ID.String(), e.Meta.HFRepo, e.Meta.HFRevision)
		}
		if err != nil {
			slog.Error("seed existing failed", "model_id", e.ID, "error", err)
			continue
		}
		s.store.SetIdentity(e.Dir, torrent.IdentityFromV1(ih))
	}
}

// seedingModels returns the current seeding list for LAN announcements.
func (s *Server) seedingModels() []lanModelAnnounce {
	if !s.lanShare.Load() {
		return nil
	}
	entries := s.engine.Entries()
	out := make([]lanModelAnnounce, 0, len(entries))
	for _, e := range entries {
		if e.Status == torrent.StatusSeeding && e.Identity.InfohashV1 != "" {
			hfRepo := ""
			revision := ""
			if id, err := model.Parse(e.ModelID); err == nil {
				if meta, err := s.store.LoadMeta(id); err == nil {
					hfRepo = strings.TrimSpace(meta.HFRepo)
					revision = strings.TrimSpace(meta.HFRevision)
				}
			}
			out = append(out, lanModelAnnounce{
				ModelID:    e.ModelID,
				Infohash:   e.Identity.InfohashV1,
				InfohashV2: e.Identity.InfohashV2,
				HFRepo:     hfRepo,
				Revision:   revision,
			})
		}
	}
	return out
}

func writeResp(conn net.Conn, resp Response) {
	json.NewEncoder(conn).Encode(resp)
}

// IPCAddr returns the platform IPC address:
// Unix socket path on Linux/macOS, TCP address on Windows.
func IPCAddr() string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("127.0.0.1:%d", config.IPCPort)
	}
	return config.IPCSocketPath()
}

// HTTPAddr returns the fixed HTTP address for the daemon.
func HTTPAddr() string {
	return fmt.Sprintf("127.0.0.1:%d", config.HTTPPort)
}
