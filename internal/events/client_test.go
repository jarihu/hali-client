package events

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIngestClientSendsPost(t *testing.T) {
	serviceDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", serviceDir)
	infohash := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	var gotHeadPath string
	var gotPostPath string
	var gotMethod string
	var gotInfohash string
	var gotTorrent []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			gotHeadPath = r.URL.Path
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			gotPostPath = r.URL.Path
			gotMethod = r.Method
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				t.Fatalf("ParseMultipartForm: %v", err)
			}
			gotInfohash = r.FormValue("infohash")
			file, _, err := r.FormFile("torrent")
			if err != nil {
				t.Fatalf("FormFile(torrent): %v", err)
			}
			defer file.Close()
			gotTorrent, err = io.ReadAll(file)
			if err != nil {
				t.Fatalf("read torrent file: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	client := NewIngestClient(server.URL)
	torrentDir := filepath.Join(serviceDir, "torrents")
	if err := os.MkdirAll(torrentDir, 0755); err != nil {
		t.Fatalf("MkdirAll torrent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(torrentDir, infohash+".torrent"), []byte("test-torrent"), 0644); err != nil {
		t.Fatalf("WriteFile torrent: %v", err)
	}

	event := ModelPullEvent{
		ModelID:         "m:7b:instruct:q4_k_m",
		Revision:        "abc123",
		InfoHash:        infohash,
		PublisherPubKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PublisherSig:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Magnet:          "magnet:?xt=urn:btih:" + infohash,
		SourceURL:       "https://example.invalid/model.gguf",
		LocalHash:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp:       time.Unix(123, 0).UTC(),
	}
	if err := client.Send(t.Context(), event); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotHeadPath != "/ingest/"+infohash {
		t.Fatalf("head path = %q, want %q", gotHeadPath, "/ingest/"+infohash)
	}
	if gotPostPath != "/ingest" {
		t.Fatalf("post path = %q, want /ingest", gotPostPath)
	}
	if gotInfohash != infohash {
		t.Fatalf("infohash = %q, want %q", gotInfohash, infohash)
	}
	if string(gotTorrent) != "test-torrent" {
		t.Fatalf("torrent payload = %q, want %q", string(gotTorrent), "test-torrent")
	}
}

func TestIngestClientDoesNotDuplicateIngestPath(t *testing.T) {
	serviceDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", serviceDir)

	infohash := "0123456789012345678901234567890123456789"
	var gotHeadPath string
	var gotPostPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			gotHeadPath = r.URL.Path
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			gotPostPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	torrentDir := filepath.Join(serviceDir, "torrents")
	if err := os.MkdirAll(torrentDir, 0755); err != nil {
		t.Fatalf("MkdirAll torrent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(torrentDir, infohash+".torrent"), []byte("test-torrent"), 0644); err != nil {
		t.Fatalf("WriteFile torrent: %v", err)
	}

	client := NewIngestClient(server.URL + "/ingest")
	event := ModelPullEvent{
		ModelID:         "m",
		Revision:        "r",
		InfoHash:        infohash,
		PublisherPubKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PublisherSig:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Magnet:          "magnet:?xt=urn:btih:" + infohash,
		SourceURL:       "https://example.invalid/model.gguf",
		LocalHash:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp:       time.Unix(123, 0).UTC(),
	}

	if err := client.Send(t.Context(), event); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotHeadPath != "/ingest/"+infohash {
		t.Fatalf("head path = %q, want %q", gotHeadPath, "/ingest/"+infohash)
	}
	if gotPostPath != "/ingest" {
		t.Fatalf("post path = %q, want /ingest", gotPostPath)
	}
}

func TestIngestClientCollapsesRepeatedIngestSuffix(t *testing.T) {
	serviceDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", serviceDir)

	infohash := "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	var gotHeadPath string
	var gotPostPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			gotHeadPath = r.URL.Path
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			gotPostPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	torrentDir := filepath.Join(serviceDir, "torrents")
	if err := os.MkdirAll(torrentDir, 0755); err != nil {
		t.Fatalf("MkdirAll torrent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(torrentDir, infohash+".torrent"), []byte("test-torrent"), 0644); err != nil {
		t.Fatalf("WriteFile torrent: %v", err)
	}

	client := NewIngestClient(server.URL + "/ingest/ingest")
	event := ModelPullEvent{
		ModelID:         "m",
		Revision:        "r",
		InfoHash:        infohash,
		PublisherPubKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PublisherSig:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Magnet:          "magnet:?xt=urn:btih:" + infohash,
		SourceURL:       "https://example.invalid/model.gguf",
		LocalHash:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp:       time.Unix(123, 0).UTC(),
	}

	if err := client.Send(t.Context(), event); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotHeadPath != "/ingest/"+infohash {
		t.Fatalf("head path = %q, want %q", gotHeadPath, "/ingest/"+infohash)
	}
	if gotPostPath != "/ingest" {
		t.Fatalf("post path = %q, want /ingest", gotPostPath)
	}
}

func TestIngestClientSkipsPostWhenInfohashExists(t *testing.T) {
	serviceDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", serviceDir)

	infohash := "9999999999999999999999999999999999999999"
	var gotHeadPath string
	var postCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			gotHeadPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			postCalls++
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	torrentDir := filepath.Join(serviceDir, "torrents")
	if err := os.MkdirAll(torrentDir, 0755); err != nil {
		t.Fatalf("MkdirAll torrent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(torrentDir, infohash+".torrent"), []byte("test-torrent"), 0644); err != nil {
		t.Fatalf("WriteFile torrent: %v", err)
	}

	client := NewIngestClient(server.URL)
	event := ModelPullEvent{
		ModelID:         "m",
		Revision:        "r",
		InfoHash:        infohash,
		PublisherPubKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PublisherSig:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Magnet:          "magnet:?xt=urn:btih:" + infohash,
		SourceURL:       "https://example.invalid/model.gguf",
		LocalHash:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp:       time.Unix(123, 0).UTC(),
	}

	if err := client.Send(t.Context(), event); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotHeadPath != "/ingest/"+infohash {
		t.Fatalf("head path = %q, want %q", gotHeadPath, "/ingest/"+infohash)
	}
	if postCalls != 0 {
		t.Fatalf("post calls = %d, want 0", postCalls)
	}
}

func TestIngestClientReturnsErrorOnHTTPFailure(t *testing.T) {
	serviceDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", serviceDir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid form payload"))
	}))
	defer server.Close()

	infohash := "fedcbafedcbafedcbafedcbafedcbafedcbafedc"
	torrentDir := filepath.Join(serviceDir, "torrents")
	if err := os.MkdirAll(torrentDir, 0755); err != nil {
		t.Fatalf("MkdirAll torrent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(torrentDir, infohash+".torrent"), []byte("test-torrent"), 0644); err != nil {
		t.Fatalf("WriteFile torrent: %v", err)
	}

	client := NewIngestClient(server.URL)
	event := ModelPullEvent{
		ModelID:         "m",
		Revision:        "r",
		InfoHash:        infohash,
		PublisherPubKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PublisherSig:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Magnet:          "magnet:?xt=urn:btih:" + infohash,
		SourceURL:       "https://example.invalid/model.gguf",
		LocalHash:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp:       time.Unix(123, 0).UTC(),
	}

	err := client.Send(t.Context(), event)
	if err == nil {
		t.Fatal("Send() expected error on HTTP 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("Send() error = %q, want status code context", err)
	}
	if !strings.Contains(err.Error(), "invalid form payload") {
		t.Fatalf("Send() error = %q, want backend response body context", err)
	}
}
