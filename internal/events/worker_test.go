package events

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
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

	"hali/internal/config"
	"hali/internal/profiles"
)

func writeTestTorrentFile(t *testing.T, infohash string) {
	t.Helper()
	torrentDir := filepath.Join(config.ServiceDataDir(), "torrents")
	if err := os.MkdirAll(torrentDir, 0755); err != nil {
		t.Fatalf("MkdirAll torrent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(torrentDir, strings.ToLower(infohash)+".torrent"), []byte("test-torrent"), 0644); err != nil {
		t.Fatalf("WriteFile torrent: %v", err)
	}
}

func decodeMultipartEvent(t *testing.T, r *http.Request) ModelPullEvent {
	t.Helper()
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		t.Fatalf("ParseMultipartForm: %v", err)
	}
	file, _, err := r.FormFile("torrent")
	if err != nil {
		t.Fatalf("FormFile(torrent): %v", err)
	}
	defer file.Close()
	if _, err := io.ReadAll(file); err != nil {
		t.Fatalf("read torrent form file: %v", err)
	}
	parsedTime, err := time.Parse(time.RFC3339Nano, r.FormValue("timestamp"))
	if err != nil {
		t.Fatalf("parse timestamp: %v", err)
	}
	return ModelPullEvent{
		ModelID:         r.FormValue("model_id"),
		Revision:        r.FormValue("revision"),
		InfoHash:        r.FormValue("infohash"),
		PublisherPubKey: r.FormValue("publisher_pubkey"),
		PublisherSig:    r.FormValue("publisher_sig"),
		Quantization:    r.FormValue("quantization"),
		Magnet:          r.FormValue("magnet"),
		SourceURL:       r.FormValue("source_url"),
		LocalHash:       r.FormValue("local_hash"),
		Timestamp:       parsedTime,
	}
}

func TestWorkerDrainDisabledLeavesQueuedFile(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)
	event := ModelPullEvent{ModelID: "m", Revision: "r", InfoHash: "da39a3ee5e6b4b0d3255bfef95601890afd80709", Magnet: "magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709", SourceURL: "src", LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Timestamp: time.Unix(1, 0).UTC()}
	if err := q.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	worker := NewWorker(dir, func() (config.File, error) {
		return config.File{}, nil
	})
	enabled := false
	worker.loadCfg = func() (config.File, error) {
		return config.File{TelemetryEnabled: &enabled}, nil
	}
	worker.drain()
	queued, err := q.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("queued = %d, want 1", len(queued))
	}
}

func TestWorkerDrainDeletesDeliveredFile(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	q := NewQueue(dir)
	event := ModelPullEvent{ModelID: "m", Revision: "r", InfoHash: "da39a3ee5e6b4b0d3255bfef95601890afd80709", Magnet: "magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709", SourceURL: "src", LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Timestamp: time.Unix(1, 0).UTC()}
	writeTestTorrentFile(t, event.InfoHash)
	if err := q.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != "/ingest" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := NewWorker(dir, func() (config.File, error) {
		return config.File{RegistryIngestURL: server.URL}, nil
	})
	worker.drain()
	queued, err := q.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(queued) != 0 {
		t.Fatalf("queued = %d, want 0", len(queued))
	}
}

func TestWorkerDrainDropsEventOnPermanentHTTPError(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	q := NewQueue(dir)
	event := ModelPullEvent{ModelID: "m", Revision: "r", InfoHash: "da39a3ee5e6b4b0d3255bfef95601890afd80709", Magnet: "magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709", SourceURL: "src", LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Timestamp: time.Unix(1, 0).UTC()}
	writeTestTorrentFile(t, event.InfoHash)
	if err := q.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid payload"))
	}))
	defer server.Close()

	worker := NewWorker(dir, func() (config.File, error) {
		return config.File{RegistryIngestURL: server.URL}, nil
	})
	worker.drain()

	queued, err := q.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(queued) != 0 {
		t.Fatalf("queued = %d, want 0", len(queued))
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retries on permanent error)", got)
	}
}

func TestWorkerDrainDefersRetryableHTTPError(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	q := NewQueue(dir)
	event := ModelPullEvent{ModelID: "m", Revision: "r", InfoHash: "da39a3ee5e6b4b0d3255bfef95601890afd80709", Magnet: "magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709", SourceURL: "src", LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Timestamp: time.Unix(1, 0).UTC()}
	writeTestTorrentFile(t, event.InfoHash)
	if err := q.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var postAttempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(&postAttempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("backend unavailable"))
	}))
	defer server.Close()

	worker := NewWorker(dir, func() (config.File, error) {
		return config.File{RegistryIngestURL: server.URL}, nil
	})
	close(worker.stopCh)

	before := time.Now().UTC()
	worker.drain()

	queued, err := q.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("queued = %d, want 1", len(queued))
	}
	if queued[0].Event.DeliveryAttempts != 1 {
		t.Fatalf("DeliveryAttempts = %d, want 1", queued[0].Event.DeliveryAttempts)
	}
	if queued[0].Event.NextAttemptAfter.IsZero() {
		t.Fatal("NextAttemptAfter should be set for deferred retry")
	}
	if !queued[0].Event.NextAttemptAfter.After(before) {
		t.Fatalf("NextAttemptAfter = %s, want future time", queued[0].Event.NextAttemptAfter)
	}
	if got := atomic.LoadInt32(&postAttempts); got != 1 {
		t.Fatalf("post attempts = %d, want 1 when stopCh is closed", got)
	}
}

