package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var userHomeDir = os.UserHomeDir

// These ports are part of the stable external contract.
// They must NEVER be made user-configurable. Doing so reintroduces
// the daemon.addr discovery race that fixed ports were chosen to eliminate.
const (
	IPCPort  = 47432 // TCP loopback, IPC (JSON)
	HTTPPort = 47433 // TCP loopback, HTTP dashboard + API
)

const DefaultRegistryIngestURL = "https://api.hali.network/ingest"

// DataDir returns the root data directory for hali.
// On Windows: %ProgramData%\Hali
// On other platforms: ~/.hali
func DataDir() string {
	if d := os.Getenv("PROGRAMDATA"); d != "" {
		return filepath.Join(d, "Hali")
	}
	home, err := userHomeDir()
	if err != nil {
		return ".hali"
	}
	return filepath.Join(home, ".hali")
}

// QBittorrentConfig configures the optional qBittorrent WebUI integration.
// When enabled, published torrents are automatically registered with qBittorrent
// for persistent internet seeding. Failure never affects publishing.
type QBittorrentConfig struct {
	Enabled       bool     `json:"enabled"`
	URL           string   `json:"url"`
	Username      string   `json:"username"`
	Password      string   `json:"password"`
	Category      string   `json:"category,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	SkipTLSVerify bool     `json:"skip_tls_verify,omitempty"`
}

// TransmissionConfig configures the optional Transmission RPC integration.
// When enabled, published torrents are automatically registered with Transmission
// for persistent internet seeding. Failure never affects publishing.
type TransmissionConfig struct {
	Enabled       bool   `json:"enabled"`
	URL           string `json:"url"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	SkipTLSVerify bool   `json:"skip_tls_verify,omitempty"`
}

// File is the on-disk user configuration stored in DataDir().
type File struct {
	StreamingHash       bool   `json:"streaming_hash"`
	Debug               *bool  `json:"debug,omitempty"`
	TelemetryEnabled    *bool  `json:"telemetry_enabled,omitempty"`
	RegistryIngestURL   string `json:"registry_ingest_url,omitempty"`
	LANHMACEnabled      *bool  `json:"lan_hmac_enabled,omitempty"`
	LANHMACSharedSecret string `json:"lan_hmac_shared_secret,omitempty"`
	ModelsDir           string `json:"models_dir,omitempty"`
	LMStudioModels      string `json:"lmstudio_models_dir,omitempty"`
	OllamaModelsDir     string `json:"ollama_models_dir,omitempty"`
	DaemonListenAddr    string `json:"daemon_listen_addr,omitempty"`
	MaxUploadMBps       int    `json:"max_upload_mbps,omitempty"`
	MaxDownloadMBps     int    `json:"max_download_mbps,omitempty"`
	// Timeout overrides (milliseconds). 0 means use the built-in default.
	TorrentMetaTimeoutMs int `json:"torrent_meta_timeout_ms,omitempty"`
	IPCDeadlineMs        int `json:"ipc_deadline_ms,omitempty"`
	LaunchTimeoutMs      int `json:"launch_timeout_ms,omitempty"`
	// QBittorrent holds optional qBittorrent WebUI integration settings.
	// When nil, qBittorrent seeding is disabled.
	QBittorrent *QBittorrentConfig `json:"qbittorrent,omitempty"`
	// Transmission holds optional Transmission RPC integration settings.
	// When nil, Transmission seeding is disabled.
	Transmission *TransmissionConfig `json:"transmission,omitempty"`
}

func boolPtr(v bool) *bool {
	b := v
	return &b
}

// defaultVisibleConfig returns the user-facing keys that should always be
// visible in config.json for transparency and easy manual editing.
func defaultVisibleConfig(modelsDir string) File {
	return File{
		StreamingHash:       true,
		Debug:               boolPtr(false),
		TelemetryEnabled:    boolPtr(true),
		RegistryIngestURL:   DefaultRegistryIngestURL,
		LANHMACEnabled:      boolPtr(false),
		LANHMACSharedSecret: "",
		ModelsDir:           modelsDir,
		LMStudioModels:      "",
		OllamaModelsDir:     "",
		DaemonListenAddr:    "0.0.0.0",
		MaxUploadMBps:       0,
		MaxDownloadMBps:     0,
	}
}

