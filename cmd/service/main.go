package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"hali/internal/buildinfo"
	"hali/internal/cache"
	"hali/internal/config"
	"hali/internal/daemon"
	"hali/internal/torrent"
	"hali/internal/winsvc"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "version":
			fmt.Printf("halid %s (%s) %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildMode)
			return
		}
	}

	debug := len(os.Args) > 1 && os.Args[1] == "--debug"

	os.MkdirAll(config.ServiceLogDir(), 0755)
	cfg, _ := config.LoadService()
	daemon.InitLogging(config.ServiceLogDir(), debug || (cfg.DebugValue()))

	isService, err := winsvc.IsWindowsService()
	if err != nil {
		fmt.Fprintf(os.Stderr, "halid: %v\n", err)
		os.Exit(1)
	}

	srv, err := buildServer()
	if err != nil {
		slog.Error("init failed", "error", err)
		os.Exit(1)
	}

	if isService && !debug {
		if err := winsvc.RunAsService(srv.Run, srv.Stop); err != nil {
			slog.Error("service exited", "error", err)
			os.Exit(1)
		}
		return
	}

	// Foreground mode (--debug or non-Windows).
	checkPortCollision()
	slog.Info("halid starting", "version", buildinfo.Version, "commit", buildinfo.Commit, "mode", buildinfo.BuildMode)
	if err := srv.Run(); err != nil {
		slog.Error("daemon exited", "error", err)
		os.Exit(1)
	}
}

// buildServer creates the daemon Server (does not start listeners).
func buildServer() (*daemon.Server, error) {
	dataDir := config.ServiceDataDir()
	if err := config.EnsureServiceConfigMaterialized(); err != nil {
		return nil, fmt.Errorf("materialize service config: %w", err)
	}
	if err := os.MkdirAll(config.ServiceLogDir(), 0755); err != nil {
		return nil, fmt.Errorf("create logs dir: %w", err)
	}
	if err := os.MkdirAll(config.ServiceRunDir(), 0755); err != nil {
		return nil, fmt.Errorf("create run dir: %w", err)
	}
	for _, sub := range []string{"torrents", "cache"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0755); err != nil {
			return nil, fmt.Errorf("create %s dir: %w", sub, err)
		}
	}
	if err := os.MkdirAll(config.ServiceModelsDir(), 0755); err != nil {
		return nil, fmt.Errorf("create models dir: %w", err)
	}

	torrentDir := filepath.Join(dataDir, "torrents")
	store := cache.NewStoreAt(config.ServiceModelsDir())
	engine, err := torrent.NewEngine(store.Root, torrentDir)
	if err != nil {
		return nil, fmt.Errorf("init torrent engine: %w", err)
	}

	stats := torrent.NewStatsCollector(engine)
	return daemon.NewServer(engine, store, stats), nil
}

// checkPortCollision exits with a user-friendly message if another daemon is already running.
func checkPortCollision() {
	addr := fmt.Sprintf("127.0.0.1:%d", config.HTTPPort)
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		fmt.Fprintln(os.Stderr, "Hali daemon is already running.")
		fmt.Fprintln(os.Stderr, "Run 'hali service status' to check its state.")
		os.Exit(1)
	}
}
