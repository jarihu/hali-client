package hf

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProgressReaderNotifiesEventually(t *testing.T) {
	data := "hello world this is test data for progress"
	reader := strings.NewReader(data)
	var progresses []Progress
	pr := &progressReader{
		r:        reader,
		total:    int64(len(data)),
		filename: "test.gguf",
		notify: func(p Progress) {
			progresses = append(progresses, p)
		},
		start: time.Now(),
	}

	buf := make([]byte, 5)
	for {
		_, err := pr.Read(buf)
		if err != nil {
			break
		}
		time.Sleep(160 * time.Millisecond)
	}

	if len(progresses) == 0 {
		t.Error("expected at least one progress callback")
	}
	if progresses[0].Filename != "test.gguf" {
		t.Errorf("Filename = %q, want test.gguf", progresses[0].Filename)
	}
	if progresses[0].Total != int64(len(data)) {
		t.Errorf("Total = %d, want %d", progresses[0].Total, len(data))
	}
	last := progresses[len(progresses)-1]
	if last.Written != int64(len(data)) {
		t.Errorf("last Written = %d, want %d", last.Written, len(data))
	}
}

func TestProgressReaderRatePositive(t *testing.T) {
	data := "hello world"
	reader := strings.NewReader(data)
	var lastRate float64
	pr := &progressReader{
		r:        reader,
		total:    int64(len(data)),
		filename: "test.gguf",
		notify: func(p Progress) {
			lastRate = p.Rate
		},
		start: time.Now(),
	}
	buf := make([]byte, 1)
	for {
		_, err := pr.Read(buf)
		if err != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if lastRate <= 0 {
		t.Errorf("Rate should be positive after slow reads, got %f", lastRate)
	}
}

func TestProgressReaderNilNotify(t *testing.T) {
	data := "hello"
	reader := strings.NewReader(data)
	pr := &progressReader{
		r:        reader,
		total:    int64(len(data)),
		filename: "test.gguf",
		notify:   nil,
		start:    time.Now(),
	}
	buf := make([]byte, 10)
	n, err := pr.Read(buf)
	if n != len(data) {
		t.Errorf("Read returned %d bytes, want %d", n, len(data))
	}
	if err != nil && err != io.EOF {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestProgressReaderThrottle(t *testing.T) {
	data := "hello world test data"
	reader := strings.NewReader(data)
	var calls int
	pr := &progressReader{
		r:        reader,
		total:    int64(len(data)),
		filename: "test.gguf",
		notify: func(p Progress) {
			calls++
		},
		start: time.Now(),
	}
	buf := make([]byte, 1)
	for {
		_, err := pr.Read(buf)
		if err != nil {
			break
		}
	}
	if calls > 2 {
		t.Errorf("fast reads should produce at most 2 notifications (initial + forced EOF), got %d", calls)
	}
}

func TestProgressReaderForcesFinalNotifyOnEOF(t *testing.T) {
	data := "hello world test data"
	reader := strings.NewReader(data)
	var last Progress
	var calls int
	pr := &progressReader{
		r:        reader,
		total:    int64(len(data)),
		filename: "test.gguf",
		notify: func(p Progress) {
			calls++
			last = p
		},
		start: time.Now(),
	}

	buf := make([]byte, 1)
	for {
		_, err := pr.Read(buf)
		if err != nil {
			if err != io.EOF {
				t.Fatalf("Read() error = %v, want io.EOF", err)
			}
			break
		}
	}

	if calls < 2 {
		t.Fatalf("expected at least 2 notifications (initial + final EOF), got %d", calls)
	}
	if last.Written != int64(len(data)) {
		t.Fatalf("last Written = %d, want %d", last.Written, len(data))
	}
	if last.Total != int64(len(data)) {
		t.Fatalf("last Total = %d, want %d", last.Total, len(data))
	}
}

func TestSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "search=test") {
			t.Errorf("expected search=test in query, got %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]SearchResult{
			{ID: "org/repo", Downloads: 1000, Tags: []string{"gguf", "llm"}},
			{ID: "org/repo2", Downloads: 500, Tags: []string{"gguf"}},
		})
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	results, err := c.Search(context.Background(), "test")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].ID != "org/repo" {
		t.Errorf("results[0].ID = %q, want org/repo", results[0].ID)
	}
	if results[1].Downloads != 500 {
		t.Errorf("results[1].Downloads = %d, want 500", results[1].Downloads)
	}
}

func TestSearchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	_, err := c.Search(context.Background(), "test")
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestSearchRetriesTransientStatusThenSucceeds(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]SearchResult{{ID: "org/repo", Downloads: 1}})
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	results, err := c.Search(context.Background(), "test")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestSearchStopsAfterHardAttemptLimit(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	_, err := c.Search(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for persistent 503 responses")
	}
	if got := atomic.LoadInt32(&attempts); got != hfAPIMaxAttempts {
		t.Fatalf("attempts = %d, want %d", got, hfAPIMaxAttempts)
	}
}

func TestGetFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"sha": "abc123def",
			"siblings": []map[string]any{
				{"rfilename": "large-model.Q4_K_M.gguf", "size": 1000},
				{"rfilename": "small-model.Q4_0.gguf", "size": 100},
				{"rfilename": "config.json", "size": 50},
				{"rfilename": "MODEL.GGUF", "size": 200},
			},
		})
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	files, sha, err := c.GetFiles(context.Background(), "org/repo", "")
	if err != nil {
		t.Fatalf("GetFiles: %v", err)
	}
	if sha != "abc123def" {
		t.Errorf("sha = %q, want abc123def", sha)
	}
	if len(files) != 3 {
		t.Fatalf("len(files) = %d, want 3 (only .gguf files)", len(files))
	}
	if files[0].Name != "small-model.Q4_0.gguf" {
		t.Errorf("files[0].Name = %q, want small-model.Q4_0.gguf", files[0].Name)
	}
	if files[1].Name != "MODEL.GGUF" {
		t.Errorf("files[1].Name = %q, want MODEL.GGUF", files[1].Name)
	}
	if files[2].Name != "large-model.Q4_K_M.gguf" {
		t.Errorf("files[2].Name = %q, want large-model.Q4_K_M.gguf", files[2].Name)
	}
}

func TestGetFilesNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	_, _, err := c.GetFiles(context.Background(), "nonexistent/repo", "")
	if err == nil {
		t.Error("expected error for 404")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found: %v", err)
	}
}

func TestGetFilesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	_, _, err := c.GetFiles(context.Background(), "org/repo", "")
	if err == nil {
		t.Error("expected error for 500")
	}
}

