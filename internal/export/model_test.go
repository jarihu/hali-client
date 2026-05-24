package export

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSanitizeModelID(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"Mistral:7B:Instruct:Q4_K_M", "mistral_7b_instruct_q4_k_m"},
		{"mistral:7b:instruct:q4_k_m", "mistral_7b_instruct_q4_k_m"},
		{"foo/bar:baz:qux", "foo_bar_baz_qux"},
		{`foo\bar:baz`, "foo_bar_baz"},
		{"  spaces:around:stuff:here  ", "spaces_around_stuff_here"},
		{"simple", "simple"},
		{"", ""},
		{"a:b:c:d", "a_b_c_d"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeModelID(tt.input)
			if got != tt.expect {
				t.Errorf("SanitizeModelID(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestModelFormat(t *testing.T) {
	tests := []struct {
		name   string
		model  Model
		expect string
	}{
		{"format in metadata", Model{Metadata: map[string]any{"format": "gguf"}}, "gguf"},
		{"format uppercase", Model{Metadata: map[string]any{"format": "GGUF"}}, "gguf"},
		{"format with spaces", Model{Metadata: map[string]any{"format": "  gguf  "}}, "gguf"},
		{"no format, has GGUF path", Model{GGUFPath: "/path/to/model.gguf"}, "gguf"},
		{"no format, no GGUF path", Model{}, ""},
		{"format wrong type", Model{Metadata: map[string]any{"format": 42}}, ""},
		{"empty metadata", Model{Metadata: map[string]any{}}, ""},
		{"nil metadata", Model{Metadata: nil}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.model.Format()
			if got != tt.expect {
				t.Errorf("Format() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestMetadataString(t *testing.T) {
	tests := []struct {
		name   string
		m      map[string]any
		key    string
		expect string
	}{
		{"key present", map[string]any{"foo": "bar"}, "foo", "bar"},
		{"key missing", map[string]any{"foo": "bar"}, "baz", ""},
		{"nil map", nil, "foo", ""},
		{"key wrong type", map[string]any{"foo": 42}, "foo", ""},
		{"string with spaces", map[string]any{"foo": "  bar  "}, "foo", "bar"},
		{"empty string", map[string]any{"foo": ""}, "foo", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MetadataString(tt.m, tt.key)
			if got != tt.expect {
				t.Errorf("MetadataString(%v, %q) = %q, want %q", tt.m, tt.key, got, tt.expect)
			}
		})
	}
}

func TestGetStringSlice(t *testing.T) {
	tests := []struct {
		name   string
		m      map[string]any
		key    string
		expect []string
	}{
		{
			name:   "present",
			m:      map[string]any{"files": []any{"a.gguf", "b.gguf"}},
			key:    "files",
			expect: []string{"a.gguf", "b.gguf"},
		},
		{
			name:   "missing key",
			m:      map[string]any{},
			key:    "files",
			expect: nil,
		},
		{
			name:   "nil map",
			m:      nil,
			key:    "files",
			expect: nil,
		},
		{
			name:   "wrong type (string)",
			m:      map[string]any{"files": "not a slice"},
			key:    "files",
			expect: nil,
		},
		{
			name:   "wrong type (int)",
			m:      map[string]any{"files": 42},
			key:    "files",
			expect: nil,
		},
		{
			name:   "mixed with non-string items",
			m:      map[string]any{"files": []any{"a.gguf", 42, "b.gguf"}},
			key:    "files",
			expect: []string{"a.gguf", "b.gguf"},
		},
		{
			name:   "empty strings filtered",
			m:      map[string]any{"files": []any{"", "a.gguf", "  "}},
			key:    "files",
			expect: []string{"a.gguf"},
		},
		{
			name:   "all non-strings filtered out",
			m:      map[string]any{"files": []any{1, 2, 3}},
			key:    "files",
			expect: []string{},
		},
		{
			name:   "empty slice",
			m:      map[string]any{"files": []any{}},
			key:    "files",
			expect: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getStringSlice(tt.m, tt.key)
			if len(got) != len(tt.expect) {
				t.Fatalf("getStringSlice() len = %d, want %d (%v vs %v)", len(got), len(tt.expect), got, tt.expect)
			}
			if len(tt.expect) == 0 && got == nil {
				return
			}
			for i := range got {
				if got[i] != tt.expect[i] {
					t.Errorf("getStringSlice()[%d] = %q, want %q", i, got[i], tt.expect[i])
				}
			}
		})
	}
}

func TestResolveModelInvalidID(t *testing.T) {
	_, err := ResolveModel("")
	if err == nil {
		t.Error("ResolveModel(\"\") should return error")
	}

	_, err = ResolveModel("not-a-valid-id")
	if err == nil {
		t.Error("ResolveModel(bad ID) should return error")
	}
}

// TestResolveGGUFPathContainmentViaMetadata verifies that a malicious "files"
// list containing path traversal sequences cannot return a path outside modelDir.
func TestResolveGGUFPathContainmentViaMetadata(t *testing.T) {
	root := t.TempDir()
	outerDir := t.TempDir()
	outerGGUF := filepath.Join(outerDir, "secret.gguf")
	if err := os.WriteFile(outerGGUF, []byte("fake"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Construct a traversal path that tries to escape root via metadata.
	traversal := filepath.Join("..", filepath.Base(outerDir), "secret.gguf")
	got, err := resolveGGUFPath(root, []string{traversal})
	if err == nil && got == outerGGUF {
		t.Errorf("resolveGGUFPath returned path outside model dir via metadata traversal: %q", got)
	}
}

// TestResolveGGUFPathContainmentViaSymlink verifies that a symlink inside
// modelDir pointing outside is rejected by the Canonical check in the files loop.
func TestResolveGGUFPathContainmentViaSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink requires elevated privilege on Windows")
	}
	root := t.TempDir()
	target := t.TempDir()

	outerGGUF := filepath.Join(target, "secret.gguf")
	if err := os.WriteFile(outerGGUF, []byte("fake"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(root, "model.gguf")
	if err := os.Symlink(outerGGUF, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	got, err := resolveGGUFPath(root, []string{"model.gguf"})
	if err == nil && got == outerGGUF {
		t.Errorf("resolveGGUFPath returned resolved symlink target outside model dir: %q", got)
	}
}