// DefaultTorrentMetaTimeout is the fallback wait for torrent metadata from peers.
const DefaultTorrentMetaTimeout = 60 * time.Second

// DefaultIPCDeadline is the fallback per-connection IPC deadline.
const DefaultIPCDeadline = 30 * time.Second

// DefaultLaunchTimeout is the fallback wait for daemon .ready sentinel.
const DefaultLaunchTimeout = 5 * time.Second

// TorrentMetaTimeout returns the configured torrent metadata timeout.
func (f File) TorrentMetaTimeout() time.Duration {
	if f.TorrentMetaTimeoutMs > 0 {
		return time.Duration(f.TorrentMetaTimeoutMs) * time.Millisecond
	}
	return DefaultTorrentMetaTimeout
}

// IPCDeadline returns the configured per-connection IPC deadline.
func (f File) IPCDeadline() time.Duration {
	if f.IPCDeadlineMs > 0 {
		return time.Duration(f.IPCDeadlineMs) * time.Millisecond
	}
	return DefaultIPCDeadline
}

// LaunchTimeout returns the configured daemon launch timeout.
func (f File) LaunchTimeout() time.Duration {
	if f.LaunchTimeoutMs > 0 {
		return time.Duration(f.LaunchTimeoutMs) * time.Millisecond
	}
	return DefaultLaunchTimeout
}

func (f File) TelemetryEnabledValue() bool {
	if f.TelemetryEnabled == nil {
		return true
	}
	return *f.TelemetryEnabled
}

func (f File) DebugValue() bool {
	if f.Debug == nil {
		return false
	}
	return *f.Debug
}

func (f File) RegistryIngestURLValue() string {
	if value := strings.TrimSpace(f.RegistryIngestURL); value != "" {
		return value
	}
	return DefaultRegistryIngestURL
}

func (f File) LANHMACEnabledValue() bool {
	if f.LANHMACEnabled == nil {
		return false
	}
	return *f.LANHMACEnabled
}

// MaxUploadKBpsValue returns the upload limit in KiB/s expected by runtime.
// 0 means unlimited.
func (f File) MaxUploadKBpsValue() int {
	if f.MaxUploadMBps <= 0 {
		return 0
	}
	return f.MaxUploadMBps * 1024
}

// MaxDownloadKBpsValue returns the download limit in KiB/s expected by runtime.
// 0 means unlimited.
func (f File) MaxDownloadKBpsValue() int {
	if f.MaxDownloadMBps <= 0 {
		return 0
	}
	return f.MaxDownloadMBps * 1024
}

// SetMaxUploadKBps stores a runtime KiB/s upload limit into config Mbps units.
func (f *File) SetMaxUploadKBps(kbps int) {
	if kbps <= 0 {
		f.MaxUploadMBps = 0
		return
	}
	f.MaxUploadMBps = int(math.Round(float64(kbps) / 1024.0))
}

// SetMaxDownloadKBps stores a runtime KiB/s download limit into config Mbps units.
func (f *File) SetMaxDownloadKBps(kbps int) {
	if kbps <= 0 {
		f.MaxDownloadMBps = 0
		return
	}
	f.MaxDownloadMBps = int(math.Round(float64(kbps) / 1024.0))
}

// QBittorrentEnabled returns true when qBittorrent integration is enabled and configured.
func (f File) QBittorrentEnabled() bool {
	return f.QBittorrent != nil && f.QBittorrent.Enabled && strings.TrimSpace(f.QBittorrent.URL) != ""
}

// TransmissionEnabled returns true when Transmission integration is enabled and configured.
func (f File) TransmissionEnabled() bool {
	return f.Transmission != nil && f.Transmission.Enabled && strings.TrimSpace(f.Transmission.URL) != ""
}

func Load() (File, error) {
	return LoadFromFile(filepath.Join(DataDir(), "config.json"))
}

func LoadService() (File, error) {
	return LoadFromFile(filepath.Join(ServiceDataDir(), "config.json"))
}

func Save(cfg File) error {
	path := filepath.Join(DataDir(), "config.json")
	return saveAtPath(path, cfg)
}

// EnsureModelsDirPersisted writes the resolved models directory into config.json
// if it is not already set, so users can find and change it by editing the file.
// Errors are non-fatal — callers should ignore them.
func EnsureModelsDirPersisted() error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.ModelsDir) != "" {
		return nil
	}
	cfg.ModelsDir = filepath.Join(DataDir(), "models")
	return Save(cfg)
}

