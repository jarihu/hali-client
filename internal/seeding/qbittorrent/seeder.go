package qbittorrent

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

// Seeder implements seeding.Seeder using qBittorrent WebUI API v2.
// It is safe for concurrent use.
type Seeder struct {
	client     *Client
	torrentDir string
	category   string
	tags       []string
}

// QBittorrentHook wraps a Seeder as a publishing.Hook.
// Each OnTorrentPublished call fires Seed in a goroutine.
type QBittorrentHook struct {
	Seeder *Seeder
}

// NewSeeder creates a Seeder from the given config and torrent directory.
// torrentDir is where Hali stores <infohash>.torrent files.
// Does not attempt to connect — call Seed to trigger the first login.
func NewSeeder(cfg config.QBittorrentConfig, torrentDir string) (*Seeder, error) {
	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Seeder{
		client:     client,
		torrentDir: torrentDir,
		category:   cfg.Category,
		tags:       cfg.Tags,
	}, nil
}

// Seed registers the torrent with qBittorrent so it can seed from contentDir.
// Treats "already registered" as success. Returns an error for all other failures.
func (s *Seeder) Seed(ctx context.Context, infohash, contentDir string) error {
	slog.Debug("qbittorrent: Seed called", "infohash", infohash, "content_dir", contentDir)

	if err := validateInfohash(infohash); err != nil {
		slog.Debug("qbittorrent: invalid infohash", "err", err)
		return err
	}

	if _, err := os.Stat(contentDir); err != nil {
		slog.Warn("qbittorrent: content directory not found", "path", contentDir, "err", err)
		return fmt.Errorf("qbittorrent: content directory not found %s: %w", contentDir, err)
	}
	slog.Debug("qbittorrent: content dir verified", "path", contentDir)

	torrentPath := filepath.Join(s.torrentDir, strings.ToLower(infohash)+".torrent")
	slog.Debug("qbittorrent: looking for torrent file", "path", torrentPath)
	if _, err := os.Stat(torrentPath); err != nil {
		slog.Warn("qbittorrent: torrent file not found", "path", torrentPath, "err", err)
		return fmt.Errorf("qbittorrent: torrent file not found %s: %w", torrentPath, err)
	}
	slog.Debug("qbittorrent: torrent file found", "path", torrentPath)

	slog.Debug("qbittorrent: attempting login", "url", s.client.baseURL)
	if err := s.client.Login(ctx); err != nil {
		slog.Warn("qbittorrent: login failed", "url", s.client.baseURL, "err", err)
		return err
	}
	slog.Debug("qbittorrent: login succeeded")

	slog.Debug("qbittorrent: checking if torrent already registered", "infohash", infohash)
	infos, err := s.client.TorrentInfo(ctx, infohash)
	if err != nil {
		slog.Warn("qbittorrent: TorrentInfo check failed", "err", err)
		return fmt.Errorf("qbittorrent: check existing: %w", err)
	}
	slog.Debug("qbittorrent: TorrentInfo result", "count", len(infos))
	if len(infos) > 0 {
		slog.Info("qbittorrent: torrent already registered", "infohash", infohash)
		return nil
	}

	slog.Debug("qbittorrent: adding torrent", "torrent_path", torrentPath, "save_path", contentDir,
		"category", s.category, "tags", s.tags)
	err = s.client.AddTorrent(ctx, torrentPath, contentDir, s.category, s.tags)
	if errors.Is(err, ErrAlreadyRegistered) {
		slog.Info("qbittorrent: torrent already registered", "infohash", infohash)
		return nil
	}
	if err != nil {
		slog.Warn("qbittorrent: AddTorrent failed", "err", err)
		return err
	}

	slog.Info("qbittorrent: torrent registered for internet seeding", "infohash", infohash)
	return nil
}

// OnTorrentPublished implements publishing.Hook.
// It fires Seed in a new goroutine so it never blocks the publishing pipeline.
func (h *QBittorrentHook) OnTorrentPublished(ctx context.Context, e publishing.TorrentPublishedEvent) {
	slog.Debug("qbittorrent: OnTorrentPublished received", "infohash", e.InfoHash, "content_dir", e.ContentDir)
	go func() {
		if err := h.Seeder.Seed(ctx, e.InfoHash, e.ContentDir); err != nil {
			slog.Warn("qbittorrent: seeding registration failed (non-fatal)", "err", err, "infohash", e.InfoHash)
		}
	}()
}

func validateInfohash(h string) error {
	if len(h) != 40 {
		return fmt.Errorf("qbittorrent: invalid infohash length %d (expected 40)", len(h))
	}
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return fmt.Errorf("qbittorrent: invalid infohash character %q", c)
		}
	}
	return nil
}
