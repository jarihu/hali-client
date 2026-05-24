package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLMStudioModelsPathFromSettingsFindsDownloadsFolder(t *testing.T) {
	tmp := t.TempDir()
	invalid := filepath.Join(tmp, "invalid.json")
	valid := filepath.Join(tmp, "settings.json")

	if err := os.WriteFile(invalid, []byte("{"), 0644); err != nil {
		t.Fatalf("WriteFile invalid: %v", err)
	}
	if err := os.WriteFile(valid, []byte(`{"downloadsFolder":"D:/AI/lmstudio/models"}`), 0644); err != nil {
		t.Fatalf("WriteFile valid: %v", err)
	}

	got := lmstudioModelsPathFromSettings([]string{invalid, valid})
	if got != filepath.Clean("D:/AI/lmstudio/models") {
		t.Fatalf("lmstudioModelsPathFromSettings = %q", got)
	}
}

func TestLMStudioModelsPathFromSettingsReturnsEmptyWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	empty := filepath.Join(tmp, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"downloadsFolder":""}`), 0644); err != nil {
		t.Fatalf("WriteFile empty: %v", err)
	}

	got := lmstudioModelsPathFromSettings([]string{filepath.Join(tmp, "missing.json"), empty})
	if got != "" {
		t.Fatalf("lmstudioModelsPathFromSettings = %q, want empty", got)
	}
}