// EnsureConfigMaterialized writes a fully visible baseline config.json to
// DataDir when the file does not exist yet.
func EnsureConfigMaterialized() error {
	if !shouldMaterializeUserConfig() {
		return nil
	}
	path := filepath.Join(DataDir(), "config.json")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat config %s: %w", path, err)
	}
	return saveAtPath(path, defaultVisibleConfig(filepath.Join(DataDir(), "models")))
}

func shouldMaterializeUserConfig() bool {
	return true
}

// EnsureServiceConfigMaterialized writes a fully visible baseline config.json
// for service-managed runtime when the file does not exist yet.
func EnsureServiceConfigMaterialized() error {
	path := filepath.Join(ServiceDataDir(), "config.json")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat service config %s: %w", path, err)
	}
	return saveAtPath(path, defaultVisibleConfig(ServiceModelsDir()))
}

func SaveService(cfg File) error {
	path := filepath.Join(ServiceDataDir(), "config.json")
	return saveAtPath(path, cfg)
}

func saveAtPath(path string, cfg File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data := renderConfigJSONC(cfg, filepath.Dir(path))
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func LoadFromFile(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, nil
		}
		return File{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg File
	if err := json.Unmarshal(stripJSONComments(data), &cfg); err != nil {
		return File{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

func renderConfigJSONC(cfg File, dataRoot string) []byte {
	merged := cfg
	defaults := defaultVisibleConfig(filepath.Join(dataRoot, "models"))

	if merged.TelemetryEnabled == nil {
		merged.TelemetryEnabled = defaults.TelemetryEnabled
	}
	if merged.Debug == nil {
		merged.Debug = defaults.Debug
	}
	if strings.TrimSpace(merged.RegistryIngestURL) == "" {
		merged.RegistryIngestURL = defaults.RegistryIngestURL
	}
	if merged.LANHMACEnabled == nil {
		merged.LANHMACEnabled = defaults.LANHMACEnabled
	}
	if strings.TrimSpace(merged.LANHMACSharedSecret) == "" {
		merged.LANHMACSharedSecret = defaults.LANHMACSharedSecret
	}
	if strings.TrimSpace(merged.ModelsDir) == "" {
		merged.ModelsDir = defaults.ModelsDir
	}
	if strings.TrimSpace(merged.DaemonListenAddr) == "" {
		merged.DaemonListenAddr = defaults.DaemonListenAddr
	}

	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString("  // Hash pieces while downloading from Hugging Face to speed up seeding handoff.\n")
	b.WriteString(fmt.Sprintf("  \"streaming_hash\": %t,\n\n", merged.StreamingHash))

	b.WriteString("  // Enable verbose daemon diagnostics.\n")
	b.WriteString(fmt.Sprintf("  \"debug\": %t,\n\n", merged.DebugValue()))

	b.WriteString("  // Share anonymous pull events to improve registry quality (recommended).\n")
	b.WriteString(fmt.Sprintf("  \"telemetry_enabled\": %t,\n\n", merged.TelemetryEnabledValue()))

	b.WriteString("  // Telemetry ingest destination.\n")
	b.WriteString(fmt.Sprintf("  \"registry_ingest_url\": %s,\n\n", jsonQuote(merged.RegistryIngestURLValue())))

	b.WriteString("  // Require HMAC signatures on LAN announcements. Set true to enable shared-secret auth.\n")
	b.WriteString(fmt.Sprintf("  \"lan_hmac_enabled\": %t,\n\n", merged.LANHMACEnabledValue()))

	b.WriteString("  // Shared secret for LAN HMAC auth (64-char hex). Used only when lan_hmac_enabled is true.\n")
	b.WriteString(fmt.Sprintf("  \"lan_hmac_shared_secret\": %s,\n\n", jsonQuote(strings.TrimSpace(merged.LANHMACSharedSecret))))

	b.WriteString("  // Directory where downloaded models are stored.\n")
	b.WriteString(fmt.Sprintf("  \"models_dir\": %s,\n\n", jsonQuote(strings.TrimSpace(merged.ModelsDir))))

	b.WriteString("  // Optional LM Studio models directory override.\n")
	b.WriteString(fmt.Sprintf("  \"lmstudio_models_dir\": %s,\n\n", jsonQuote(strings.TrimSpace(merged.LMStudioModels))))

	b.WriteString("  // Optional Ollama models directory override.\n")
	b.WriteString(fmt.Sprintf("  \"ollama_models_dir\": %s,\n\n", jsonQuote(strings.TrimSpace(merged.OllamaModelsDir))))

	b.WriteString("  // Daemon HTTP bind address (fixed to 0.0.0.0).\n")
	b.WriteString(fmt.Sprintf("  \"daemon_listen_addr\": %s", jsonQuote(strings.TrimSpace(merged.DaemonListenAddr))))

	b.WriteString(",\n\n")
	b.WriteString("  // Advanced: upload rate limit in Mbps (0 = unlimited).\n")
	b.WriteString(fmt.Sprintf("  \"max_upload_mbps\": %d", merged.MaxUploadMBps))

	b.WriteString(",\n\n")
	b.WriteString("  // Advanced: download rate limit in Mbps (0 = unlimited).\n")
	b.WriteString(fmt.Sprintf("  \"max_download_mbps\": %d", merged.MaxDownloadMBps))
	if merged.TorrentMetaTimeoutMs > 0 {
		b.WriteString(",\n\n")
		b.WriteString("  // Advanced: torrent metadata timeout in milliseconds.\n")
		b.WriteString(fmt.Sprintf("  \"torrent_meta_timeout_ms\": %d", merged.TorrentMetaTimeoutMs))
	}
	if merged.IPCDeadlineMs > 0 {
		b.WriteString(",\n\n")
		b.WriteString("  // Advanced: IPC request deadline in milliseconds.\n")
		b.WriteString(fmt.Sprintf("  \"ipc_deadline_ms\": %d", merged.IPCDeadlineMs))
	}
	if merged.LaunchTimeoutMs > 0 {
		b.WriteString(",\n\n")
		b.WriteString("  // Advanced: daemon launch timeout in milliseconds.\n")
		b.WriteString(fmt.Sprintf("  \"launch_timeout_ms\": %d", merged.LaunchTimeoutMs))
	}
	if merged.QBittorrent != nil && strings.TrimSpace(merged.QBittorrent.URL) != "" {
		b.WriteString(",\n\n")
		b.WriteString("  // Optional: register seeded torrents with qBittorrent for persistent internet seeding.\n")
		b.WriteString("  // Set enabled=true to activate. qBittorrent must run with WebUI enabled.\n")
		b.WriteString("  // Failure here never fails a publish.\n")
		b.WriteString("  \"qbittorrent\": {\n")
		b.WriteString(fmt.Sprintf("    \"enabled\": %t,\n", merged.QBittorrent.Enabled))
		b.WriteString(fmt.Sprintf("    \"url\": %s,\n", jsonQuote(strings.TrimSpace(merged.QBittorrent.URL))))
		b.WriteString(fmt.Sprintf("    \"username\": %s", jsonQuote(merged.QBittorrent.Username)))
		if merged.QBittorrent.Category != "" {
			b.WriteString(fmt.Sprintf(",\n    \"category\": %s", jsonQuote(merged.QBittorrent.Category)))
		}
		if merged.QBittorrent.SkipTLSVerify {
			b.WriteString(",\n    \"skip_tls_verify\": true")
		}
		b.WriteString("\n  }")
	}
	if merged.Transmission != nil && strings.TrimSpace(merged.Transmission.URL) != "" {
		b.WriteString(",\n\n")
		b.WriteString("  // Optional: register seeded torrents with Transmission for persistent internet seeding.\n")
		b.WriteString("  // Set enabled=true to activate. Transmission must run with RPC enabled.\n")
		b.WriteString("  // Failure here never fails a publish.\n")
		b.WriteString("  \"transmission\": {\n")
		b.WriteString(fmt.Sprintf("    \"enabled\": %t,\n", merged.Transmission.Enabled))
		b.WriteString(fmt.Sprintf("    \"url\": %s", jsonQuote(strings.TrimSpace(merged.Transmission.URL))))
		if merged.Transmission.Username != "" {
			b.WriteString(fmt.Sprintf(",\n    \"username\": %s", jsonQuote(merged.Transmission.Username)))
		}
		if merged.Transmission.SkipTLSVerify {
			b.WriteString(",\n    \"skip_tls_verify\": true")
		}
		b.WriteString("\n  }")
	}

	b.WriteString("\n}\n")
	return []byte(b.String())
}

func jsonQuote(v string) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func stripJSONComments(src []byte) []byte {
	if len(src) == 0 {
		return src
	}
	out := bytes.NewBuffer(make([]byte, 0, len(src)))
	inString := false
	escaped := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(src); i++ {
		c := src[i]
		next := byte(0)
		if i+1 < len(src) {
			next = src[i+1]
		}

		if inLineComment {
			if c == '\n' {
				inLineComment = false
				out.WriteByte(c)
			}
			continue
		}
		if inBlockComment {
			if c == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}

		if inString {
			out.WriteByte(c)
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}

		if c == '"' {
			inString = true
			out.WriteByte(c)
			continue
		}
		if c == '/' && next == '/' {
			inLineComment = true
			i++
			continue
		}
		if c == '/' && next == '*' {
			inBlockComment = true
			i++
			continue
		}

		out.WriteByte(c)
	}

	return out.Bytes()
}

func StreamingHashEnabled() (bool, error) {
	if raw, ok := os.LookupEnv("ENABLE_STREAMING_HASH"); ok {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return false, fmt.Errorf("parse ENABLE_STREAMING_HASH: %w", err)
		}
		return value, nil
	}

	cfg, err := Load()
	if err != nil {
		return false, err
	}
	return cfg.StreamingHash, nil
}

func LMStudioModelsDir() (string, error) {
	if env := strings.TrimSpace(os.Getenv("LMSTUDIO_MODELS_DIR")); env != "" {
		return env, nil
	}

	cfg, err := Load()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(cfg.LMStudioModels), nil
}

func OllamaModelsDir() (string, error) {
	if env := strings.TrimSpace(os.Getenv("OLLAMA_MODELS")); env != "" {
		return env, nil
	}

	if env := strings.TrimSpace(os.Getenv("OLLAMA_HOME")); env != "" {
		return env, nil
	}

	cfg, err := Load()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(cfg.OllamaModelsDir), nil
}

// DaemonListenAddr returns the listen address for daemon HTTP on port 47433.
// Sharing is a core feature, so this is fixed to 0.0.0.0.
func DaemonListenAddr() (string, error) {
	if _, err := LoadService(); err != nil {
		return "", err
	}
	return "0.0.0.0", nil
}

// ServiceDataDir returns the daemon state root — always the current user's ~/.hali.
func ServiceDataDir() string {
	if d := strings.TrimSpace(os.Getenv("HALI_SERVICE_DATA_DIR")); d != "" {
		return d
	}
	home, err := userHomeDir()
	if err != nil {
		return DataDir()
	}
	return filepath.Join(home, ".hali")
}

// ServiceLogDir returns the daemon log root — always ~/.hali/logs.
func ServiceLogDir() string {
	if d := strings.TrimSpace(os.Getenv("HALI_SERVICE_LOG_DIR")); d != "" {
		return d
	}
	return filepath.Join(ServiceDataDir(), "logs")
}

// IPCSocketPath returns the Unix domain socket path used for IPC on Linux/macOS.
func IPCSocketPath() string {
	return filepath.Join(ServiceDataDir(), "hali.sock")
}

// IPCSecretPath returns the path to the shared-secret file used for IPC auth on Windows.
func IPCSecretPath() string {
	return filepath.Join(DataDir(), "ipc.secret")
}

// ServiceModelsDir returns the model cache root for service-managed runtime.
func ServiceModelsDir() string {
	return filepath.Join(ServiceDataDir(), "models")
}

// ModelsDir returns the directory where downloaded models are stored.
// Resolution order: HALI_MODELS_DIR env var → BT_MODELS_DIR (deprecated fallback) → config file models_dir → DataDir()/models.
func ModelsDir() (string, error) {
	if env := strings.TrimSpace(os.Getenv("HALI_MODELS_DIR")); env != "" {
		return env, nil
	}
	if env := strings.TrimSpace(os.Getenv("BT_MODELS_DIR")); env != "" {
		return env, nil
	}

	cfg, err := Load()
	if err != nil {
		return "", err
	}
	if d := strings.TrimSpace(cfg.ModelsDir); d != "" {
		return d, nil
	}
	return filepath.Join(DataDir(), "models"), nil
}
