package lmstudio

import (
	"encoding/json"
	"errors"
	"fmt"
	exportcore "hali/internal/export"
	"hali/internal/runtime"
	"os"
	"path/filepath"
	"strings"
)

var ErrNoChange = errors.New("lmstudio export already up to date")

type Adapter struct{}

func NewExporter() *Adapter {
	return &Adapter{}
}

func (a *Adapter) Name() string {
	return "lmstudio"
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
		return fmt.Errorf("lmstudio export only supports gguf models (got %s)", got)
	}

	root, err := rt.ModelsPath()
	if err != nil {
		return err
	}
	modelDir := filepath.Join(root, exportcore.SanitizeModelID(m.ID))
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		return err
	}

	dstGGUF := filepath.Join(modelDir, "model.gguf")
	dstMeta := filepath.Join(modelDir, "metadata.json")

	revision := exportcore.MetadataString(m.Metadata, "revision")
	if revision == "" {
		revision = exportcore.MetadataString(m.Metadata, "hf_revision")
	}

	meta := map[string]any{
		"source":   "hali",
		"model_id": m.ID,
		"revision": revision,
		"format":   "gguf",
		"path":     dstGGUF,
	}

	if isUpToDate(m.GGUFPath, dstGGUF, dstMeta, meta) {
		return ErrNoChange
	}

	if err := linkOrCopyFileAtomic(m.GGUFPath, dstGGUF); err != nil {
		return err
	}
	return writeJSONAtomic(dstMeta, meta)
}

func ExportPath(modelID string) (string, error) {
	root, err := runtime.LMStudioRuntime{}.ModelsPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, exportcore.SanitizeModelID(modelID)), nil
}

func isUpToDate(src, dstGGUF, dstMeta string, meta map[string]any) bool {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return false
	}
	dstInfo, err := os.Stat(dstGGUF)
	if err != nil {
		return false
	}
	if srcInfo.Size() != dstInfo.Size() {
		return false
	}
	data, err := os.ReadFile(dstMeta)
	if err != nil {
		return false
	}
	existing := map[string]any{}
	if err := json.Unmarshal(data, &existing); err != nil {
		return false
	}
	return exportcore.MetadataString(existing, "model_id") == exportcore.MetadataString(meta, "model_id") &&
		exportcore.MetadataString(existing, "revision") == exportcore.MetadataString(meta, "revision") &&
		exportcore.MetadataString(existing, "format") == exportcore.MetadataString(meta, "format") &&
		exportcore.MetadataString(existing, "path") == exportcore.MetadataString(meta, "path")
}

func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if _, err := out.ReadFrom(in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func linkOrCopyFileAtomic(src, dst string) error {
	if err := symlinkFileAtomic(src, dst); err == nil {
		return nil
	}
	return copyFileAtomic(src, dst)
}

func symlinkFileAtomic(src, dst string) error {
	tmp := dst + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(src, tmp); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func writeJSONAtomic(path string, payload map[string]any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
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
