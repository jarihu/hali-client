package lmstudio

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	exportcore "hali/internal/export"
)

func TestName(t *testing.T) {
	a := Adapter{}
	if name := a.Name(); name != "lmstudio" {
		t.Errorf("Name() = %q, want lmstudio", name)
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

func TestCopyFileAtomic(t *testing.T) {
	t.Run("copy small file", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src.txt")
		dst := filepath.Join(dir, "dst.txt")
		content := []byte("hello world")

		if err := os.WriteFile(src, content, 0644); err != nil {
			t.Fatalf("WriteFile src: %v", err)
		}
		if err := copyFileAtomic(src, dst); err != nil {
			t.Fatalf("copyFileAtomic: %v", err)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("ReadFile dst: %v", err)
		}
		if string(got) != string(content) {
			t.Errorf("dst content = %q, want %q", string(got), string(content))
		}
		if _, err := os.Stat(dst + ".tmp"); err == nil {
			t.Error("tmp file should not exist after successful copy")
		}
	})

	t.Run("overwrite existing", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src.txt")
		dst := filepath.Join(dir, "dst.txt")

		if err := os.WriteFile(src, []byte("new content"), 0644); err != nil {
			t.Fatalf("WriteFile src: %v", err)
		}
		if err := os.WriteFile(dst, []byte("old content"), 0644); err != nil {
			t.Fatalf("WriteFile dst: %v", err)
		}
		if err := copyFileAtomic(src, dst); err != nil {
			t.Fatalf("copyFileAtomic: %v", err)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("ReadFile dst: %v", err)
		}
		if string(got) != "new content" {
			t.Errorf("dst content = %q, want new content", string(got))
		}
	})

	t.Run("source does not exist", func(t *testing.T) {
		dir := t.TempDir()
		err := copyFileAtomic(filepath.Join(dir, "nope.txt"), filepath.Join(dir, "dst.txt"))
		if err == nil {
			t.Error("expected error for missing source")
		}
	})
}

func TestLinkOrCopyFileAtomic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.gguf")
	dst := filepath.Join(dir, "dst.gguf")

	if err := os.WriteFile(src, []byte("model-bytes"), 0644); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}
	if err := linkOrCopyFileAtomic(src, dst); err != nil {
		t.Fatalf("linkOrCopyFileAtomic: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(got) != "model-bytes" {
		t.Fatalf("dst content = %q, want model-bytes", string(got))
	}

	if symlinkSupportedForTest(dir) {
		info, err := os.Lstat(dst)
		if err != nil {
			t.Fatalf("Lstat dst: %v", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("expected symlink export when symlinks are supported")
		}
	}
}

func symlinkSupportedForTest(dir string) bool {
	if runtime.GOOS == "windows" {
		// Windows symlink support depends on privilege/developer mode.
		probeTarget := filepath.Join(dir, "probe-target")
		probeLink := filepath.Join(dir, "probe-link")
		if err := os.WriteFile(probeTarget, []byte("x"), 0644); err != nil {
			return false
		}
		if err := os.Symlink(probeTarget, probeLink); err != nil {
			return false
		}
		_ = os.Remove(probeLink)
		return true
	}
	probeTarget := filepath.Join(dir, "probe-target")
	probeLink := filepath.Join(dir, "probe-link")
	if err := os.WriteFile(probeTarget, []byte("x"), 0644); err != nil {
		return false
	}
	if err := os.Symlink(probeTarget, probeLink); err != nil {
		return false
	}
	_ = os.Remove(probeLink)
	return true
}

func TestWriteJSONAtomic(t *testing.T) {
	payload := map[string]any{
		"source":   "hali",
		"model_id": "test:model",
		"revision": "abc",
		"format":   "gguf",
	}

	t.Run("new file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "meta.json")
		if err := writeJSONAtomic(path, payload); err != nil {
			t.Fatalf("writeJSONAtomic: %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file not created: %v", err)
		}
		if _, err := os.Stat(path + ".tmp"); err == nil {
			t.Error("tmp file should not exist after successful write")
		}
	})

	t.Run("overwrite existing", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "meta.json")
		if err := writeJSONAtomic(path, payload); err != nil {
			t.Fatalf("first write: %v", err)
		}
		payload2 := map[string]any{"source": "hali", "revision": "newrev"}
		if err := writeJSONAtomic(path, payload2); err != nil {
			t.Fatalf("second write: %v", err)
		}
		if _, err := os.Stat(path + ".tmp"); err == nil {
			t.Error("tmp file should not exist after atomic overwrite")
		}
	})
}

