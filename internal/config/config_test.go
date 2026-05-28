package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadFromFileMissing(t *testing.T) {
	cfg, err := LoadFromFile(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("LoadFromFile missing: %v", err)
	}
	if cfg != (File{}) {
		t.Fatalf("cfg = %#v, want zero config", cfg)
	}
}

func TestLoadFromFileParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"streaming_hash":true,"lmstudio_models_dir":"C:/lmstudio/models","ollama_models_dir":"C:/ollama/models"}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if !cfg.StreamingHash {
		t.Fatal("StreamingHash = false, want true")
	}
	if cfg.LMStudioModels != "C:/lmstudio/models" {
		t.Fatalf("LMStudioModels = %q", cfg.LMStudioModels)
	}
	if cfg.OllamaModelsDir != "C:/ollama/models" {
		t.Fatalf("OllamaModelsDir = %q", cfg.OllamaModelsDir)
	}
}

func TestLoadFromFileParsesJSONCComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
  // sample comment
  "streaming_hash": true,
  /* block comment */
  "registry_ingest_url": "https://ingest.example.test"
}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if !cfg.StreamingHash {
		t.Fatal("StreamingHash = false, want true")
	}
	if got := cfg.RegistryIngestURLValue(); got != "https://ingest.example.test" {
		t.Fatalf("RegistryIngestURLValue() = %q", got)
	}
}

func TestStreamingHashEnvOverridesConfig(t *testing.T) {
	t.Setenv("ENABLE_STREAMING_HASH", "false")
	t.Setenv("PROGRAMDATA", "") // force home-dir fallback in DataDir()
	home := t.TempDir()
	path := filepath.Join(home, ".hali")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "config.json"), []byte(`{"streaming_hash":true}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = old }()

	enabled, err := StreamingHashEnabled()
	if err != nil {
		t.Fatalf("StreamingHashEnabled: %v", err)
	}
	if enabled {
		t.Fatal("StreamingHashEnabled = true, want false")
	}
}

func TestLMStudioModelsDirFromConfig(t *testing.T) {
	t.Setenv("PROGRAMDATA", "") // force home-dir fallback in DataDir()
	home := t.TempDir()
	path := filepath.Join(home, ".hali")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "config.json"), []byte(`{"lmstudio_models_dir":"/tmp/lmstudio"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = old }()

	got, err := LMStudioModelsDir()
	if err != nil {
		t.Fatalf("LMStudioModelsDir: %v", err)
	}
	if got != "/tmp/lmstudio" {
		t.Fatalf("LMStudioModelsDir = %q", got)
	}
}

func TestModelsDirDefault(t *testing.T) {
	t.Setenv("HALI_MODELS_DIR", "")
	t.Setenv("BT_MODELS_DIR", "")
	t.Setenv("PROGRAMDATA", "")
	home := t.TempDir()
	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = old }()

	got, err := ModelsDir()
	if err != nil {
		t.Fatalf("ModelsDir: %v", err)
	}
	want := filepath.Join(home, ".hali", "models")
	if got != want {
		t.Fatalf("ModelsDir = %q, want %q", got, want)
	}
}

func TestModelsDirFromConfig(t *testing.T) {
	t.Setenv("HALI_MODELS_DIR", "")
	t.Setenv("BT_MODELS_DIR", "")
	t.Setenv("PROGRAMDATA", "")
	home := t.TempDir()
	path := filepath.Join(home, ".hali")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "config.json"), []byte(`{"models_dir":"/mnt/bigdisk/models"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = old }()

	got, err := ModelsDir()
	if err != nil {
		t.Fatalf("ModelsDir: %v", err)
	}
	if got != "/mnt/bigdisk/models" {
		t.Fatalf("ModelsDir = %q", got)
	}
}

func TestModelsDirEnvOverridesConfig(t *testing.T) {
	t.Setenv("HALI_MODELS_DIR", "/env/models")
	t.Setenv("PROGRAMDATA", "")
	home := t.TempDir()
	path := filepath.Join(home, ".hali")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "config.json"), []byte(`{"models_dir":"/cfg/models"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = old }()

	got, err := ModelsDir()
	if err != nil {
		t.Fatalf("ModelsDir: %v", err)
	}
	if got != "/env/models" {
		t.Fatalf("ModelsDir = %q, want /env/models", got)
	}
}

