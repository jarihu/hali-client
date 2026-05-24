package ollama

import (
	"os"
	"path/filepath"
	"testing"

	exportcore "hali/internal/export"
)

func TestHasGGUF(t *testing.T) {
	tests := []struct {
		name   string
		files  []string
		expect bool
	}{
		{"single gguf", []string{"model.gguf"}, true},
		{"with path", []string{"subdir/model.gguf"}, true},
		{"uppercase", []string{"MODEL.GGUF"}, true},
		{"mixed case", []string{"Model.Gguf"}, true},
		{"no gguf", []string{"model.safetensors", "config.json"}, false},
		{"empty slice", []string{}, false},
		{"nil slice", nil, false},
		{"among non-gguf", []string{"config.json", "model.gguf", "tokenizer.json"}, true},
		{"just extension", []string{".gguf"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasGGUF(tt.files)
			if got != tt.expect {
				t.Errorf("hasGGUF(%v) = %v, want %v", tt.files, got, tt.expect)
			}
		})
	}
}

func TestSupports(t *testing.T) {
	a := Adapter{}
	tests := []struct {
		name   string
		model  exportcore.Model
		expect bool
	}{
		{
			"format gguf in metadata",
			exportcore.Model{Metadata: map[string]any{"format": "gguf"}},
			true,
		},
		{
			"format uppercase in metadata",
			exportcore.Model{Metadata: map[string]any{"format": "GGUF"}},
			true,
		},
		{
			"no format, has .gguf path",
			exportcore.Model{GGUFPath: "/path/to/model.gguf"},
			true,
		},
		{
			"no format, uppercase .GGUF path",
			exportcore.Model{GGUFPath: "/path/to/model.GGUF"},
			true,
		},
		{
			"no format, no gguf path",
			exportcore.Model{Metadata: map[string]any{"format": "safetensors"}, GGUFPath: "/path/to/model.safetensors"},
			false,
		},
		{
			"unsupported format",
			exportcore.Model{Metadata: map[string]any{"format": "safetensors"}},
			false,
		},
		{
			"empty model",
			exportcore.Model{},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := a.Supports(tt.model)
			if got != tt.expect {
				t.Errorf("Supports() = %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestName(t *testing.T) {
	a := Adapter{}
	if name := a.Name(); name != "ollama" {
		t.Errorf("Name() = %q, want ollama", name)
	}
}

func TestMetadataString(t *testing.T) {
	tests := []struct {
		name   string
		meta   map[string]any
		key    string
		expect string
	}{
		{"present", map[string]any{"rev": "abc123"}, "rev", "abc123"},
		{"missing", map[string]any{}, "rev", ""},
		{"wrong type", map[string]any{"rev": 42}, "rev", ""},
		{"whitespace", map[string]any{"rev": "  abc  "}, "rev", "abc"},
		{"nil map", nil, "rev", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exportcore.MetadataString(tt.meta, tt.key)
			if got != tt.expect {
				t.Errorf("MetadataString() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestIsSameManifest(t *testing.T) {
	want := manifest{
		Name:   "mistral:7b:instruct:q4_k_m",
		Format: "gguf",
		Files:  []manifestFile{{Path: "/tmp/model.gguf", Type: "model"}},
		Metadata: manifestMetadata{
			Source:   "hali",
			Revision: "abc123",
			ModelID:  "mistral:7b:instruct:q4_k_m",
		},
	}

	t.Run("file does not exist", func(t *testing.T) {
		same, err := isSameManifest("/nonexistent/path.json", want)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if same {
			t.Error("isSameManifest for nonexistent file should return false")
		}
	})

	t.Run("identical manifest", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.json")
		if err := writeManifestAtomic(path, want); err != nil {
			t.Fatalf("writeManifestAtomic: %v", err)
		}
		same, err := isSameManifest(path, want)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !same {
			t.Error("identical manifests should be same")
		}
	})

	t.Run("different name", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.json")
		if err := writeManifestAtomic(path, want); err != nil {
			t.Fatalf("writeManifestAtomic: %v", err)
		}
		other := want
		other.Name = "other:model:name:here"
		same, err := isSameManifest(path, other)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if same {
			t.Error("manifests with different names should not be same")
		}
	})

	t.Run("different format", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.json")
		if err := writeManifestAtomic(path, want); err != nil {
			t.Fatalf("writeManifestAtomic: %v", err)
		}
		other := want
		other.Format = "safetensors"
		same, err := isSameManifest(path, other)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if same {
			t.Error("manifests with different formats should not be same")
		}
	})

	t.Run("different revision", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.json")
		if err := writeManifestAtomic(path, want); err != nil {
			t.Fatalf("writeManifestAtomic: %v", err)
		}
		other := want
		other.Metadata.Revision = "different"
		same, err := isSameManifest(path, other)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if same {
			t.Error("manifests with different revisions should not be same")
		}
	})

	t.Run("different source", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.json")
		if err := writeManifestAtomic(path, want); err != nil {
			t.Fatalf("writeManifestAtomic: %v", err)
		}
		other := want
		other.Metadata.Source = "other"
		same, err := isSameManifest(path, other)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if same {
			t.Error("manifests with different sources should not be same")
		}
	})

	t.Run("different file path", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.json")
		if err := writeManifestAtomic(path, want); err != nil {
			t.Fatalf("writeManifestAtomic: %v", err)
		}
		other := want
		other.Files[0].Path = "/other/path.gguf"
		same, err := isSameManifest(path, other)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if same {
			t.Error("manifests with different file paths should not be same")
		}
	})

	t.Run("empty files in existing", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.json")
		empty := manifest{Name: "x", Metadata: manifestMetadata{Source: "hali"}}
		if err := writeManifestAtomic(path, empty); err != nil {
			t.Fatalf("writeManifestAtomic: %v", err)
		}
		same, err := isSameManifest(path, want)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if same {
			t.Error("manifest with empty files should not match")
		}
	})
}

func TestWriteManifestAtomic(t *testing.T) {
	m := manifest{
		Name:   "test:model",
		Format: "gguf",
		Files:  []manifestFile{{Path: "/tmp/m.gguf", Type: "model"}},
		Metadata: manifestMetadata{
			Source:   "hali",
			Revision: "rev123",
			ModelID:  "test:model",
		},
	}

	t.Run("new file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.json")
		if err := writeManifestAtomic(path, m); err != nil {
			t.Fatalf("writeManifestAtomic: %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("manifest file not created: %v", err)
		}
		if _, err := os.Stat(path + ".tmp"); err == nil {
			t.Error("tmp file should not exist after atomic write")
		}
	})

	t.Run("overwrite existing", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.json")
		if err := writeManifestAtomic(path, m); err != nil {
			t.Fatalf("first write: %v", err)
		}
		m2 := m
		m2.Metadata.Revision = "newrev"
		if err := writeManifestAtomic(path, m2); err != nil {
			t.Fatalf("second write: %v", err)
		}
		// Verify no stale .tmp
		if _, err := os.Stat(path + ".tmp"); err == nil {
			t.Error("tmp file should not exist after atomic overwrite")
		}
	})

	t.Run("tmp cleaned when path is non-empty dir", func(t *testing.T) {
		dir := t.TempDir()
		subdir := filepath.Join(dir, "sub")
		os.MkdirAll(subdir, 0755)
		path := filepath.Join(subdir, "manifest.json")
		// Make path a non-empty directory so os.Remove(path) fails
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(path, "stale"), []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		err := writeManifestAtomic(path, m)
		if err == nil {
			t.Error("expected error when path is a non-empty directory")
		}
		// tmp file should be cleaned up
		if _, statErr := os.Stat(path + ".tmp"); statErr == nil {
			t.Error("tmp file should not exist after failed atomic write")
		}
	})
}

func TestErrNoChange(t *testing.T) {
	if ErrNoChange.Error() == "" {
		t.Error("ErrNoChange should have a message")
	}
}
