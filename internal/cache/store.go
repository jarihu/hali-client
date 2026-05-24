package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"hali/internal/config"
	"hali/internal/model"
	"hali/internal/safepath"
	"hali/internal/torrent"
)

type Metadata struct {
	ModelID      string   `json:"model_id"`
	HFRepo       string   `json:"hf_repo"`
	HFRevision   string   `json:"hf_revision"`
	HFSnapshot   string   `json:"hf_snapshot_hash"`
	Infohash     string   `json:"torrent_infohash,omitempty"`    // v1 — JSON key frozen
	InfohashV2   string   `json:"torrent_infohash_v2,omitempty"` // v2 — new
	IdentitySeal string   `json:"identity_seal"`
	Files        []string `json:"files"`
	Size         int64    `json:"size"`
	DownloadedAt string   `json:"downloaded_at"`
}

type Entry struct {
	ID   model.ModelID
	Meta Metadata
	Dir  string
}

type Store struct {
	Root string
	mu   sync.Mutex
}

type EvictionResult struct {
	EvictedModels int
	BytesFreed    int64
	BytesBefore   int64
	BytesAfter    int64
}

func NewStore() *Store {
	dir, err := config.ModelsDir()
	if err != nil {
		dir = filepath.Join(config.DataDir(), "models")
	}
	return &Store{Root: dir}
}

func NewStoreAt(root string) *Store {
	return &Store{Root: root}
}

func (s *Store) Dir(id model.ModelID) string {
	return filepath.Join(s.Root, id.StorePath())
}

func (s *Store) Has(id model.ModelID) bool {
	dir := s.Dir(id)
	if !safepath.IsUnderRoot(s.Root, dir) {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "metadata.json"))
	return err == nil
}

func (s *Store) Save(id model.ModelID, meta Metadata) error {
	dir := s.Dir(id)
	if !safepath.IsUnderRoot(s.Root, dir) {
		return fmt.Errorf("model path %q escapes cache root", dir)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	meta.ModelID = id.String()
	meta.DownloadedAt = time.Now().UTC().Format(time.RFC3339)
	meta.IdentitySeal = ComputeIdentitySeal(meta)
	if err := ValidateIdentitySeal(meta); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0644)
}

// SetInfohash updates the torrent_infohash field in an existing metadata.json.
// modelDir is the absolute path to the model directory.
// Write is atomic (temp file + rename) to prevent corruption on concurrent calls.
func (s *Store) SetInfohash(modelDir, infohash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	metaPath := filepath.Join(modelDir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}
	meta.Infohash = infohash
	meta.IdentitySeal = ComputeIdentitySeal(meta)
	if err := ValidateIdentitySeal(meta); err != nil {
		return err
	}
	out, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := metaPath + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, metaPath)
}

// SetIdentity updates both infohash fields in an existing metadata.json atomically.
// Never-downgrade: if the file already has V2, it is not overwritten with an empty string.
func (s *Store) SetIdentity(modelDir string, id torrent.TorrentIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	metaPath := filepath.Join(modelDir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}
	if id.InfohashV1 != "" {
		meta.Infohash = id.InfohashV1
	}
	if id.InfohashV2 != "" {
		meta.InfohashV2 = id.InfohashV2
	}
	meta.IdentitySeal = ComputeIdentitySeal(meta)
	if err := ValidateIdentitySeal(meta); err != nil {
		return err
	}
	out, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := metaPath + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, metaPath)
}

func (s *Store) LoadMeta(id model.ModelID) (*Metadata, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir(id), "metadata.json"))
	if err != nil {
		return nil, err
	}
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if err := ValidateIdentitySeal(m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) List() ([]Entry, error) {
	var entries []Entry
	err := filepath.WalkDir(s.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "metadata.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var meta Metadata
		if err := json.Unmarshal(data, &meta); err != nil {
			return fmt.Errorf("invalid metadata %s: %w", path, err)
		}
		if err := ValidateIdentitySeal(meta); err != nil {
			return fmt.Errorf("invalid metadata %s: %w", path, err)
		}
		id, err := model.Parse(meta.ModelID)
		if err != nil {
			return fmt.Errorf("invalid model_id in %s: %w", path, err)
		}
		entries = append(entries, Entry{ID: id, Meta: meta, Dir: filepath.Dir(path)})
		return nil
	})
	return entries, err
}

// EvictLRU trims cache size to maxBytes by removing least-recently-downloaded models first.
// If maxBytes <= 0, no eviction is performed.
func (s *Store) EvictLRU(maxBytes int64) (EvictionResult, error) {
	if maxBytes <= 0 {
		return EvictionResult{}, nil
	}

	entries, err := s.List()
	if err != nil {
		return EvictionResult{}, err
	}

	type candidate struct {
		entry Entry
		when  time.Time
		size  int64
	}

	cands := make([]candidate, 0, len(entries))
	var total int64
	for _, e := range entries {
		when, parseErr := time.Parse(time.RFC3339, e.Meta.DownloadedAt)
		if parseErr != nil {
			when = time.Unix(0, 0)
		}
		size := e.Meta.Size
		if size < 0 {
			size = 0
		}
		total += size
		cands = append(cands, candidate{entry: e, when: when, size: size})
	}

	res := EvictionResult{BytesBefore: total, BytesAfter: total}
	if total <= maxBytes {
		return res, nil
	}

	sort.Slice(cands, func(i, j int) bool {
		if cands[i].when.Equal(cands[j].when) {
			return cands[i].entry.ID.String() < cands[j].entry.ID.String()
		}
		return cands[i].when.Before(cands[j].when)
	})

	for _, c := range cands {
		if res.BytesAfter <= maxBytes {
			break
		}
		if !safepath.IsUnderRoot(s.Root, c.entry.Dir) {
			continue
		}
		if err := os.RemoveAll(c.entry.Dir); err != nil {
			return res, fmt.Errorf("evict %s: %w", c.entry.ID.String(), err)
		}
		res.EvictedModels++
		res.BytesFreed += c.size
		res.BytesAfter -= c.size
		if res.BytesAfter < 0 {
			res.BytesAfter = 0
		}
	}

	return res, nil
}

// FormatSize formats a byte count as a human-readable string.
func FormatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
