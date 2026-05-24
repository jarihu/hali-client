package model

import (
	"fmt"
	"strings"
)

// Normalize parses a user-supplied model query that may be a partial canonical ID
// (1–4 colon-separated parts) or an HF repo string (contains "/").
// Returns a ModelID with as many fields populated as the input allows.
// All components are lowercased. Path traversal is always rejected.
//
// Valid forms:
//
//	base                    → {Base}
//	base:size               → {Base, Size}
//	base:size:variant       → {Base, Size, Variant}
//	base:size:variant:quant → full canonical ID (delegates to Parse)
//	org/Repo-GGUF           → best-effort HF repo parsing
func Normalize(input string) (ModelID, error) {
	if input == "" {
		return ModelID{}, fmt.Errorf("empty model ID")
	}
	if strings.Contains(input, "..") {
		return ModelID{}, fmt.Errorf("invalid model ID %q: path traversal not allowed", input)
	}

	lower := strings.ToLower(input)

	if strings.Contains(lower, "/") {
		return normalizeHFRepo(lower, input)
	}

	parts := strings.Split(lower, ":")
	switch len(parts) {
	case 1:
		if !isSafePathComponent(parts[0]) {
			return ModelID{}, fmt.Errorf("invalid model ID %q: invalid base component", input)
		}
		return ModelID{Base: parts[0]}, nil
	case 2:
		id := ModelID{Base: parts[0], Size: parts[1]}
		if !isSafePathComponent(id.Base) || !isSafePathComponent(id.Size) {
			return ModelID{}, fmt.Errorf("invalid model ID %q: invalid component", input)
		}
		return id, nil
	case 3:
		id := ModelID{Base: parts[0], Size: parts[1], Variant: parts[2]}
		if !isSafePathComponent(id.Base) || !isSafePathComponent(id.Size) || !isSafePathComponent(id.Variant) {
			return ModelID{}, fmt.Errorf("invalid model ID %q: invalid component", input)
		}
		return id, nil
	case 4:
		return Parse(lower)
	default:
		return ModelID{}, fmt.Errorf("invalid model ID %q: too many parts (max 4 colon-separated)", input)
	}
}

// normalizeHFRepo extracts a best-effort ModelID from an HF repo identifier.
// Falls back to base+format if no quant can be inferred without a filename.
func normalizeHFRepo(lower, original string) (ModelID, error) {
	id := FromHF(lower, "")
	if !id.IsZero() {
		return id, nil
	}

	repoName := lower
	if idx := strings.Index(repoName, "/"); idx >= 0 {
		repoName = repoName[idx+1:]
	}

	format := ""
	if strings.Contains(repoName, "-gguf") || strings.HasSuffix(repoName, "gguf") {
		format = "gguf"
	}
	repoName = strings.ReplaceAll(repoName, "-gguf", "")
	repoName = strings.Trim(repoName, "-_. ")

	tokens := strings.FieldsFunc(repoName, func(r rune) bool { return r == '-' || r == '_' })
	if len(tokens) == 0 || !isSafePathComponent(tokens[0]) {
		return ModelID{}, fmt.Errorf("invalid model ID %q: cannot extract safe base from HF repo", original)
	}

	return ModelID{Base: tokens[0], Format: format}, nil
}
