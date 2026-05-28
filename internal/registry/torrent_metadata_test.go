package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
)

func TestBuildDeterministicTorrentMetadataStable(t *testing.T) {
	a := BuildDeterministicTorrentMetadata("owner/repo", "rev1", "model.gguf", 123)
	b := BuildDeterministicTorrentMetadata("owner/repo", "rev1", "model.gguf", 123)
	if a != b {
		t.Fatalf("metadata not deterministic: %+v vs %+v", a, b)
	}
	c := BuildDeterministicTorrentMetadata("owner/repo", "rev1", "other.gguf", 123)
	if a.Infohash == c.Infohash {
		t.Fatal("infohash should differ when inputs differ")
	}
}

func TestUploadRepoBatchIngestAlreadyExistsFromHead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && strings.HasPrefix(r.URL.Path, "/ingest/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	c := &Client{baseURL: server.URL, httpClient: server.Client()}
	meta := BuildDeterministicTorrentMetadata("owner/repo", "rev", "a.gguf", 1)
	res, err := c.UploadRepoBatchIngest(context.Background(), "owner", "repo", RepoBatchIngestRequest{
		Revision:        "rev",
		PublisherPubKey: strings.Repeat("a", 64),
		Files: []RepoBatchIngestFile{{
			Torrent:      "dG9ycmVudA==",
			Infohash:     meta.Infohash,
			Magnet:       "magnet:?xt=urn:btih:" + meta.Infohash,
			SourceURL:    "https://huggingface.co/owner/repo/resolve/rev/a.gguf",
			LocalHash:    strings.Repeat("b", 64),
			PublisherSig: strings.Repeat("c", 128),
			Timestamp:    "2026-05-26T00:00:00Z",
		}},
	})
	if err != nil {
		t.Fatalf("UploadRepoBatchIngest: %v", err)
	}
	if !res.AlreadyExists {
		t.Fatal("expected already exists result")
	}
}

func TestUploadRepoBatchIngestPostsPayload(t *testing.T) {
	var sawPost bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			sawPost = true
			if got, want := path.Clean(r.URL.Path), "/repo/owner/repo/ingest"; got != want {
				t.Fatalf("unexpected post path: %s", r.URL.Path)
			}
			var payload RepoBatchIngestRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if payload.Revision != "rev" || payload.PublisherPubKey == "" {
				t.Fatalf("unexpected payload: %+v", payload)
			}
			if len(payload.Files) != 1 || payload.Files[0].Infohash == "" {
				t.Fatalf("unexpected files payload: %+v", payload.Files)
			}
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(`{"ingested":1,"failed":0}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	c := &Client{baseURL: server.URL, httpClient: server.Client()}
	meta := BuildDeterministicTorrentMetadata("owner/repo", "rev", "a.gguf", 1)
	res, err := c.UploadRepoBatchIngest(context.Background(), "owner", "repo", RepoBatchIngestRequest{
		ModelID:         "owner/repo",
		Revision:        "rev",
		PublisherPubKey: strings.Repeat("a", 64),
		Files: []RepoBatchIngestFile{{
			Torrent:      "dG9ycmVudA==",
			Infohash:     meta.Infohash,
			Magnet:       "magnet:?xt=urn:btih:" + meta.Infohash,
			SourceURL:    "https://huggingface.co/owner/repo/resolve/rev/a.gguf",
			LocalHash:    strings.Repeat("b", 64),
			PublisherSig: strings.Repeat("c", 128),
			Timestamp:    "2026-05-26T00:00:00Z",
		}},
	})
	if err != nil {
		t.Fatalf("UploadRepoBatchIngest: %v", err)
	}
	if res.AlreadyExists {
		t.Fatal("did not expect already exists")
	}
	if !sawPost {
		t.Fatal("expected post request")
	}
}