func TestIsUpToDate(t *testing.T) {
	t.Run("identical files", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src.gguf")
		dst := filepath.Join(dir, "dst.gguf")
		metaPath := filepath.Join(dir, "meta.json")

		os.WriteFile(src, []byte("model data"), 0644)
		os.WriteFile(dst, []byte("model data"), 0644)
		meta := map[string]any{
			"model_id": "test:model",
			"revision": "abc",
			"format":   "gguf",
			"path":     dst,
		}
		writeJSONAtomic(metaPath, meta)

		upToDate := isUpToDate(src, dst, metaPath, meta)
		if !upToDate {
			t.Error("identical files should be up to date")
		}
	})

	t.Run("different sizes", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src.gguf")
		dst := filepath.Join(dir, "dst.gguf")
		metaPath := filepath.Join(dir, "meta.json")

		os.WriteFile(src, []byte("model data longer"), 0644)
		os.WriteFile(dst, []byte("short"), 0644)
		meta := map[string]any{"model_id": "test:model", "revision": "abc", "format": "gguf", "path": dst}
		writeJSONAtomic(metaPath, meta)

		upToDate := isUpToDate(src, dst, metaPath, meta)
		if upToDate {
			t.Error("different sizes should not be up to date")
		}
	})

	t.Run("missing source", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "dst.gguf")
		metaPath := filepath.Join(dir, "meta.json")
		os.WriteFile(dst, []byte("data"), 0644)
		meta := map[string]any{"model_id": "test:model", "revision": "abc", "format": "gguf", "path": dst}
		writeJSONAtomic(metaPath, meta)

		upToDate := isUpToDate("/nonexistent/src.gguf", dst, metaPath, meta)
		if upToDate {
			t.Error("missing source should not be up to date")
		}
	})

	t.Run("missing metadata file", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src.gguf")
		dst := filepath.Join(dir, "dst.gguf")
		os.WriteFile(src, []byte("data"), 0644)
		os.WriteFile(dst, []byte("data"), 0644)
		meta := map[string]any{"model_id": "test:model", "revision": "abc", "format": "gguf", "path": dst}

		upToDate := isUpToDate(src, dst, filepath.Join(dir, "nope.json"), meta)
		if upToDate {
			t.Error("missing metadata should not be up to date")
		}
	})

	t.Run("metadata mismatch", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src.gguf")
		dst := filepath.Join(dir, "dst.gguf")
		metaPath := filepath.Join(dir, "meta.json")

		os.WriteFile(src, []byte("model data"), 0644)
		os.WriteFile(dst, []byte("model data"), 0644)
		oldMeta := map[string]any{"model_id": "old:model", "revision": "old", "format": "gguf", "path": dst}
		writeJSONAtomic(metaPath, oldMeta)
		newMeta := map[string]any{"model_id": "new:model", "revision": "new", "format": "gguf", "path": dst}

		upToDate := isUpToDate(src, dst, metaPath, newMeta)
		if upToDate {
			t.Error("metadata mismatch should not be up to date")
		}
	})
}

func TestErrNoChange(t *testing.T) {
	if ErrNoChange.Error() == "" {
		t.Error("ErrNoChange should have a message")
	}
}
