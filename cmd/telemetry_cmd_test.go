package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"hali/internal/config"
)

func TestSetTelemetryEnabledPersistsToServiceConfig(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())

	if err := setTelemetryEnabled(false); err != nil {
		t.Fatalf("setTelemetryEnabled(false): %v", err)
	}
	cfg, err := config.LoadService()
	if err != nil {
		t.Fatalf("LoadService: %v", err)
	}
	if cfg.TelemetryEnabled == nil || *cfg.TelemetryEnabled {
		t.Fatalf("TelemetryEnabled = %v, want false", cfg.TelemetryEnabled)
	}

	if err := setTelemetryEnabled(true); err != nil {
		t.Fatalf("setTelemetryEnabled(true): %v", err)
	}
	cfg, err = config.LoadService()
	if err != nil {
		t.Fatalf("LoadService: %v", err)
	}
	if cfg.TelemetryEnabled == nil || !*cfg.TelemetryEnabled {
		t.Fatalf("TelemetryEnabled = %v, want true", cfg.TelemetryEnabled)
	}

	path := filepath.Join(config.ServiceDataDir(), "config.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file at %s: %v", path, err)
	}
}
