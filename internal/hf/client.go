package hf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	baseURL = "https://huggingface.co"
	apiURL  = "https://huggingface.co/api"

	hfAPIMaxAttempts = 4
	hfAPIBaseDelay   = 250 * time.Millisecond
	hfAPIMaxDelay    = 2 * time.Second
)

type SearchResult struct {
	ID        string   `json:"id"`
	Downloads int      `json:"downloads"`
	Tags      []string `json:"tags"`
}

type File struct {
	Name string `json:"rfilename"`
	Size int64  `json:"size"`
}

type apiFile struct {
	Name string `json:"rfilename"`
	Size int64  `json:"size"`
	LFS  *struct {
		Size int64 `json:"size"`
	} `json:"lfs"`
}

type treeFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		Size int64 `json:"size"`
	} `json:"lfs"`
}

type Client struct {
	http    *http.Client
	apiBase string
	dlBase  string
}

func NewClient() *Client {
	return &Client{
		http:    &http.Client{Timeout: 30 * time.Second},
		apiBase: strings.TrimRight(apiURL, "/"),
		dlBase:  strings.TrimRight(baseURL, "/"),
	}
}

func (c *Client) Search(ctx context.Context, query string) ([]SearchResult, error) {
	u := fmt.Sprintf("%s/models?search=%s&filter=gguf&sort=downloads&direction=-1&limit=20",
		c.apiBase, url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.doAPIRequestWithRetry(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HF API returned %s", resp.Status)
	}
	var results []SearchResult
	return results, json.NewDecoder(resp.Body).Decode(&results)
}

// GetFiles returns the GGUF files in a repo, sorted smallest-first, plus the resolved revision SHA.
// If revision is non-empty and not "main", the HF API is queried at that specific revision.
func (c *Client) GetFiles(ctx context.Context, repoID, revision string) ([]File, string, error) {
	parts := strings.Split(repoID, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	repoPath := strings.Join(parts, "/")
	u := fmt.Sprintf("%s/models/%s", c.apiBase, repoPath)
	if revision != "" && revision != "main" {
		u += "?revision=" + url.QueryEscape(revision)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.doAPIRequestWithRetry(ctx, req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", fmt.Errorf("model not found: %s", repoID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HF API returned %s", resp.Status)
	}
	var m struct {
		SHA      string    `json:"sha"`
		Siblings []apiFile `json:"siblings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, "", err
	}

	var gguf []File
	needTreeSizes := false
	for _, f := range m.Siblings {
		if strings.HasSuffix(strings.ToLower(f.Name), ".gguf") {
			size := f.Size
			if size <= 0 && f.LFS != nil {
				size = f.LFS.Size
			}
			if size <= 0 {
				needTreeSizes = true
			}
			gguf = append(gguf, File{Name: f.Name, Size: size})
		}
	}

	if needTreeSizes {
		sizeByName, err := c.getTreeGGUFSizes(ctx, repoPath, revision)
		if err == nil {
			for i := range gguf {
				if gguf[i].Size <= 0 {
					if size, ok := sizeByName[gguf[i].Name]; ok && size > 0 {
						gguf[i].Size = size
					}
				}
			}
		}
	}

	sort.Slice(gguf, func(i, j int) bool {
		iKnown := gguf[i].Size > 0
		jKnown := gguf[j].Size > 0
		if iKnown != jKnown {
			return iKnown
		}
		if iKnown {
			return gguf[i].Size < gguf[j].Size
		}
		return strings.ToLower(gguf[i].Name) < strings.ToLower(gguf[j].Name)
	})
	resolved := m.SHA
	if revision != "" {
		resolved = revision
	}
	return gguf, resolved, nil
}

func (c *Client) getTreeGGUFSizes(ctx context.Context, repoPath, revision string) (map[string]int64, error) {
	rev := revision
	if rev == "" || rev == "main" {
		rev = "main"
	}
	u := fmt.Sprintf("%s/models/%s/tree/%s?recursive=1", c.apiBase, repoPath, url.PathEscape(rev))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.doAPIRequestWithRetry(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HF API tree returned %s", resp.Status)
	}

	var tree []treeFile
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return nil, err
	}

	sizeByName := make(map[string]int64, len(tree))
	for _, f := range tree {
		if !strings.HasSuffix(strings.ToLower(f.Path), ".gguf") {
			continue
		}
		size := f.Size
		if size <= 0 && f.LFS != nil {
			size = f.LFS.Size
		}
		if size <= 0 {
			continue
		}
		sizeByName[filepath.Base(f.Path)] = size
	}
	return sizeByName, nil
}

type Progress struct {
	Filename string
	Total    int64
	Written  int64
	Rate     float64 // bytes per second
	Resumed  bool
	ResumeAt int64
}

var (
	downloadIdleTimeout       = 2 * time.Minute
	downloadIdleCheckInterval = 5 * time.Second
)

// Download streams a file from HF to destDir, calling progress on each chunk.
// Uses a .tmp file and renames on success to prevent partial writes.
// If a .tmp file already exists, resumes with an HTTP Range request.
// If tee is non-nil, all bytes are also written to it (for streaming piece hashing).
// Retries transient errors up to 3 times with exponential backoff.
// revision is the HF revision (branch, tag, or commit SHA); "" defaults to "main".
func (c *Client) Download(ctx context.Context, repoID, filename, revision, destDir string, progress func(Progress), tee io.Writer) (int64, error) {
	const maxRetries = 3
	baseDelay := 5 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		n, err := c.downloadOnce(ctx, repoID, filename, revision, destDir, progress, tee)
		if err == nil {
			return n, nil
		}
		if !isRetryable(err) || attempt == maxRetries {
			return 0, err
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
		slog.Info("hf download retry", "attempt", attempt+1, "delay", delay, "file", filename, "err", err)
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(delay):
		}
	}
	return 0, fmt.Errorf("unreachable")
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "tls:") ||
		strings.Contains(msg, "no such host")
}

func (c *Client) downloadOnce(ctx context.Context, repoID, filename, revision, destDir string, progress func(Progress), tee io.Writer) (int64, error) {
	rev := revision
	if rev == "" {
		rev = "main"
	}
	dlURL := fmt.Sprintf("%s/%s/resolve/%s/%s", c.dlBase, repoID, url.PathEscape(rev), url.PathEscape(filename))

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return 0, err
	}

	safeFilename := filepath.Base(filename)
	tmpPath := filepath.Join(destDir, safeFilename+".tmp")
	destPath := filepath.Join(destDir, safeFilename)

	// Check for an existing partial download to resume.
	var resumeAt int64
	if st, statErr := os.Stat(tmpPath); statErr == nil && st.Size() > 0 {
		resumeAt = st.Size()
	}

	reqCtx, cancelReq := context.WithCancel(ctx)
	defer cancelReq()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, dlURL, nil)
	if err != nil {
		return 0, err
	}
	if resumeAt > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeAt))
	}

	// No timeout — large models can take hours.
	dlClient := &http.Client{}
	resp, err := dlClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	// Accept 200 (full download), 206 (partial / resume), and 416 (range not satisfiable: file already complete).
	switch resp.StatusCode {
	case http.StatusOK:
		if resumeAt > 0 {
			// Server ignored the Range header; discard partial file and start over.
			os.Remove(tmpPath)
			resumeAt = 0
		}
	case http.StatusPartialContent:
		// Resume continuation — append to existing .tmp file.
	case http.StatusRequestedRangeNotSatisfiable:
		if err := hashResumedPrefix(tmpPath, resumeAt, tee); err != nil {
			return 0, err
		}
		// Already fully downloaded — rename .tmp to final destination.
		if err := os.Rename(tmpPath, destPath); err != nil {
			os.Remove(tmpPath)
			return 0, fmt.Errorf("file already complete but rename failed: %w", err)
		}
		return resumeAt, nil
	case http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return 0, &retryableError{msg: fmt.Sprintf("download returned %s", resp.Status)}
	default:
		return 0, fmt.Errorf("download returned %s", resp.Status)
	}

	totalSize := resp.ContentLength
	if totalSize > 0 && resumeAt > 0 {
		totalSize += resumeAt // Content-Length on 206 is just the remaining bytes.
	}
	if err := hashResumedPrefix(tmpPath, resumeAt, tee); err != nil {
		return 0, err
	}

	var f *os.File
	if resumeAt > 0 {
		f, err = os.OpenFile(tmpPath, os.O_APPEND|os.O_WRONLY, 0644)
	} else {
		f, err = os.Create(tmpPath)
	}
	if err != nil {
		return 0, err
	}

	pr := &progressReader{
		r:        resp.Body,
		total:    totalSize,
		read:     resumeAt, // start progress at the resumed position
		resumeAt: resumeAt,
		filename: filename,
		notify:   progress,
		start:    time.Now(),
	}
	watchdogTriggered := atomic.Bool{}
	watchdogStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(downloadIdleCheckInterval)
		defer ticker.Stop()
		lastWritten := pr.currentRead()
		lastAdvance := time.Now()
		for {
			select {
			case <-watchdogStop:
				return
			case <-reqCtx.Done():
				return
			case <-ticker.C:
				current := pr.currentRead()
				if current > lastWritten {
					lastWritten = current
					lastAdvance = time.Now()
					continue
				}
				if time.Since(lastAdvance) > downloadIdleTimeout {
					watchdogTriggered.Store(true)
					cancelReq()
					return
				}
			}
		}
	}()
	var dst io.Writer = f
	if tee != nil {
		dst = io.MultiWriter(f, tee)
	}
	written, copyErr := io.Copy(dst, pr)
	close(watchdogStop)
	f.Close()

	if copyErr != nil {
		if watchdogTriggered.Load() {
			os.Remove(tmpPath)
			return 0, &retryableError{msg: "download stalled with no progress"}
		}
		os.Remove(tmpPath)
		return 0, copyErr
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return 0, err
	}
	return written + resumeAt, nil
}

type retryableError struct {
	msg string
}

func (e *retryableError) Error() string {
	return e.msg
}

func hashResumedPrefix(tmpPath string, resumeAt int64, tee io.Writer) error {
	if tee == nil || resumeAt <= 0 {
		return nil
	}
	f, err := os.Open(tmpPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.CopyN(tee, f, resumeAt); err != nil {
		return err
	}
	return nil
}

func (c *Client) doAPIRequestWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < hfAPIMaxAttempts; attempt++ {
		attemptReq := req.Clone(ctx)
		resp, err := c.http.Do(attemptReq)
		if err == nil {
			if !isRetryableStatus(resp.StatusCode) {
				return resp, nil
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("HF API returned %s", resp.Status)
		} else {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
		}

		if attempt == hfAPIMaxAttempts-1 {
			break
		}
		delay := hfAPIBaseDelay * time.Duration(1<<attempt)
		if delay > hfAPIMaxDelay {
			delay = hfAPIMaxDelay
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("HF API request failed after %d attempts", hfAPIMaxAttempts)
	}
	return nil, lastErr
}

func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

type progressReader struct {
	r          io.Reader
	total      int64
	read       int64
	resumeAt   int64
	filename   string
	notify     func(Progress)
	start      time.Time
	lastNotify time.Time
}

func (pr *progressReader) currentRead() int64 {
	return atomic.LoadInt64(&pr.read)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	current := atomic.AddInt64(&pr.read, int64(n))
	shouldNotify := pr.notify != nil && (time.Since(pr.lastNotify) > 150*time.Millisecond || err == io.EOF)
	if shouldNotify {
		elapsed := time.Since(pr.start).Seconds()
		rate := 0.0
		if elapsed > 0 {
			rate = float64(current-pr.resumeAt) / elapsed
		}
		pr.notify(Progress{
			Filename: pr.filename,
			Total:    pr.total,
			Written:  current,
			Rate:     rate,
			Resumed:  pr.resumeAt > 0,
			ResumeAt: pr.resumeAt,
		})
		pr.lastNotify = time.Now()
	}
	return n, err
}
