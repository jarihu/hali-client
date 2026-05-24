package model

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type ModelID struct {
	Base     string
	Size     string
	Variant  string
	Quant    string
	Format   string
	Revision string
}

func (id ModelID) String() string {
	return fmt.Sprintf("%s:%s:%s:%s", id.Base, id.Size, id.Variant, id.Quant)
}

func (id ModelID) IsZero() bool {
	return id.Base == ""
}

func isSafePathComponent(s string) bool {
	return s != "" && !strings.Contains(s, "/") && !strings.Contains(s, "\\") && s != ".." && !strings.Contains(s, string(filepath.Separator))
}

func (id ModelID) Valid() bool {
	return isSafePathComponent(id.Base) &&
		isSafePathComponent(id.Size) &&
		isSafePathComponent(id.Variant) &&
		isSafePathComponent(id.Quant)
}

// Parse parses a canonical model ID string (base:size:variant:quant).
func Parse(s string) (ModelID, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 4 {
		return ModelID{}, fmt.Errorf("invalid model ID %q: want base:size:variant:quant", s)
	}
	id := ModelID{Base: parts[0], Size: parts[1], Variant: parts[2], Quant: parts[3]}
	if !id.Valid() {
		return ModelID{}, fmt.Errorf("invalid model ID %q: path components must not contain directory separators or ..", s)
	}
	return id, nil
}

// StorePath returns the relative directory path for this model within the cache.
func (id ModelID) StorePath() string {
	return filepath.Join(id.Base, id.Size+"-"+id.Variant, id.Quant)
}

var (
	reSize  = regexp.MustCompile(`(?i)(\d+\.?\d*[bm])`)
	reQuant = regexp.MustCompile(`(?i)(q\d+_[a-z0-9_]+|f16|f32|fp16|fp32|bf16)`)
)

var knownTypes = []string{"instruct", "chat", "code", "it", "sft"}

// FromHF derives a best-effort canonical model ID from an HF repo ID and GGUF filename.
// Returns a zero ID if the filename cannot be reliably parsed.
// Caller should set Revision after receiving the result.
func FromHF(repoID, filename string) ModelID {
	name := strings.ToLower(repoID)
	if idx := strings.Index(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.ReplaceAll(name, "-gguf", "")
	fname := strings.ToLower(filename)

	size := reSize.FindString(fname)
	if size == "" {
		size = reSize.FindString(name)
	}
	quant := reQuant.FindString(fname)

	base := name
	if size != "" {
		if i := strings.Index(base, strings.ToLower(size)); i > 0 {
			base = base[:i]
		}
	}
	base = strings.Trim(base, "-_. ")
	if parts := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' }); len(parts) > 0 {
		base = parts[0]
	}

	variant := "base"
	for _, t := range knownTypes {
		if strings.Contains(name, t) || strings.Contains(fname, t) {
			variant = t
			break
		}
	}

	if base == "" || size == "" || quant == "" {
		return ModelID{}
	}
	return ModelID{
		Base:    base,
		Size:    strings.ToLower(size),
		Variant: variant,
		Quant:   strings.ToLower(quant),
		Format:  "gguf",
	}
}
