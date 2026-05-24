package safepath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsSafeFilename(t *testing.T) {
	bad := []string{
		"",
		".",
		"..",
		"../etc",
		"a/b",
		"a\\b",
		"foo\x00bar",
		"foo\x1f",
		"foo\rbar",
		"NUL",
		"nul",
		"CON",
		"PRN",
		"AUX",
		"COM1",
		"LPT1",
		"COM1.txt",
		"nul.gguf",
		"lpt9.gguf",
		strings.Repeat("a", 256),
		"  ",
		"\t",
	}
	for _, name := range bad {
		if IsSafeFilename(name) {
			t.Errorf("IsSafeFilename(%q) = true, want false", name)
		}
	}

	good := []string{
		"model.gguf",
		"mistral-7b-instruct-q4_k_m.gguf",
		"model.safetensors",
		"with spaces.gguf",
		"über.gguf",
		"COM11.txt",  // COM11 is not reserved (only COM0-COM9)
		"null.gguf",  // "null" != "nul"
		"console.pt", // "console" != "con"
		strings.Repeat("a", 255),
	}
	for _, name := range good {
		if !IsSafeFilename(name) {
			t.Errorf("IsSafeFilename(%q) = false, want true", name)
		}
	}
}

func TestIsValidInfohash(t *testing.T) {
	bad := []string{
		"",
		"abc",
		strings.Repeat("a", 39),
		strings.Repeat("a", 41),
		strings.Repeat("g", 40), // 'g' is not hex
		strings.Repeat("Z", 40), // 'Z' is not hex
		"../etc/shadow_____________________________",        // 40 chars, not hex
		"da39a3ee5e6b4b0d3255bfef95601890afd80709" + "\x00", // NUL appended
	}
	for _, s := range bad {
		if IsValidInfohash(s) {
			t.Errorf("IsValidInfohash(%q) = true, want false", s)
		}
	}

	good := []string{
		"da39a3ee5e6b4b0d3255bfef95601890afd80709",
		"DA39A3EE5E6B4B0D3255BFEF95601890AFD80709",
		"0000000000000000000000000000000000000000",
		"ffffffffffffffffffffffffffffffffffffffff",
		"abcdef0123456789abcdef0123456789abcdef01",
	}
	for _, s := range good {
		if !IsValidInfohash(s) {
			t.Errorf("IsValidInfohash(%q) = false, want true", s)
		}
	}
}

func TestIsValidInfohashV2(t *testing.T) {
	bad := []string{
		"",
		strings.Repeat("a", 63), // too short
		strings.Repeat("a", 65), // too long
		strings.Repeat("g", 64), // 'g' not hex
		strings.Repeat("Z", 64), // 'Z' not hex
		strings.Repeat("a", 40), // v1 length, not v2
		"../etc/shadow" + strings.Repeat("a", 51), // traversal prefix
	}
	for _, s := range bad {
		if IsValidInfohashV2(s) {
			t.Errorf("IsValidInfohashV2(%q) = true, want false", s)
		}
	}

	good := []string{
		strings.Repeat("a", 64),
		strings.Repeat("0", 64),
		strings.Repeat("f", 64),
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855",
	}
	for _, s := range good {
		if !IsValidInfohashV2(s) {
			t.Errorf("IsValidInfohashV2(%q) = false, want true", s)
		}
	}
}

func TestIsUnderRoot(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Dir(root)

	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(root, "subdir", "file.gguf"), true},
		{filepath.Join(root, "file"), true},
		{root, true},
		{filepath.Join(parent, "sibling"), false},
		{filepath.Join(root, "..", "escape"), false},
	}
	for _, tt := range tests {
		got := IsUnderRoot(root, tt.path)
		if got != tt.want {
			t.Errorf("IsUnderRoot(%q, %q) = %v, want %v", root, tt.path, got, tt.want)
		}
	}
}

func TestCanonicalAllowsValidPath(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "model.gguf")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := Canonical(root, f)
	if err != nil {
		t.Fatalf("Canonical rejected valid path: %v", err)
	}
	if !IsUnderRoot(root, got) {
		t.Errorf("Canonical returned path outside root: %q", got)
	}
}

func TestCanonicalRejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Dir(root)

	_, err := Canonical(root, filepath.Join(outside, "evil.gguf"))
	if err == nil {
		t.Error("Canonical should reject path outside root")
	}
}

func TestCanonicalRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink requires elevated privilege on Windows")
	}
	root := t.TempDir()
	target := t.TempDir() // outside root

	link := filepath.Join(root, "escape")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := Canonical(root, link)
	if err == nil {
		t.Error("Canonical should reject symlink pointing outside root")
	}
}

func TestCanonicalNewFilePath(t *testing.T) {
	root := t.TempDir()
	// Path that doesn't exist yet — Canonical should accept it if parent is valid.
	newPath := filepath.Join(root, "new_model.gguf")

	got, err := Canonical(root, newPath)
	if err != nil {
		t.Fatalf("Canonical should accept non-existent path with valid parent: %v", err)
	}
	if !IsUnderRoot(root, got) {
		t.Errorf("Canonical returned path outside root for new file: %q", got)
	}
}

func TestCanonicalNestedNewPath(t *testing.T) {
	root := t.TempDir()
	newPath := filepath.Join(root, "models", "foo", "bar", "model.gguf")

	got, err := Canonical(root, newPath)
	if err != nil {
		t.Fatalf("Canonical should accept nested non-existent path under root: %v", err)
	}
	if !IsUnderRoot(root, got) {
		t.Errorf("Canonical returned path outside root for nested file: %q", got)
	}
}