func TestWorkerDrainDropsEventAfterMaxRetryableFailures(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	q := NewQueue(dir)
	event := ModelPullEvent{
		ModelID:          "m",
		Revision:         "r",
		InfoHash:         "da39a3ee5e6b4b0d3255bfef95601890afd80709",
		Magnet:           "magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709",
		SourceURL:        "src",
		LocalHash:        "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp:        time.Unix(1, 0).UTC(),
		DeliveryAttempts: maxDeliveryAttempts - 1,
	}
	writeTestTorrentFile(t, event.InfoHash)
	if err := q.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	worker := NewWorker(dir, func() (config.File, error) {
		return config.File{RegistryIngestURL: server.URL}, nil
	})
	close(worker.stopCh)

	worker.drain()

	queued, err := q.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(queued) != 0 {
		t.Fatalf("queued = %d, want 0", len(queued))
	}
}

func TestDefaultQueueDirUsesServiceDataDir(t *testing.T) {
	serviceDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", serviceDir)
	if got, want := DefaultQueueDir(), filepath.Join(serviceDir, "events"); got != want {
		t.Fatalf("DefaultQueueDir() = %q, want %q", got, want)
	}
	if _, err := os.Stat(serviceDir); err != nil {
		t.Fatalf("service dir should exist: %v", err)
	}
}

func TestWorkerSendWithRetryStopsOnSuccess(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := NewWorker(t.TempDir(), func() (config.File, error) {
		return config.File{RegistryIngestURL: server.URL}, nil
	})
	event := ModelPullEvent{
		ModelID:         "m",
		Revision:        "r",
		InfoHash:        "da39a3ee5e6b4b0d3255bfef95601890afd80709",
		PublisherPubKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PublisherSig:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Magnet:          "magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709",
		SourceURL:       "https://example.invalid/model.gguf",
		LocalHash:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp:       time.Now().UTC(),
	}
	writeTestTorrentFile(t, event.InfoHash)
	if err := worker.sendWithRetry(NewIngestClient(server.URL), event); err != nil {
		t.Fatalf("sendWithRetry: %v", err)
	}
	if attempts < 2 {
		t.Fatalf("attempts = %d, want >= 2", attempts)
	}

	_ = worker
	_ = context.Background()
}

func TestWorkerSendWithRetryReturnsErrorOnHTTPFailure(t *testing.T) {
	t.Setenv("HALI_SERVICE_DATA_DIR", t.TempDir())
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("backend unavailable"))
	}))
	defer server.Close()

	worker := NewWorker(t.TempDir(), func() (config.File, error) {
		return config.File{RegistryIngestURL: server.URL}, nil
	})
	close(worker.stopCh)

	event := ModelPullEvent{
		ModelID:         "m",
		Revision:        "r",
		InfoHash:        "da39a3ee5e6b4b0d3255bfef95601890afd80709",
		PublisherPubKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PublisherSig:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Magnet:          "magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709",
		SourceURL:       "https://example.invalid/model.gguf",
		LocalHash:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp:       time.Now().UTC(),
	}
	writeTestTorrentFile(t, event.InfoHash)

	err := worker.sendWithRetry(NewIngestClient(server.URL), event)
	if err == nil {
		t.Fatal("sendWithRetry() expected error on HTTP 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("sendWithRetry() error = %q, want status code context", err)
	}
	if atomic.LoadInt32(&attempts) == 0 {
		t.Fatal("sendWithRetry() did not attempt any HTTP request")
	}
}