func TestOllamaModelsDirEnvOverridesConfig(t *testing.T) {
	t.Setenv("OLLAMA_HOME", "/env/ollama")
	t.Setenv("OLLAMA_MODELS", "")
	t.Setenv("PROGRAMDATA", "")
	home := t.TempDir()
	path := filepath.Join(home, ".hali")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "config.json"), []byte(`{"ollama_models_dir":"/cfg/ollama"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = old }()

	got, err := OllamaModelsDir()
	if err != nil {
		t.Fatalf("OllamaModelsDir: %v", err)
	}
	if got != "/env/ollama" {
		t.Fatalf("OllamaModelsDir = %q", got)
	}
}

func TestOllamaModelsDirPrefersOllamaModelsEnv(t *testing.T) {
	t.Setenv("OLLAMA_MODELS", "/env/models")
	t.Setenv("OLLAMA_HOME", "/env/home")
	t.Setenv("PROGRAMDATA", "")
	home := t.TempDir()
	path := filepath.Join(home, ".hali")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "config.json"), []byte(`{"ollama_models_dir":"/cfg/ollama"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = old }()

	got, err := OllamaModelsDir()
	if err != nil {
		t.Fatalf("OllamaModelsDir: %v", err)
	}
	if got != "/env/models" {
		t.Fatalf("OllamaModelsDir = %q, want /env/models", got)
	}
}

func TestServiceDirEnvOverrides(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", "/tmp/hali-data")
	t.Setenv("HALI_SERVICE_LOG_DIR", "/tmp/hali-log")

	if got := ServiceDataDir(); got != "/tmp/hali-data" {
		t.Fatalf("ServiceDataDir = %q", got)
	}
	if got := ServiceLogDir(); got != "/tmp/hali-log" {
		t.Fatalf("ServiceLogDir = %q", got)
	}
}

func TestTorrentMetaTimeoutDefault(t *testing.T) {
	f := File{}
	if got := f.TorrentMetaTimeout(); got != DefaultTorrentMetaTimeout {
		t.Errorf("TorrentMetaTimeout() = %v, want %v", got, DefaultTorrentMetaTimeout)
	}
}

func TestTorrentMetaTimeoutCustom(t *testing.T) {
	f := File{TorrentMetaTimeoutMs: 30000}
	if got := f.TorrentMetaTimeout(); got != 30*time.Second {
		t.Errorf("TorrentMetaTimeout() = %v, want 30s", got)
	}
}

func TestIPCDeadlineDefault(t *testing.T) {
	f := File{}
	if got := f.IPCDeadline(); got != DefaultIPCDeadline {
		t.Errorf("IPCDeadline() = %v, want %v", got, DefaultIPCDeadline)
	}
}

func TestShouldMaterializeUserConfigLinuxServiceAware(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only behavior")
	}

	root := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", filepath.Join(root, "service"))
	t.Setenv("HALI_SERVICE_RUN_DIR", filepath.Join(root, "run"))

	// No service config and no socket: should materialize user config.
	if !shouldMaterializeUserConfig() {
		t.Fatal("shouldMaterializeUserConfig() = false, want true when no service state exists")
	}

	// Service config exists: should skip user config materialization.
	serviceCfg := filepath.Join(ServiceDataDir(), "config.json")
	if err := os.MkdirAll(filepath.Dir(serviceCfg), 0o755); err != nil {
		t.Fatalf("mkdir service dir: %v", err)
	}
	if err := os.WriteFile(serviceCfg, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write service config: %v", err)
	}
	if shouldMaterializeUserConfig() {
		t.Fatal("shouldMaterializeUserConfig() = true, want false when service config exists")
	}
}

func TestIPCDeadlineCustom(t *testing.T) {
	f := File{IPCDeadlineMs: 15000}
	if got := f.IPCDeadline(); got != 15*time.Second {
		t.Errorf("IPCDeadline() = %v, want 15s", got)
	}
}

func TestLaunchTimeoutDefault(t *testing.T) {
	f := File{}
	if got := f.LaunchTimeout(); got != DefaultLaunchTimeout {
		t.Errorf("LaunchTimeout() = %v, want %v", got, DefaultLaunchTimeout)
	}
}

func TestLaunchTimeoutCustom(t *testing.T) {
	f := File{LaunchTimeoutMs: 10000}
	if got := f.LaunchTimeout(); got != 10*time.Second {
		t.Errorf("LaunchTimeout() = %v, want 10s", got)
	}
}

func TestSettingsFieldsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"max_upload_mbps":1,"max_download_mbps":2,"pull_concurrency":3}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if cfg.MaxUploadMBps != 1 {
		t.Errorf("MaxUploadMBps = %d, want 1", cfg.MaxUploadMBps)
	}
	if cfg.MaxDownloadMBps != 2 {
		t.Errorf("MaxDownloadMBps = %d, want 2", cfg.MaxDownloadMBps)
	}
	if cfg.PullConcurrency != 3 {
		t.Errorf("PullConcurrency = %d, want 3", cfg.PullConcurrency)
	}
	if cfg.MaxUploadKBpsValue() != 1024 {
		t.Errorf("MaxUploadKBpsValue() = %d, want 1024", cfg.MaxUploadKBpsValue())
	}
	if cfg.MaxDownloadKBpsValue() != 2048 {
		t.Errorf("MaxDownloadKBpsValue() = %d, want 2048", cfg.MaxDownloadKBpsValue())
	}
}

func TestPullConcurrencyValueDefaultsToOne(t *testing.T) {
	if got := (File{}).PullConcurrencyValue(); got != DefaultPullConcurrency {
		t.Fatalf("PullConcurrencyValue() = %d, want %d", got, DefaultPullConcurrency)
	}
}

func TestPullConcurrencyValueClampsToMax(t *testing.T) {
	cfg := File{PullConcurrency: MaxPullConcurrency + 100}
	if got := cfg.PullConcurrencyValue(); got != MaxPullConcurrency {
		t.Fatalf("PullConcurrencyValue() = %d, want %d", got, MaxPullConcurrency)
	}
}

func TestTelemetryEnabledDefault(t *testing.T) {
	cfg := File{}
	if !cfg.TelemetryEnabledValue() {
		t.Fatal("TelemetryEnabledValue() = false, want true")
	}
}

func TestTelemetryEnabledConfiguredFalse(t *testing.T) {
	enabled := false
	cfg := File{TelemetryEnabled: &enabled}
	if cfg.TelemetryEnabledValue() {
		t.Fatal("TelemetryEnabledValue() = true, want false")
	}
}

func TestRegistryIngestURLDefault(t *testing.T) {
	cfg := File{}
	if got := cfg.RegistryIngestURLValue(); got != DefaultRegistryIngestURL {
		t.Fatalf("RegistryIngestURLValue() = %q, want %q", got, DefaultRegistryIngestURL)
	}
}

func TestRegistryIngestURLConfigured(t *testing.T) {
	cfg := File{RegistryIngestURL: " https://example.invalid/api "}
	if got := cfg.RegistryIngestURLValue(); got != "https://example.invalid/api" {
		t.Fatalf("RegistryIngestURLValue() = %q", got)
	}
}

