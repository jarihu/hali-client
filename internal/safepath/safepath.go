package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// windowsReserved lists device names that cannot be used as filenames on Windows,
// regardless of extension.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com0": true, "com1": true, "com2": true, "com3": true,
	"com4": true, "com5": true, "com6": true, "com7": true,
	"com8": true, "com9": true, "lpt0": true, "lpt1": true,
	"lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// IsSafeFilename returns true iff name is a single path component with no
// traversal sequences, control characters, or Windows reserved device names.
// Uses explicit character checks — filepath.Base is not a security primitive.
func IsSafeFilename(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	if len(name) > 255 {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r == 0 || r < 32 {
			return false
		}
	}
	if strings.Contains(name, "..") {
		return false
	}
	if !utf8.ValidString(name) {
		return false
	}
	base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	return !windowsReserved[base]
}

// IsValidInfohash returns true iff s is exactly 40 ASCII hex characters.
// Tight loop — no allocation, safe in hot LAN path.
func IsValidInfohash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < 40; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// IsValidInfohashV2 returns true iff s is exactly 64 ASCII hex characters.
// Tight loop — no allocation, safe in hot LAN path.
func IsValidInfohashV2(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < 64; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// IsUnderRoot reports whether path is contained within root after resolving "..".
// Does NOT resolve symlinks — call Canonical first when the path already exists on disk.
func IsUnderRoot(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	if resolvedRoot, resolveErr := filepath.EvalSymlinks(absRoot); resolveErr == nil {
		absRoot = resolvedRoot
	}
	if resolvedPath, resolveErr := filepath.EvalSymlinks(absPath); resolveErr == nil {
		absPath = resolvedPath
	}

	absRoot = filepath.Clean(absRoot)
	absPath = filepath.Clean(absPath)

	if runtime.GOOS == "windows" {
		absRoot = strings.ToLower(absRoot)
		absPath = strings.ToLower(absPath)
	}

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	if rel == ".." {
		return false
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// Canonical resolves all symlinks in path and verifies the result is still
// under root. Call before any file read or write to prevent symlink-escape attacks.
//
// For paths that do not yet exist (new writes), symlinks in the parent
// directory are resolved and the final component is appended unresolved.
func Canonical(root, path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("canonical: abs %q: %w", path, err)
	}

	// Resolve symlinks on the nearest existing ancestor so we can safely
	// support new paths whose parent chain does not exist yet.
	anchor := absPath
	missing := make([]string, 0, 4)
	for {
		if _, statErr := os.Lstat(anchor); statErr == nil {
			break
		} else if os.IsNotExist(statErr) {
			parent := filepath.Dir(anchor)
			if parent == anchor {
				return "", fmt.Errorf("canonical: no existing ancestor for %q", path)
			}
			missing = append(missing, filepath.Base(anchor))
			anchor = parent
			continue
		} else {
			return "", fmt.Errorf("canonical: lstat %q: %w", anchor, statErr)
		}
	}

	resolved, err := filepath.EvalSymlinks(anchor)
	if err != nil {
		return "", fmt.Errorf("canonical: resolve ancestor %q: %w", anchor, err)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, missing[i])
	}

	// Resolve symlinks on root too so that comparisons work correctly even
	// when root contains Windows 8.3 short names or other symlink forms.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("canonical: resolve root %q: %w", root, err)
	}
	if !IsUnderRoot(resolvedRoot, resolved) {
		return "", fmt.Errorf("path %q escapes root %q after symlink resolution", resolved, root)
	}
	return resolved, nil
}
