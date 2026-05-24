package ollama

import (
	"encoding/json"
	"errors"
	"fmt"
	exportcore "hali/internal/export"
	"hali/internal/model"
	"hali/internal/runtime"
	"os"
	"path/filepath"
	"strings"
)

var ErrNoChange = errors.New("ollama manifest already up to date")

type Adapter struct{}

func NewExporter() *Adapter {
	return &Adapter{}
}

func (a *Adapter) Name() string {
	return "ollama"
}

func (a *Adapter) Supports(m exportcore.Model) bool {
	if strings.EqualFold(m.Format(), "gguf") {
		return true
	}
	return strings.EqualFold(filepath.Ext(m.GGUFPath), ".gguf")
}

func (a *Adapter) Export(m exportcore.Model, rt runtime.Runtime) error {
	if !a.Supports(m) {
		got := m.Format()
		if strings.TrimSpace(got) == "" {
			got = "unknown"
		}
		return fmt.Errorf("ollama export only supports gguf models (got %s)", got)
	}

	root, err := rt.ModelsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "manifests"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "blobs"), 0755); err != nil {
		return err
	}

	revision := exportcore.MetadataString(m.Metadata, "revision")
	if revision == "" {
		revision = exportcore.MetadataString(m.Metadata, "hf_revision")
	}

	out := manifest{
		Name:   m.ID,
		Format: "gguf",
		Files: []manifestFile{
			{Path: m.GGUFPath, Type: "model"},
		},
		Metadata: manifestMetadata{
			Source:   "hali",
			Revision: revision,
			ModelID:  m.ID,
		},
	}

	manifestPath := filepath.Join(root, "manifests", exportcore.SanitizeModelID(m.ID)+".json")
	if same, err := isSameManifest(manifestPath, out); err == nil && same {
		return ErrNoChange
	}

	return writeManifestAtomic(manifestPath, out)
}

type manifestFile struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type manifestMetadata struct {
	Source   string `json:"source"`
	Revision string `json:"revision"`
	ModelID  string `json:"model_id"`
}

type manifest struct {
	Name     string           `json:"name"`
	Format   string           `json:"format"`
	Files    []manifestFile   `json:"files"`
	Metadata manifestMetadata `json:"metadata"`
}

type modelMetadata struct {
	ModelID    string   `json:"model_id"`
	Revision   string   `json:"revision"`
	HFRevision string   `json:"hf_revision"`
	Format     string   `json:"format"`
	Files      []string `json:"files"`
}

func ExportModel(modelID string) error {
	m, err := exportcore.ResolveModel(modelID)
	if err != nil {
		return err
	}
	return NewExporter().Export(m, runtime.OllamaRuntime{})
}

func ManifestPath(modelID string) (string, error) {
	if _, err := model.Parse(modelID); err != nil {
		return "", err
	}
	root, err := ollamaRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "manifests", exportcore.SanitizeModelID(modelID)+".json"), nil
}

func ollamaRoot() (string, error) {
	return runtime.OllamaRuntime{}.ModelsPath()
}

func readModelMetadata(modelDir string) (modelMetadata, error) {
	var out modelMetadata
	metaPath := filepath.Join(modelDir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	if strings.TrimSpace(out.ModelID) != "" {
		if _, err := model.Parse(out.ModelID); err != nil {
			return out, fmt.Errorf("invalid metadata model_id %q: %w", out.ModelID, err)
		}
	}
	return out, nil
}

func hasGGUF(files []string) bool {
	for _, f := range files {
		if strings.EqualFold(filepath.Ext(f), ".gguf") {
			return true
		}
	}
	return false
}

func isSameManifest(path string, want manifest) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var existing manifest
	if err := json.Unmarshal(data, &existing); err != nil {
		return false, err
	}
	if len(existing.Files) == 0 || len(want.Files) == 0 {
		return false, nil
	}
	return existing.Name == want.Name &&
		existing.Format == want.Format &&
		existing.Files[0].Path == want.Files[0].Path &&
		existing.Files[0].Type == want.Files[0].Type &&
		existing.Metadata.Source == want.Metadata.Source &&
		existing.Metadata.Revision == want.Metadata.Revision &&
		existing.Metadata.ModelID == want.Metadata.ModelID, nil
}

func writeManifestAtomic(path string, m manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
