package export

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"hali/internal/cache"
	"hali/internal/model"
	"hali/internal/safepath"
)

type Model struct {
	ID       string
	Path     string
	GGUFPath string
	Metadata map[string]any
}

func (m Model) Format() string {
	if v, ok := m.Metadata["format"].(string); ok {
		return strings.ToLower(strings.TrimSpace(v))
	}
	if m.GGUFPath != "" {
		return "gguf"
	}
	return ""
}

func ResolveModel(modelID string) (Model, error) {
	id, err := model.Parse(modelID)
	if err != nil {
		return Model{}, err
	}

	store := cache.NewStore()
	modelDir := store.Dir(id)
	metaPath := filepath.Join(modelDir, "metadata.json")
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Model{}, fmt.Errorf("model not found in hali cache")
		}
		return Model{}, err
	}

	meta := map[string]any{}
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return Model{}, err
	}

	if got := MetadataString(meta, "model_id"); got != "" && got != modelID {
		return Model{}, fmt.Errorf("metadata model_id mismatch: expected %s, got %s", modelID, got)
	}

	ggufPath, err := resolveGGUFPath(modelDir, getStringSlice(meta, "files"))
	if err != nil {
		return Model{}, err
	}

	return Model{
		ID:       modelID,
		Path:     modelDir,
		GGUFPath: ggufPath,
		Metadata: meta,
	}, nil
}

func SanitizeModelID(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	replacer := strings.NewReplacer(":", "_", "/", "_", "\\", "_")
	return replacer.Replace(s)
}

func resolveGGUFPath(modelDir string, files []string) (string, error) {
	absRoot, err := filepath.Abs(modelDir)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if !strings.EqualFold(filepath.Ext(f), ".gguf") {
			continue
		}
		candidate := filepath.Join(modelDir, f)
		// Resolve symlinks and enforce containment to prevent metadata-driven path traversal.
		abs, err := safepath.Canonical(absRoot, candidate)
		if err != nil {
			slog.Debug("export: skipping path outside model dir", "path", candidate, "err", err)
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			return abs, nil
		}
	}

	// Glob fallback is already safe: only matches *.gguf directly under modelDir.
	matches, err := filepath.Glob(filepath.Join(modelDir, "*.gguf"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("model not found in hali cache")
	}
	return filepath.Abs(matches[0])
}

func MetadataString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func getStringSlice(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	anySlice, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(anySlice))
	for _, item := range anySlice {
		s, ok := item.(string)
		if ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
