package transmission

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"hali/internal/config"
	"hali/internal/publishing"
)

// Seeder implements seeding.Seeder using the Transmission RPC API.
// It is safe for concurrent use.
type Seeder struct {
	client     *Client
	torrentDir string
}

// TransmissionHook wraps a Seeder as a publishing.Hook.
// Each OnTorrentPublished call fires Seed in a goroutine.
type TransmissionHook struct {
	Seeder *Seeder
}

// NewSeeder creates a Seeder from the given config and torrent directory.
// torrentDir is where Hali stores <infohash>.torrent files.
// Does not attempt to connect — the session handshake happens on first Seed call.
func NewSeeder(cfg config.TransmissionConfig, torrentDir string) (*Seeder, error) {
	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Seeder{
		client:     client,
		torrentDir: torrentDir,
	}, nil
}

// Seed registers the torrent with Transmission so it can seed from contentDir.
// Treats "already registered" as success. Returns an error for all other failures.
func (s *Seeder) Seed(ctx context.Context, infohash, contentDir string) error {
	slog.Debug("transmission: Seed called", "infohash", infohash, "content_dir", contentDir)

	if err := validateInfohash(infohash); err != nil {
		slog.Debug("transmission: invalid infohash", "err", err)
		return err
	}

	if _, err := os.Stat(contentDir); err != nil {
		slog.Warn("transmission: content directory not found", "path", contentDir, "err", err)
		return fmt.Errorf("transmission: content directory not found %s: %w", contentDir, err)
	}
	slog.Debug("transmission: content dir verified", "path", contentDir)

	torrentPath := filepath.Join(s.torrentDir, strings.ToLower(infohash)+".torrent")
	slog.Debug("transmission: looking for torrent file", "path", torrentPath)
	if _, err := os.Stat(torrentPath); err != nil {
		slog.Warn("transmission: torrent file not found", "path", torrentPath, "err", err)
		return fmt.Errorf("transmission: torrent file not found %s: %w", torrentPath, err)
	}
	slog.Debug("transmission: torrent file found", "path", torrentPath)

	err := s.client.AddTorrent(ctx, torrentPath, contentDir)
	if errors.Is(err, ErrDuplicateTorrent) {
		slog.Info("transmission: torrent already registered", "infohash", infohash)
		return nil
	}
	if err != nil {
		slog.Warn("transmission: AddTorrent failed", "err", err)
		return err
	}

	slog.Info("transmission: torrent registered for internet seeding", "infohash", infohash)
	return nil
}

// OnTorrentPublished implements publishing.Hook.
// It fires Seed in a new goroutine so it never blocks the publishing pipeline.
func (h *TransmissionHook) OnTorrentPublished(ctx context.Context, e publishing.TorrentPublishedEvent) {
	slog.Debug("transmission: OnTorrentPublished received", "infohash", e.InfoHash, "content_dir", e.ContentDir)
	go func() {
		if err := h.Seeder.Seed(ctx, e.InfoHash, e.ContentDir); err != nil {
			slog.Warn("transmission: seeding registration failed (non-fatal)", "err", err, "infohash", e.InfoHash)
		}
	}()
}

func validateInfohash(h string) error {
	if len(h) != 40 {
		return fmt.Errorf("transmission: invalid infohash length %d (expected 40)", len(h))
	}
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return fmt.Errorf("transmission: invalid infohash character %q", c)
		}
	}
	return nil
}
