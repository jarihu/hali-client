package transmission

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"hali/internal/config"
)

const testSessionID = "test-session-id-abc123"

// newMockTransmission builds a test server that simulates Transmission RPC.
// The server enforces the session-ID handshake: requests without the correct
// X-Transmission-Session-Id header receive a 409. Once the header is present,
// the server returns successBody as the RPC response.
func newMockTransmission(t *testing.T, successBody string) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	mux := http.NewServeMux()
	mux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		log.addCall()
		log.captureBody(r)
		if r.Header.Get("X-Transmission-Session-Id") != testSessionID {
			w.Header().Set("X-Transmission-Session-Id", testSessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, successBody) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, log
}

type requestLog struct {
	calls int64
	body  string
}

func (l *requestLog) addCall() { atomic.AddInt64(&l.calls, 1) }

func (l *requestLog) captureBody(r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	l.body = string(raw)
}

func (l *requestLog) callCount() int { return int(atomic.LoadInt64(&l.calls)) }

func successAddBody(added bool) string {
	key := "torrent-added"
	if !added {
		key = "torrent-duplicate"
	}
	ref := torrentRef{HashString: strings.Repeat("a", 40), ID: 1, Name: "model"}
	data, _ := json.Marshal(map[string]any{
		"result":    "success",
		"arguments": map[string]any{key: ref},
	})
	return string(data)
}

// makeSeeder creates a Seeder pointing at srv, with a temp torrentDir containing
// a fake .torrent file for infohash "a"*40.
func makeSeeder(t *testing.T, srv *httptest.Server) (*Seeder, string) {
	t.Helper()
	torrentDir := t.TempDir()
	fakeInfohash := strings.Repeat("a", 40)
	if err := os.WriteFile(filepath.Join(torrentDir, fakeInfohash+".torrent"), []byte("fake"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.TransmissionConfig{URL: srv.URL}
	s, err := NewSeeder(cfg, torrentDir)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	return s, fakeInfohash
}

func TestSeedSuccess(t *testing.T) {
	srv, log := newMockTransmission(t, successAddBody(true))
	s, ih := makeSeeder(t, srv)
	contentDir := t.TempDir()

	if err := s.Seed(t.Context(), ih, contentDir); err != nil {
		t.Fatalf("Seed returned error: %v", err)
	}
	// Expect 2 calls: first 409 (no session header) + retry with session header.
	if log.callCount() != 2 {
		t.Fatalf("expected 2 HTTP calls (409 + retry), got %d", log.callCount())
	}
}

func TestSeedDuplicateHandledAsSuccess(t *testing.T) {
	srv, _ := newMockTransmission(t, successAddBody(false))
	s, ih := makeSeeder(t, srv)
	contentDir := t.TempDir()

	if err := s.Seed(t.Context(), ih, contentDir); err != nil {
		t.Fatalf("Seed returned error for duplicate: %v", err)
	}
}

func TestSeedMissingContentDir(t *testing.T) {
	srv, log := newMockTransmission(t, successAddBody(true))
	s, ih := makeSeeder(t, srv)

	err := s.Seed(t.Context(), ih, "/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for missing content dir")
	}
	if log.callCount() != 0 {
		t.Fatal("should not contact Transmission when content dir is missing")
	}
}

func TestSeedMissingTorrentFile(t *testing.T) {
	srv, log := newMockTransmission(t, successAddBody(true))
	cfg := config.TransmissionConfig{URL: srv.URL}
	s, err := NewSeeder(cfg, t.TempDir()) // empty torrentDir — no .torrent files
	if err != nil {
		t.Fatal(err)
	}
	contentDir := t.TempDir()
	ih := strings.Repeat("b", 40)

	seedErr := s.Seed(t.Context(), ih, contentDir)
	if seedErr == nil {
		t.Fatal("expected error when torrent file is missing (no silent fallback)")
	}
	if log.callCount() != 0 {
		t.Fatal("should not contact Transmission when torrent file is missing")
	}
}

func TestSeedInvalidInfohash(t *testing.T) {
	srv, log := newMockTransmission(t, successAddBody(true))
	s, _ := makeSeeder(t, srv)
	contentDir := t.TempDir()

	err := s.Seed(t.Context(), strings.Repeat("a", 39), contentDir)
	if err == nil {
		t.Fatal("expected error for 39-char infohash")
	}
	if log.callCount() != 0 {
		t.Fatal("should not contact Transmission for invalid infohash")
	}
}

func TestSeedServiceUnavailable(t *testing.T) {
	srv, _ := newMockTransmission(t, successAddBody(true))
	s, ih := makeSeeder(t, srv)
	contentDir := t.TempDir()
	srv.Close() // closed before Seed is called

	err := s.Seed(t.Context(), ih, contentDir)
	if err == nil {
		t.Fatal("expected error when Transmission is unreachable")
	}
}

func TestSessionRetryOn409(t *testing.T) {
	// Verify that the client correctly handles the 409 handshake and sends
	// the session ID on the retry request.
	var sessionOnRetry string
	var callCount int

	mux := http.NewServeMux()
	mux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		sid := r.Header.Get("X-Transmission-Session-Id")
		if sid == "" {
			w.Header().Set("X-Transmission-Session-Id", testSessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		sessionOnRetry = sid
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, successAddBody(true)) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	s, ih := makeSeeder(t, srv)
	contentDir := t.TempDir()

	if err := s.Seed(t.Context(), ih, contentDir); err != nil {
		t.Fatalf("Seed failed: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls (409 + retry), got %d", callCount)
	}
	if sessionOnRetry != testSessionID {
		t.Fatalf("expected session ID %q on retry, got %q", testSessionID, sessionOnRetry)
	}
}

func TestDownloadDirUsesForwardSlashes(t *testing.T) {
	var capturedBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		capturedBody = string(raw)
		// Always accept (skip session handshake for simplicity of this test).
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", testSessionID)
			w.WriteHeader(http.StatusConflict)
			capturedBody = "" // reset — this was the 409 call
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, successAddBody(true)) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	s, ih := makeSeeder(t, srv)
	contentDir := t.TempDir()

	if err := s.Seed(t.Context(), ih, contentDir); err != nil {
		t.Fatalf("Seed failed: %v", err)
	}

	if strings.Contains(capturedBody, `\\`) {
		t.Fatalf("download-dir must not contain backslashes, got body: %s", capturedBody)
	}
}

func TestNewSeeder_EmptyURL(t *testing.T) {
	_, err := NewSeeder(config.TransmissionConfig{}, t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}