func TestGetFilesFallsBackToTreeSizes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/org/repo":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"sha": "abc123def",
				"siblings": []map[string]any{
					{"rfilename": "big.gguf"},
					{"rfilename": "small.gguf"},
					{"rfilename": "README.md"},
				},
			})
		case "/models/org/repo/tree/main":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]any{
				{"path": "big.gguf", "size": 2000},
				{"path": "small.gguf", "size": 1000},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	files, sha, err := c.GetFiles(context.Background(), "org/repo", "")
	if err != nil {
		t.Fatalf("GetFiles: %v", err)
	}
	if sha != "abc123def" {
		t.Errorf("sha = %q, want abc123def", sha)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
	if files[0].Name != "small.gguf" || files[0].Size != 1000 {
		t.Errorf("files[0] = %+v, want {Name:small.gguf Size:1000}", files[0])
	}
	if files[1].Name != "big.gguf" || files[1].Size != 2000 {
		t.Errorf("files[1] = %+v, want {Name:big.gguf Size:2000}", files[1])
	}
}

func TestDownloadSuccess(t *testing.T) {
	content := []byte("this is a test file content for download")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()
	c := &Client{dlBase: server.URL}

	var calls int
	pcb := func(p Progress) { calls++ }

	written, err := c.Download(context.Background(), "org/repo", "model.gguf", "", dir, pcb, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if written == 0 {
		t.Error("written should be > 0")
	}
	if calls == 0 {
		t.Error("progress callback never called")
	}
	destFile := filepath.Join(dir, "model.gguf")
	if _, err := os.Stat(destFile); err != nil {
		t.Errorf("destination file not found: %v", err)
	}
	if _, err := os.Stat(destFile + ".tmp"); err == nil {
		t.Error("tmp file should not exist after successful download")
	}
}

func TestDownloadErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := &Client{dlBase: server.URL}
	_, err := c.Download(context.Background(), "org/repo", "model.gguf", "", t.TempDir(), nil, nil)
	if err == nil {
		t.Error("expected error for non-200 status")
	}
}

func TestDownloadOnceStalledTransferTriggersRetryableError(t *testing.T) {
	prevTimeout := downloadIdleTimeout
	prevInterval := downloadIdleCheckInterval
	downloadIdleTimeout = 80 * time.Millisecond
	downloadIdleCheckInterval = 20 * time.Millisecond
	t.Cleanup(func() {
		downloadIdleTimeout = prevTimeout
		downloadIdleCheckInterval = prevInterval
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("start"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(400 * time.Millisecond)
	}))
	defer server.Close()

	c := &Client{dlBase: server.URL}
	_, err := c.downloadOnce(context.Background(), "org/repo", "model.gguf", "", t.TempDir(), nil, nil)
	if err == nil {
		t.Fatal("expected stalled-transfer error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "stalled") {
		t.Fatalf("expected stalled error, got: %v", err)
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient()
	if c.http == nil {
		t.Fatal("http client is nil")
	}
	if c.http.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", c.http.Timeout)
	}
	if c.apiBase != apiURL {
		t.Errorf("apiBase = %q, want %q", c.apiBase, apiURL)
	}
	if c.dlBase != baseURL {
		t.Errorf("dlBase = %q, want %q", c.dlBase, baseURL)
	}
}

func TestFileStruct(t *testing.T) {
	f := File{Name: "model.gguf", Size: 1234}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got File
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got != f {
		t.Errorf("round-trip mismatch: %+v vs %+v", got, f)
	}
}

func TestSearchResultStruct(t *testing.T) {
	sr := SearchResult{ID: "org/repo", Downloads: 999, Tags: []string{"gguf"}}
	data, err := json.Marshal(sr)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got SearchResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got.ID != sr.ID || got.Downloads != sr.Downloads {
		t.Errorf("round-trip mismatch: %+v vs %+v", got, sr)
	}
}

func TestProgressStruct(t *testing.T) {
	p := Progress{Filename: "f.gguf", Total: 100, Written: 50, Rate: 1024.5}
	// Verify fields are correct
	if p.Filename != "f.gguf" {
		t.Errorf("Filename = %q", p.Filename)
	}
	if p.Total != 100 {
		t.Errorf("Total = %d", p.Total)
	}
	if p.Rate != 1024.5 {
		t.Errorf("Rate = %f", p.Rate)
	}
}

func TestProgressReaderNoThrottleFirstCall(t *testing.T) {
	data := "test data"
	reader := strings.NewReader(data)
	var called bool
	pr := &progressReader{
		r:        reader,
		total:    int64(len(data)),
		filename: "test.gguf",
		notify: func(p Progress) {
			called = true
		},
		start: time.Now(),
	}
	buf := make([]byte, len(data))
	pr.Read(buf)
	if !called {
		t.Error("first read should always trigger notify (lastNotify is zero value)")
	}
}

func TestProgressReaderZeroTotal(t *testing.T) {
	data := "data"
	reader := strings.NewReader(data)
	pr := &progressReader{
		r:        reader,
		total:    0,
		filename: "unknown.gguf",
		notify:   func(p Progress) {},
		start:    time.Now(),
	}
	buf := make([]byte, len(data))
	n, err := pr.Read(buf)
	if n != len(data) {
		t.Errorf("Read returned %d, want %d", n, len(data))
	}
	if err != nil && err != io.EOF {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetFilesEmptyRepo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"sha":      "abcdef",
			"siblings": []map[string]any{},
		})
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	files, sha, err := c.GetFiles(context.Background(), "empty/repo", "")
	if err != nil {
		t.Fatalf("GetFiles: %v", err)
	}
	if sha != "abcdef" {
		t.Errorf("sha = %q, want %q", sha, "abcdef")
	}
	if len(files) != 0 {
		t.Errorf("len(files) = %d, want 0", len(files))
	}
}

func TestGetFilesOnlyNonGGUF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"sha": "abc",
			"siblings": []map[string]any{
				{"rfilename": "config.json", "size": 100},
				{"rfilename": "tokenizer.json", "size": 200},
			},
		})
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	files, _, err := c.GetFiles(context.Background(), "no-gguf/repo", "")
	if err != nil {
		t.Fatalf("GetFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("len(files) = %d, want 0 (no gguf files)", len(files))
	}
}

