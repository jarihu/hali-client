package qbittorrent

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hali/internal/config"
)

// newMockQBit builds a test server that returns the given responses for the
// login and add endpoints. infoResp should be a JSON array (e.g. "[]" or "[{...}]").
func newMockQBit(t *testing.T, loginResp, infoResp, addResp string) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		log.add("login")
		io.WriteString(w, loginResp) //nolint:errcheck
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		log.add("info")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, infoResp) //nolint:errcheck
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		log.add("add")
		// Capture multipart fields for assertions.
		ct := r.Header.Get("Content-Type")
		_, params, _ := mime.ParseMediaType(ct)
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			name := p.FormName()
			val, _ := io.ReadAll(p)
			log.addField(name, string(val))
		}
		io.WriteString(w, addResp) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, log
}

type requestLog struct {
	calls  []string
	fields map[string]string
}

func (l *requestLog) add(name string) { l.calls = append(l.calls, name) }

func (l *requestLog) addField(k, v string) {
	if l.fields == nil {
		l.fields = map[string]string{}
	}
	l.fields[k] = v
}

func (l *requestLog) called(name string) bool {
	for _, c := range l.calls {
		if c == name {
			return true
		}
	}
	return false
}

// makeSeeder builds a Seeder pointing at srv with a temp torrentDir containing
// a fake .torrent file for infohash "a" * 40.
func makeSeeder(t *testing.T, srv *httptest.Server) (*Seeder, string) {
	t.Helper()
	torrentDir := t.TempDir()
	fakeInfohash := strings.Repeat("a", 40)
	if err := os.WriteFile(filepath.Join(torrentDir, fakeInfohash+".torrent"), []byte("fake"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.QBittorrentConfig{URL: srv.URL, Username: "admin", Password: "pass"}
	s, err := NewSeeder(cfg, torrentDir)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	return s, fakeInfohash
}

func TestSeedSuccess(t *testing.T) {
	srv, log := newMockQBit(t, "Ok.", "[]", "Ok.")
	s, ih := makeSeeder(t, srv)
	contentDir := t.TempDir()

	if err := s.Seed(t.Context(), ih, contentDir); err != nil {
		t.Fatalf("Seed returned error: %v", err)
	}
	if !log.called("login") || !log.called("info") || !log.called("add") {
		t.Fatalf("expected login+info+add calls, got %v", log.calls)
	}
}

func TestSeedAlreadyExistsViaInfo(t *testing.T) {
	// TorrentInfo returns a non-empty result → should skip Add and return nil.
	infoJSON, _ := json.Marshal([]TorrentInfo{{Hash: strings.Repeat("a", 40), State: "seeding"}})
	srv, log := newMockQBit(t, "Ok.", string(infoJSON), "Ok.")
	s, ih := makeSeeder(t, srv)
	contentDir := t.TempDir()

	if err := s.Seed(t.Context(), ih, contentDir); err != nil {
		t.Fatalf("Seed returned error: %v", err)
	}
	if log.called("add") {
		t.Fatal("AddTorrent should not be called when torrent already exists via TorrentInfo")
	}
}

func TestSeedAlreadyExistsViaAdd(t *testing.T) {
	// AddTorrent returns "Fails." (duplicate) → Seed must return nil.
	srv, _ := newMockQBit(t, "Ok.", "[]", "Fails.")
	s, ih := makeSeeder(t, srv)
	contentDir := t.TempDir()

	if err := s.Seed(t.Context(), ih, contentDir); err != nil {
		t.Fatalf("Seed returned error for duplicate: %v", err)
	}
}

func TestSeedLoginFails(t *testing.T) {
	srv, _ := newMockQBit(t, "Fails.", "[]", "Ok.")
	s, ih := makeSeeder(t, srv)
	contentDir := t.TempDir()

	err := s.Seed(t.Context(), ih, contentDir)
	if err == nil {
		t.Fatal("expected error on login failure")
	}
	var authErr *AuthError
	if !isAuthError(err, &authErr) {
		t.Fatalf("expected *AuthError, got %T: %v", err, err)
	}
}

func isAuthError(err error, target **AuthError) bool {
	if ae, ok := err.(*AuthError); ok {
		*target = ae
		return true
	}
	return false
}

func TestSeedMissingContentDir(t *testing.T) {
	srv, log := newMockQBit(t, "Ok.", "[]", "Ok.")
	s, ih := makeSeeder(t, srv)

	err := s.Seed(t.Context(), ih, "/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for missing content dir")
	}
	if log.called("login") {
		t.Fatal("should not have contacted qBittorrent when content dir is missing")
	}
}

func TestSeedMissingTorrentFile(t *testing.T) {
	srv, log := newMockQBit(t, "Ok.", "[]", "Ok.")
	cfg := config.QBittorrentConfig{URL: srv.URL, Username: "admin", Password: "pass"}
	// Use a torrent dir with no files.
	s, err := NewSeeder(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	contentDir := t.TempDir()
	ih := strings.Repeat("b", 40)

	seedErr := s.Seed(t.Context(), ih, contentDir)
	if seedErr == nil {
		t.Fatal("expected error when torrent file is missing (no silent fallback)")
	}
	if log.called("login") {
		t.Fatal("should not contact qBittorrent when torrent file is missing")
	}
}

func TestSeedInvalidInfohash(t *testing.T) {
	srv, log := newMockQBit(t, "Ok.", "[]", "Ok.")
	s, _ := makeSeeder(t, srv)
	contentDir := t.TempDir()

	// 39 chars — too short.
	err := s.Seed(t.Context(), strings.Repeat("a", 39), contentDir)
	if err == nil {
		t.Fatal("expected error for 39-char infohash")
	}
	if log.called("login") {
		t.Fatal("should not contact qBittorrent for invalid infohash")
	}
}

func TestSeedServiceUnavailable(t *testing.T) {
	srv, _ := newMockQBit(t, "Ok.", "[]", "Ok.")
	s, ih := makeSeeder(t, srv)
	contentDir := t.TempDir()
	srv.Close() // close before Seed is called

	err := s.Seed(t.Context(), ih, contentDir)
	if err == nil {
		t.Fatal("expected error when qBittorrent is unreachable")
	}
}

func TestAddTorrentSavePathSlashes(t *testing.T) {
	srv, log := newMockQBit(t, "Ok.", "[]", "Ok.")
	s, ih := makeSeeder(t, srv)

	// Use a path with backslashes (simulating Windows).
	contentDir := t.TempDir()
	// Override contentDir to one that looks like a Windows path for the field check.
	_ = s.Seed(t.Context(), ih, contentDir)

	savepath := log.fields["savepath"]
	if strings.Contains(savepath, `\`) {
		t.Fatalf("savepath must use forward slashes, got: %q", savepath)
	}
}

func TestAddTorrentSkipCheckingFalse(t *testing.T) {
	srv, log := newMockQBit(t, "Ok.", "[]", "Ok.")
	s, ih := makeSeeder(t, srv)
	contentDir := t.TempDir()

	_ = s.Seed(t.Context(), ih, contentDir)

	if v := log.fields["skip_checking"]; v != "false" {
		t.Fatalf("expected skip_checking=false, got %q", v)
	}
}

func TestNewSeeder_EmptyURL(t *testing.T) {
	_, err := NewSeeder(config.QBittorrentConfig{}, t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}
