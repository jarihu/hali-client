package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hali/internal/config"
)

func TestRunConfigSetRejectsNetworkMode(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	if err := runConfigSet(nil, []string{"network.mode", "lan_only"}); err == nil {
		t.Fatal("runConfigSet expected error for unsupported key network.mode")
	}
}

func TestRunConfigSetRejectsUnknownKey(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	if err := runConfigSet(nil, []string{"network.foo", "bar"}); err == nil {
		t.Fatal("runConfigSet expected error for unknown key")
	}
}

func TestRunConfigSetRejectsDaemonListenAddr(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	if err := runConfigSet(nil, []string{"daemon_listen_addr", "0.0.0.0"}); err == nil {
		t.Fatal("runConfigSet expected error for unsupported key daemon_listen_addr")
	}
}

func TestRunConfigSetRejectsTelemetryIngestURL(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	if err := runConfigSet(nil, []string{"telemetry.ingest_url", "https://telemetry.example.test/v1/ingest"}); err == nil {
		t.Fatal("runConfigSet expected error for unsupported key telemetry.ingest_url")
	}
}

func TestRunConfigSetTelemetryEnabled(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	if err := runConfigSet(nil, []string{"telemetry.enabled", "false"}); err != nil {
		t.Fatalf("runConfigSet telemetry.enabled: %v", err)
	}
	cfg, err := config.LoadService()
	if err != nil {
		t.Fatalf("LoadService: %v", err)
	}
	if cfg.TelemetryEnabledValue() {
		t.Fatal("TelemetryEnabledValue() = true, want false")
	}
}

func TestRunConfigSetStreamingHash(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	if err := runConfigSet(nil, []string{"streaming_hash", "true"}); err != nil {
		t.Fatalf("runConfigSet streaming_hash: %v", err)
	}
	cfg, err := config.LoadService()
	if err != nil {
		t.Fatalf("LoadService: %v", err)
	}
	if !cfg.StreamingHash {
		t.Fatal("StreamingHash = false, want true")
	}
}

func TestRunConfigSetDebug(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	if err := runConfigSet(nil, []string{"debug", "true"}); err != nil {
		t.Fatalf("runConfigSet debug: %v", err)
	}
	cfg, err := config.LoadService()
	if err != nil {
		t.Fatalf("LoadService: %v", err)
	}
	if !cfg.DebugValue() {
		t.Fatal("DebugValue() = false, want true")
	}
}

func TestRunConfigSetLANHMACEnabled(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	if err := runConfigSet(nil, []string{"lan.hmac_enabled", "false"}); err != nil {
		t.Fatalf("runConfigSet lan.hmac_enabled: %v", err)
	}
	cfg, err := config.LoadService()
	if err != nil {
		t.Fatalf("LoadService: %v", err)
	}
	if cfg.LANHMACEnabledValue() {
		t.Fatal("LANHMACEnabledValue() = true, want false")
	}
}

func TestRunConfigSetLANHMACSharedSecretRejectsInvalid(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	if err := runConfigSet(nil, []string{"lan.hmac_shared_secret", "bad"}); err == nil {
		t.Fatal("runConfigSet expected error for invalid lan.hmac_shared_secret")
	}
}

func TestRunConfigSetLANHMACSharedSecret(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	secret := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := runConfigSet(nil, []string{"lan.hmac_shared_secret", secret}); err != nil {
		t.Fatalf("runConfigSet lan.hmac_shared_secret: %v", err)
	}
	cfg, err := config.LoadService()
	if err != nil {
		t.Fatalf("LoadService: %v", err)
	}
	if got := cfg.LANHMACSharedSecret; got != secret {
		t.Fatalf("LANHMACSharedSecret = %q, want %q", got, secret)
	}
}

func TestRunConfigSetModelsDirDefault(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", dataDir)
	if err := runConfigSet(nil, []string{"models_dir", "default"}); err != nil {
		t.Fatalf("runConfigSet models_dir default: %v", err)
	}
	cfg, err := config.LoadService()
	if err != nil {
		t.Fatalf("LoadService: %v", err)
	}
	if got := cfg.ModelsDir; got != filepath.Join(dataDir, "models") {
		t.Fatalf("ModelsDir = %q, want %q", got, filepath.Join(dataDir, "models"))
	}
}

func TestRunConfigShowIncludesConfigPath(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", dataDir)

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	runErr := runConfigShow(nil, nil)
	_ = w.Close()
	os.Stdout = orig
	if runErr != nil {
		t.Fatalf("runConfigShow: %v", runErr)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "config path:") {
		t.Fatalf("config show output missing config path: %q", out)
	}
	if !strings.Contains(out, dataDir) {
		t.Fatalf("config show output missing service data dir %q: %q", dataDir, out)
	}
	if !strings.Contains(out, "effective telemetry.ingest_url: "+config.DefaultRegistryIngestURL) {
		t.Fatalf("config show output missing default telemetry ingest url: %q", out)
	}
	if !strings.Contains(out, "models_dir:") {
		t.Fatalf("config show output missing models_dir: %q", out)
	}
	if !strings.Contains(out, "lan.hmac_enabled:") {
		t.Fatalf("config show output missing lan.hmac_enabled: %q", out)
	}
	if !strings.Contains(out, "debug:") {
		t.Fatalf("config show output missing debug: %q", out)
	}
}

func TestRunConfigShowIncludesConfiguredTelemetryDestination(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", dataDir)

	enabled := false
	cfg := config.File{
		TelemetryEnabled:  &enabled,
		RegistryIngestURL: "https://telemetry.example.test/ingest",
	}
	if err := config.SaveService(cfg); err != nil {
		t.Fatalf("SaveService: %v", err)
	}

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	runErr := runConfigShow(nil, nil)
	_ = w.Close()
	os.Stdout = orig
	if runErr != nil {
		t.Fatalf("runConfigShow: %v", runErr)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "effective telemetry.enabled: false") {
		t.Fatalf("config show output missing effective telemetry.enabled=false: %q", out)
	}
	if !strings.Contains(out, "effective telemetry.ingest_url: https://telemetry.example.test/ingest") {
		t.Fatalf("config show output missing configured telemetry ingest url: %q", out)
	}
}
