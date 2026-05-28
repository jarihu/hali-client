package registry

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// TorrentMetadata is the deterministic metadata object the client uploads.
type TorrentMetadata struct {
	FileName string `json:"file_name"`
	RepoID   string `json:"repo_id"`
	Revision string `json:"revision"`
	FileSize int64  `json:"file_size"`
	Infohash string `json:"infohash"`
}

type RepoBatchIngestRequest struct {
	ModelID         string                `json:"model_id,omitempty"`
	Revision        string                `json:"revision"`
	PublisherPubKey string                `json:"publisher_pubkey"`
	TorrentType     string                `json:"torrent_type,omitempty"`
	Files           []RepoBatchIngestFile `json:"files"`
}

type RepoBatchIngestFile struct {
	Torrent        string   `json:"torrent"`
	TorrentType    string   `json:"torrent_type,omitempty"`
	Files          []string `json:"files,omitempty"`
	Infohash       string   `json:"infohash"`
	Magnet         string   `json:"magnet"`
	SourceURL      string   `json:"source_url"`
	LocalHash      string   `json:"local_hash"`
	ModelSizeBytes int64    `json:"model_size_bytes,omitempty"`
	Quantization   string   `json:"quantization,omitempty"`
	DisplayName    string   `json:"display_name,omitempty"`
	PublisherSig   string   `json:"publisher_sig"`
	Timestamp      string   `json:"timestamp"`
}

// UploadResult reports whether upload was accepted or deduplicated.
type UploadResult struct {
	AlreadyExists bool
}

// BuildDeterministicTorrentMetadata creates a deterministic metadata object.
func BuildDeterministicTorrentMetadata(repoID, revision, fileName string, fileSize int64) TorrentMetadata {
	repoID = strings.TrimSpace(repoID)
	revision = strings.TrimSpace(revision)
	fileName = strings.TrimSpace(fileName)
	if fileSize < 0 {
		fileSize = 0
	}
	canonical := repoID + "\n" + revision + "\n" + fileName + "\n" + fmt.Sprintf("%d", fileSize)
	sum := sha1.Sum([]byte(canonical))
	return TorrentMetadata{
		FileName: fileName,
		RepoID:   repoID,
		Revision: revision,
		FileSize: fileSize,
		Infohash: hex.EncodeToString(sum[:]),
	}
}

// UploadRepoBatchIngest uploads repo metadata entries to /repo/{owner}/{repo}/ingest.
// It performs best-effort dedupe checks and skips already-ingested entries.
func (c *Client) UploadRepoBatchIngest(ctx context.Context, owner, repo string, payload RepoBatchIngestRequest) (UploadResult, error) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return UploadResult{}, fmt.Errorf("owner and repo are required")
	}

	filtered := make([]RepoBatchIngestFile, 0, len(payload.Files))
	for _, f := range payload.Files {
		exists, err := c.IngestExists(ctx, f.Infohash)
		if err != nil {
			return UploadResult{}, err
		}
		if exists {
			continue
		}
		filtered = append(filtered, f)
	}
	if len(filtered) == 0 {
		return UploadResult{AlreadyExists: true}, nil
	}

	payload.Files = filtered
	body, err := json.Marshal(payload)
	if err != nil {
		return UploadResult{}, fmt.Errorf("marshal repo ingest payload: %w", err)
	}

	u := fmt.Sprintf("%s/repo/%s/%s/ingest", strings.TrimRight(c.baseURL, "/"), url.PathEscape(owner), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return UploadResult{}, fmt.Errorf("build repo ingest request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return UploadResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return UploadResult{}, fmt.Errorf("repo ingest returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	if resp.StatusCode == http.StatusMultiStatus {
		var result struct {
			Ingested int `json:"ingested"`
			Failed   int `json:"failed"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.Failed > 0 {
			return UploadResult{}, fmt.Errorf("repo ingest reported %d failed entries", result.Failed)
		}
	}

	return UploadResult{}, nil
}

// IngestExists checks whether an infohash is already ingested.
func (c *Client) IngestExists(ctx context.Context, infohash string) (bool, error) {
	h := strings.ToLower(strings.TrimSpace(infohash))
	if h == "" {
		return false, fmt.Errorf("missing infohash for dedupe check")
	}
	u, err := url.Parse(strings.TrimRight(c.baseURL, "/") + "/ingest/" + h)
	if err != nil {
		return false, fmt.Errorf("parse dedupe url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if err != nil {
		return false, fmt.Errorf("build dedupe request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return false, nil
	default:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, fmt.Errorf("dedupe check returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
}