// Test that progressReader passes through read errors from underlying reader
type errorReader struct{}

func (errorReader) Read(p []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestProgressReaderErrorPassthrough(t *testing.T) {
	pr := &progressReader{
		r:        errorReader{},
		total:    100,
		filename: "test.gguf",
		notify:   func(p Progress) {},
		start:    time.Now(),
	}
	buf := make([]byte, 10)
	_, err := pr.Read(buf)
	if err != io.ErrUnexpectedEOF {
		t.Errorf("expected io.ErrUnexpectedEOF, got %v", err)
	}
}

// Regression: bytes.Reader always returns (n, nil) until exhausted.
// progressReader must not drop bytes.
func TestProgressReaderDoesNotDropBytes(t *testing.T) {
	content := []byte("abcdefghij")
	r := bytes.NewReader(content)
	var written int64
	pr := &progressReader{
		r:        r,
		total:    int64(len(content)),
		filename: "test.gguf",
		notify: func(p Progress) {
			written = p.Written
		},
		start: time.Now(),
	}
	var out bytes.Buffer
	_, err := io.Copy(&out, pr)
	if err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	if !bytes.Equal(out.Bytes(), content) {
		t.Errorf("output mismatch: got %q, want %q", out.String(), string(content))
	}
	if written != int64(len(content)) {
		t.Errorf("final written = %d, want %d", written, len(content))
	}
}

func TestSearchRateLimitReturnsError(t *testing.T) {
	// 429 from the HF API must be returned as an error, not panic or hang.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	_, err := c.Search(context.Background(), "llama")
	if err == nil {
		t.Error("Search with 429 response should return an error")
	}
}

func TestGetFilesRateLimitReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	c := &Client{http: server.Client(), apiBase: server.URL}
	_, _, err := c.GetFiles(context.Background(), "org/repo", "")
	if err == nil {
		t.Error("GetFiles with 429 response should return an error")
	}
}

func TestDownloadRateLimitReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	c := &Client{dlBase: server.URL}
	_, err := c.Download(context.Background(), "org/repo", "model.gguf", "", t.TempDir(), nil, nil)
	if err == nil {
		t.Error("Download with 429 response should return an error")
	}
}

func TestDownloadResumeFromPartial(t *testing.T) {
	fullContent := []byte("HELLO WORLD THIS IS THE FULL TEST FILE CONTENT")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", "bytes 5-46/47")
			w.WriteHeader(http.StatusPartialContent)
			w.Write(fullContent[5:])
			return
		}
		w.Write(fullContent)
	}))
	defer server.Close()

	dir := t.TempDir()
	// Create a partial .tmp file (first 5 bytes).
	tmpPath := filepath.Join(dir, "model.gguf.tmp")
	if err := os.WriteFile(tmpPath, fullContent[:5], 0644); err != nil {
		t.Fatalf("failed to write partial tmp: %v", err)
	}

	c := &Client{dlBase: server.URL}
	written, err := c.Download(context.Background(), "org/repo", "model.gguf", "", dir, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if written != int64(len(fullContent)) {
		t.Errorf("written = %d, want %d", written, len(fullContent))
	}

	destPath := filepath.Join(dir, "model.gguf")
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("ReadFile dest: %v", err)
	}
	if !bytes.Equal(data, fullContent) {
		t.Errorf("file content mismatch: got %q, want %q", string(data), string(fullContent))
	}
	if _, err := os.Stat(tmpPath); err == nil {
		t.Error("tmp file should not exist after successful resume")
	}
}

func TestDownloadResumeServerIgnoresRange(t *testing.T) {
	fullContent := []byte("THIS IS THE CONTENT")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fullContent)
	}))
	defer server.Close()

	dir := t.TempDir()
	// Create a partial .tmp file with stale data from a previous attempt.
	tmpPath := filepath.Join(dir, "model.gguf.tmp")
	staleData := []byte("XXXXX") // not part of the actual content
	if err := os.WriteFile(tmpPath, staleData, 0644); err != nil {
		t.Fatalf("failed to write stale tmp: %v", err)
	}

	c := &Client{dlBase: server.URL}
	written, err := c.Download(context.Background(), "org/repo", "model.gguf", "", dir, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if written != int64(len(fullContent)) {
		t.Errorf("written = %d, want %d", written, len(fullContent))
	}

	destPath := filepath.Join(dir, "model.gguf")
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("ReadFile dest: %v", err)
	}
	if !bytes.Equal(data, fullContent) {
		t.Errorf("file content mismatch: got %q, want %q (stale partial should have been discarded)", string(data), string(fullContent))
	}
}

func TestDownloadResumeAlreadyComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 416 means the server considers the file already fully downloaded.
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer server.Close()

	dir := t.TempDir()
	// Create a "partial" .tmp file that is actually the complete file.
	tmpPath := filepath.Join(dir, "model.gguf.tmp")
	fullContent := []byte("the content")
	if err := os.WriteFile(tmpPath, fullContent, 0644); err != nil {
		t.Fatalf("failed to write tmp: %v", err)
	}

	c := &Client{dlBase: server.URL}
	written, err := c.Download(context.Background(), "org/repo", "model.gguf", "", dir, nil, nil)
	if err != nil {
		t.Fatalf("Download (416 resume complete): %v", err)
	}
	if written != int64(len(fullContent)) {
		t.Errorf("written = %d, want %d", written, len(fullContent))
	}

	// The .tmp file should have been renamed to the final destination.
	destPath := filepath.Join(dir, "model.gguf")
	if _, err := os.Stat(destPath); err != nil {
		t.Errorf("dest file should exist after 416 resume: %v", err)
	}
	if _, err := os.Stat(tmpPath); err == nil {
		t.Error("tmp file should not exist after successful 416 resume")
	}
}

func TestDownloadWithTee(t *testing.T) {
	content := []byte("tee writer test content here")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()
	var teeBuf bytes.Buffer

	c := &Client{dlBase: server.URL}
	written, err := c.Download(context.Background(), "org/repo", "model.gguf", "", dir, nil, &teeBuf)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if written != int64(len(content)) {
		t.Errorf("written = %d, want %d", written, len(content))
	}
	if !bytes.Equal(teeBuf.Bytes(), content) {
		t.Errorf("tee buffer mismatch: got %q, want %q", teeBuf.String(), string(content))
	}

	destPath := filepath.Join(dir, "model.gguf")
	destData, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("ReadFile dest: %v", err)
	}
	if !bytes.Equal(destData, content) {
		t.Errorf("file content mismatch: got %q, want %q", string(destData), string(content))
	}
}

func TestDownloadNoContentLength(t *testing.T) {
	content := []byte("content with no length header")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// httptest.Server sets Content-Length automatically based on w.Write.
		// To test no-Content-Length behavior, use chunked encoding.
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()
	var pWritten int64
	pcb := func(p Progress) {
		pWritten = p.Written
	}

	c := &Client{dlBase: server.URL}
	written, err := c.Download(context.Background(), "org/repo", "model.gguf", "", dir, pcb, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if written != int64(len(content)) {
		t.Errorf("written = %d, want %d", written, len(content))
	}
	if pWritten != int64(len(content)) {
		t.Errorf("progress.Written = %d, want %d", pWritten, len(content))
	}
}

func TestDownloadErrorCleansUpTmp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte("partial data"))
		// Simulate connection drop by not writing the full content.
	}))
	defer server.Close()

	dir := t.TempDir()
	// Wrap in a context with timeout so the test doesn't block on io.Copy
	// when the server doesn't close the connection.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c := &Client{dlBase: server.URL}
	_, err := c.Download(ctx, "org/repo", "model.gguf", "", dir, nil, nil)
	// Either we get an error (connection drop) or context deadline.
	if err == nil {
		t.Error("expected error for truncated download")
	}
	// Tmp file should be cleaned up.
	tmpPath := filepath.Join(dir, "model.gguf.tmp")
	if _, statErr := os.Stat(tmpPath); statErr == nil {
		t.Error("tmp file should have been cleaned up after download error")
	}
}

func TestDownloadServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("unavailable"))
	}))
	defer server.Close()

	c := &Client{dlBase: server.URL}
	_, err := c.Download(context.Background(), "org/repo", "model.gguf", "", t.TempDir(), nil, nil)
	if err == nil {
		t.Error("expected error for 503 response")
	}
	if !strings.Contains(err.Error(), "503") && !strings.Contains(err.Error(), "Service") {
		t.Errorf("error should mention status: %v", err)
	}
}