func TestWorkerDrainAddsPublisherAttribution(t *testing.T) {
	serviceDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", serviceDir)

	dir := t.TempDir()
	q := NewQueue(dir)
	event := ModelPullEvent{
		ModelID:   "m",
		Revision:  "r",
		InfoHash:  "da39a3ee5e6b4b0d3255bfef95601890afd80709",
		Magnet:    "magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709",
		SourceURL: "src",
		LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp: time.Unix(1, 0).UTC(),
	}
	writeTestTorrentFile(t, event.InfoHash)
	if err := q.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var got ModelPullEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		got = decodeMultipartEvent(t, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := NewWorker(dir, func() (config.File, error) {
		return config.File{RegistryIngestURL: server.URL}, nil
	})
	worker.drain()

	if got.PublisherPubKey == "" {
		t.Fatal("PublisherPubKey is empty")
	}
	if got.PublisherSig == "" {
		t.Fatal("PublisherSig is empty")
	}
	pub, err := hex.DecodeString(got.PublisherPubKey)
	if err != nil {
		t.Fatalf("decode pubkey: %v", err)
	}
	sig, err := hex.DecodeString(got.PublisherSig)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	payload := []byte(strings.Join([]string{
		got.ModelID,
		got.Revision,
		got.InfoHash,
		got.Magnet,
		got.SourceURL,
		got.LocalHash,
		got.Timestamp.UTC().Format(time.RFC3339Nano),
		got.PublisherPubKey,
	}, "\n"))
	if !ed25519.Verify(ed25519.PublicKey(pub), payload, sig) {
		t.Fatal("publisher signature failed verification")
	}
}

func TestWorkerDrainUsesProfilePubKeyWhenAvailable(t *testing.T) {
	serviceDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", serviceDir)

	pub, err := config.LoadOrCreateNodePublicKeyHex()
	if err != nil {
		t.Fatalf("LoadOrCreateNodePublicKeyHex: %v", err)
	}
	sp := profiles.SignedProfile{Profile: profiles.Profile{PubKey: strings.ToUpper(pub), DisplayName: "Tester", Timestamp: time.Now().Unix()}, Signature: ""}
	data, err := json.Marshal(sp)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(serviceDir, "profile.json"), data, 0600); err != nil {
		t.Fatalf("write profile.json: %v", err)
	}

	dir := t.TempDir()
	q := NewQueue(dir)
	event := ModelPullEvent{
		ModelID:   "m",
		Revision:  "r",
		InfoHash:  "da39a3ee5e6b4b0d3255bfef95601890afd80709",
		Magnet:    "magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709",
		SourceURL: "src",
		LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp: time.Unix(1, 0).UTC(),
	}
	writeTestTorrentFile(t, event.InfoHash)
	if err := q.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var got ModelPullEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		got = decodeMultipartEvent(t, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := NewWorker(dir, func() (config.File, error) {
		return config.File{RegistryIngestURL: server.URL}, nil
	})
	worker.drain()

	if got.PublisherPubKey != strings.ToLower(pub) {
		t.Fatalf("PublisherPubKey = %q, want %q", got.PublisherPubKey, strings.ToLower(pub))
	}
}

func TestWorkerDrainUsesLegacyPlainProfilePubKeyWhenAvailable(t *testing.T) {
	serviceDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", serviceDir)

	pub, err := config.LoadOrCreateNodePublicKeyHex()
	if err != nil {
		t.Fatalf("LoadOrCreateNodePublicKeyHex: %v", err)
	}
	legacy := profiles.Profile{PubKey: strings.ToUpper(pub), DisplayName: "Tester", Timestamp: time.Now().Unix()}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(serviceDir, "profile.json"), data, 0600); err != nil {
		t.Fatalf("write profile.json: %v", err)
	}

	dir := t.TempDir()
	q := NewQueue(dir)
	event := ModelPullEvent{
		ModelID:   "m",
		Revision:  "r",
		InfoHash:  "da39a3ee5e6b4b0d3255bfef95601890afd80709",
		Magnet:    "magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709",
		SourceURL: "src",
		LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp: time.Unix(1, 0).UTC(),
	}
	writeTestTorrentFile(t, event.InfoHash)
	if err := q.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var got ModelPullEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		got = decodeMultipartEvent(t, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := NewWorker(dir, func() (config.File, error) {
		return config.File{RegistryIngestURL: server.URL}, nil
	})
	worker.drain()

	if got.PublisherPubKey != strings.ToLower(pub) {
		t.Fatalf("PublisherPubKey = %q, want %q", got.PublisherPubKey, strings.ToLower(pub))
	}
}

func TestWorkerDrainProfilePubKeyMismatchStopsDelivery(t *testing.T) {
	serviceDir := t.TempDir()
	t.Setenv("HALI_SERVICE_DATA_DIR", serviceDir)

	sp := profiles.SignedProfile{Profile: profiles.Profile{PubKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", DisplayName: "Mismatch", Timestamp: time.Now().Unix()}, Signature: ""}
	data, err := json.Marshal(sp)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(serviceDir, "profile.json"), data, 0600); err != nil {
		t.Fatalf("write profile.json: %v", err)
	}

	dir := t.TempDir()
	q := NewQueue(dir)
	event := ModelPullEvent{
		ModelID:   "m",
		Revision:  "r",
		InfoHash:  "da39a3ee5e6b4b0d3255bfef95601890afd80709",
		Magnet:    "magnet:?xt=urn:btih:da39a3ee5e6b4b0d3255bfef95601890afd80709",
		SourceURL: "src",
		LocalHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Timestamp: time.Unix(1, 0).UTC(),
	}
	if err := q.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := NewWorker(dir, func() (config.File, error) {
		return config.File{RegistryIngestURL: server.URL}, nil
	})
	worker.drain()

	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("ingest should not be called on pubkey mismatch")
	}
	queued, err := q.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("queued = %d, want 1", len(queued))
	}
}
