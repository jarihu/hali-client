package protocol

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var (
	rNamespace = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	rName      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	rSHA       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	rVersion   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,80}$`)
)

// HaliURL is the parsed and validated form of a hali:// protocol URL.
type HaliURL struct {
	Action    string // always "model" for now
	Namespace string // e.g., "Qwen"
	Name      string // e.g., "Qwen3-32B"
	Version   string // "latest", a 40-char hex SHA, or a short tag
	File      string // optional GGUF filename/path within the repo
}

// RepositoryID returns the neutral repository identifier (namespace/name).
// Named without HF coupling so alternate backends can be added later.
func (h *HaliURL) RepositoryID() string { return h.Namespace + "/" + h.Name }

// Revision maps the version field to a VCS revision string.
// "latest" and "" both map to "main". Specific SHAs pass through unchanged.
// NOTE: "main" is not pinned to a commit — registry pinning is a Phase 2 concern.
func (h *HaliURL) Revision() string {
	if h.Version == "" || h.Version == "latest" {
		return "main"
	}
	return h.Version
}

// Parse validates and parses a hali:// URL. All browser input is treated as untrusted.
func Parse(raw string) (*HaliURL, error) {
	if len(raw) > 2048 {
		return nil, fmt.Errorf("URL too long (max 2048 chars)")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("malformed URL: %w", err)
	}

	if strings.ToLower(u.Scheme) != "hali" {
		return nil, fmt.Errorf("unsupported scheme %q: expected hali://", u.Scheme)
	}

	action := strings.ToLower(u.Host)
	if action != "model" {
		return nil, fmt.Errorf("unsupported action %q: only hali://model/ is supported", u.Host)
	}

	// Decode percent-encoding so that encoded traversal sequences (e.g. %2e%2e, %2F)
	// are visible before we split the path.
	decoded, err := url.PathUnescape(u.Path)
	if err != nil {
		return nil, fmt.Errorf("invalid percent-encoding in path: %w", err)
	}

	// Reject '..' segments before path.Clean collapses them silently.
	for _, seg := range strings.Split(decoded, "/") {
		if seg == ".." {
			return nil, fmt.Errorf("path traversal not allowed")
		}
	}

	clean := path.Clean(decoded)
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid path %q: expected hali://model/<namespace>/<name>", u.Path)
	}
	namespace, name := parts[0], parts[1]
	if namespace == "" {
		return nil, fmt.Errorf("empty namespace in path")
	}
	if name == "" {
		return nil, fmt.Errorf("empty model name in path")
	}

	if !rNamespace.MatchString(namespace) {
		return nil, fmt.Errorf("invalid namespace %q: only letters, digits, dots, hyphens, underscores allowed", namespace)
	}
	if !rName.MatchString(name) {
		return nil, fmt.Errorf("invalid model name %q: only letters, digits, dots, hyphens, underscores allowed", name)
	}

	// Use ParseQuery directly so that query parameters containing ';' cause an
	// error rather than being silently dropped by URL.Query().
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid query string: %w", err)
	}
	version := q.Get("version")
	if version != "" && version != "latest" {
		if !rSHA.MatchString(version) && !rVersion.MatchString(version) {
			return nil, fmt.Errorf("invalid version %q", version)
		}
	}
	file, err := sanitizeRequestedFile(q.Get("file"))
	if err != nil {
		return nil, err
	}

	return &HaliURL{
		Action:    action,
		Namespace: namespace,
		Name:      name,
		Version:   version,
		File:      file,
	}, nil
}

func sanitizeRequestedFile(raw string) (string, error) {
	file := strings.TrimSpace(raw)
	if file == "" {
		return "", nil
	}
	if len(file) > 512 {
		return "", fmt.Errorf("invalid file parameter: too long")
	}
	if strings.Contains(file, "\\") {
		return "", fmt.Errorf("invalid file parameter: backslashes are not allowed")
	}
	if strings.ContainsAny(file, "\r\n\t") {
		return "", fmt.Errorf("invalid file parameter: contains control characters")
	}
	if strings.HasPrefix(file, "/") {
		return "", fmt.Errorf("invalid file parameter: absolute paths are not allowed")
	}
	for _, seg := range strings.Split(file, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("invalid file parameter: path traversal not allowed")
		}
	}
	return file, nil
}