func TestSaveServiceMaterializesDefaultRegistryIngestURL(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", dataDir)

	if err := SaveService(File{}); err != nil {
		t.Fatalf("SaveService: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	text := string(data)
	if !strings.Contains(text, "//") {
		t.Fatalf("expected JSONC comments in saved config: %s", text)
	}
	if !strings.Contains(text, `"registry_ingest_url": "`+DefaultRegistryIngestURL+`"`) {
		t.Fatalf("registry_ingest_url missing/incorrect in saved config: %s", text)
	}
	if !strings.Contains(text, `"telemetry_enabled": true`) {
		t.Fatalf("telemetry_enabled should be explicit true in saved config: %s", text)
	}
	if !strings.Contains(text, `"debug": false`) {
		t.Fatalf("debug should be explicit false in saved config: %s", text)
	}
	if !strings.Contains(text, `"lan_hmac_enabled": false`) {
		t.Fatalf("lan_hmac_enabled should be explicit false in saved config: %s", text)
	}
}

func TestEnsureServiceConfigMaterializedCreatesVisibleConfig(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", dataDir)

	if err := EnsureServiceConfigMaterialized(); err != nil {
		t.Fatalf("EnsureServiceConfigMaterialized: %v", err)
	}

	path := filepath.Join(dataDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"telemetry_enabled": true`) {
		t.Fatalf("telemetry_enabled missing/incorrect in config: %s", text)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if got := cfg.ModelsDir; got != filepath.Join(dataDir, "models") {
		t.Fatalf("ModelsDir = %q, want %q", got, filepath.Join(dataDir, "models"))
	}
}

func TestPortsAreFixedContract(t *testing.T) {
	if IPCPort != 47432 {
		t.Fatalf("IPCPort = %d, want 47432", IPCPort)
	}
	if HTTPPort != 47433 {
		t.Fatalf("HTTPPort = %d, want 47433", HTTPPort)
	}
}

func TestQBittorrentRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
  "qbittorrent": {
	"enabled": true,
    "url": "http://localhost:8080",
    "username": "admin",
    "password": "secret123",
    "category": "hali",
    "tags": ["hali", "models"],
    "skip_tls_verify": true
  }
}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if cfg.QBittorrent == nil {
		t.Fatal("QBittorrent is nil after load")
	}
	if cfg.QBittorrent.URL != "http://localhost:8080" {
		t.Errorf("URL = %q", cfg.QBittorrent.URL)
	}
	if !cfg.QBittorrent.Enabled {
		t.Error("Enabled = false, want true")
	}
	if cfg.QBittorrent.Username != "admin" {
		t.Errorf("Username = %q", cfg.QBittorrent.Username)
	}
	if cfg.QBittorrent.Password != "secret123" {
		t.Errorf("Password = %q", cfg.QBittorrent.Password)
	}
	if cfg.QBittorrent.Category != "hali" {
		t.Errorf("Category = %q", cfg.QBittorrent.Category)
	}
	if len(cfg.QBittorrent.Tags) != 2 {
		t.Errorf("Tags = %v", cfg.QBittorrent.Tags)
	}
	if !cfg.QBittorrent.SkipTLSVerify {
		t.Error("SkipTLSVerify = false, want true")
	}
}

func TestQBittorrentEnabledFalseNil(t *testing.T) {
	var f File
	if f.QBittorrentEnabled() {
		t.Error("QBittorrentEnabled() = true for nil QBittorrent, want false")
	}
}

func TestQBittorrentEnabledFalseEmptyURL(t *testing.T) {
	f := File{QBittorrent: &QBittorrentConfig{Enabled: true, URL: "   "}}
	if f.QBittorrentEnabled() {
		t.Error("QBittorrentEnabled() = true for empty URL, want false")
	}
}

func TestQBittorrentEnabledFalseWhenDisabled(t *testing.T) {
	f := File{QBittorrent: &QBittorrentConfig{Enabled: false, URL: "http://localhost:8080"}}
	if f.QBittorrentEnabled() {
		t.Error("QBittorrentEnabled() = true when enabled=false, want false")
	}
}

func TestQBittorrentEnabledTrue(t *testing.T) {
	f := File{QBittorrent: &QBittorrentConfig{Enabled: true, URL: "http://localhost:8080"}}
	if !f.QBittorrentEnabled() {
		t.Error("QBittorrentEnabled() = false, want true")
	}
}

func TestTransmissionEnabledFalseNil(t *testing.T) {
	var f File
	if f.TransmissionEnabled() {
		t.Error("TransmissionEnabled() = true for nil Transmission, want false")
	}
}

func TestTransmissionEnabledFalseEmptyURL(t *testing.T) {
	f := File{Transmission: &TransmissionConfig{Enabled: true, URL: "   "}}
	if f.TransmissionEnabled() {
		t.Error("TransmissionEnabled() = true for empty URL, want false")
	}
}

func TestTransmissionEnabledFalseWhenDisabled(t *testing.T) {
	f := File{Transmission: &TransmissionConfig{Enabled: false, URL: "http://localhost:9091"}}
	if f.TransmissionEnabled() {
		t.Error("TransmissionEnabled() = true when enabled=false, want false")
	}
}

func TestTransmissionEnabledTrue(t *testing.T) {
	f := File{Transmission: &TransmissionConfig{Enabled: true, URL: "http://localhost:9091"}}
	if !f.TransmissionEnabled() {
		t.Error("TransmissionEnabled() = false, want true")
	}
}

func TestRenderOmitsQBittorrentWhenNil(t *testing.T) {
	rendered := string(renderConfigJSONC(File{}, t.TempDir()))
	if strings.Contains(rendered, "qbittorrent") {
		t.Error("rendered config contains 'qbittorrent' when QBittorrent is nil")
	}
}

func TestRenderIncludesQBittorrentBlock(t *testing.T) {
	f := File{QBittorrent: &QBittorrentConfig{
		Enabled:  true,
		URL:      "http://seedbox.example.com:8080",
		Username: "myuser",
		Password: "supersecret",
		Category: "hali",
	}}
	rendered := string(renderConfigJSONC(f, t.TempDir()))
	if !strings.Contains(rendered, `"qbittorrent"`) {
		t.Error("rendered config missing 'qbittorrent' block")
	}
	if !strings.Contains(rendered, "seedbox.example.com") {
		t.Error("rendered config missing URL")
	}
	if !strings.Contains(rendered, `"enabled": true`) {
		t.Error("rendered config missing enabled=true")
	}
}

func TestRenderPasswordNeverEmitted(t *testing.T) {
	const secret = "supersecretpassword42"
	f := File{QBittorrent: &QBittorrentConfig{
		Enabled:  true,
		URL:      "http://localhost:8080",
		Username: "admin",
		Password: secret,
	}}
	rendered := string(renderConfigJSONC(f, t.TempDir()))
	if strings.Contains(rendered, secret) {
		t.Error("rendered config must not contain the password value")
	}
}
