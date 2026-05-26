package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"hali/internal/cache"
	"hali/internal/config"
	"hali/internal/daemon"
	"hali/internal/torrent"

	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the hali daemon",
	Long: `Manage the hali background daemon.

The daemon seeds your downloaded models to other machines on the LAN
and handles LAN peer discovery. It runs as a background process and
persists across terminal sessions.

Subcommands:
  start   — launch the daemon
  stop    — shut it down
  status  — show PID, uptime, seeding list, and visible LAN peers`,
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the daemon",
	Long: `Start the hali daemon in the background.

The daemon seeds all locally cached models via BitTorrent and announces
them to peers on the LAN.

If the daemon is already running this is a no-op.

Example:
  hali daemon start`,
	RunE: runDaemonStart,
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the daemon",
	Long: `Stop the running hali daemon.

Sends a stop command over IPC and waits for the process to exit.
If the daemon is not running this is a no-op.

Example:
  hali daemon stop`,
	RunE: runDaemonStop,
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status and seeding info",
	Long: `Show the current daemon status.

Prints PID, uptime, BitTorrent port, a list of models being seeded,
and any models discovered on the LAN.

Example:
  hali daemon status`,
	RunE: runDaemonStatus,
}

var daemonRunCmd = &cobra.Command{
	Use:    "_run",
	Hidden: true,
	RunE:   runDaemonRun,
}

func runDaemonStart(_ *cobra.Command, _ []string) error {
	if daemonAliasesService() {
		if err := serviceStartAction(); err != nil {
			return err
		}
		fmt.Println("Service started.")
		return nil
	}

	if daemon.IsRunning() {
		fmt.Println("Daemon is already running.")
		return nil
	}
	fmt.Print("Starting daemon...")
	if err := daemon.Launch(); err != nil {
		fmt.Println(" failed.")
		return err
	}

	if !daemon.IsRunning() {
		return fmt.Errorf("daemon started but not responding")
	}
	fmt.Println(" done.")
	if cfg, err := config.LoadService(); err == nil && cfg.LANHMACEnabledValue() {
		fmt.Println("LAN HMAC auth is enabled. Peers must use matching signatures/shared secret.")
	}
	return nil
}

func runDaemonStop(_ *cobra.Command, _ []string) error {
	if daemonAliasesService() {
		if err := serviceStopAction(); err != nil {
			return err
		}
		fmt.Println("Service stopped.")
		return nil
	}

	if !daemon.IsRunning() {
		fmt.Println("Daemon is not running.")
		return nil
	}
	client := daemon.DefaultClient()
	resp, err := client.Send(daemon.Request{Cmd: daemon.CmdStop})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	fmt.Println("Daemon stopped.")
	return nil
}

func runDaemonStatus(_ *cobra.Command, _ []string) error {
	if daemonAliasesService() {
		status, err := serviceStatusAction()
		if err != nil {
			return err
		}
		fmt.Println(status)

		if daemon.IsRunning() {
			client := daemon.DefaultClient()
			resp, err := client.Send(daemon.Request{Cmd: daemon.CmdStatus})
			if err == nil && resp.OK {
				raw, err := json.Marshal(resp.Data)
				if err == nil {
					var ds daemon.StatusData
					if err := json.Unmarshal(raw, &ds); err == nil {
						fmt.Printf("Torrent listening port: %d\n", ds.Port)
					}
				}
			}
		}
		return nil
	}

	if !daemon.IsRunning() {
		fmt.Println("Daemon is not running.")
		return nil
	}
	client := daemon.DefaultClient()
	resp, err := client.Send(daemon.Request{Cmd: daemon.CmdStatus})
	if err != nil {
		fmt.Println("Daemon is not running.")
		return nil
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return fmt.Errorf("marshaling status data: %w", err)
	}
	var status daemon.StatusData
	if err := json.Unmarshal(raw, &status); err != nil {
		return fmt.Errorf("parsing status: %w", err)
	}

	fmt.Printf("Daemon running  PID %d  uptime %s  port %d\n", status.PID, status.Uptime, status.Port)
	fmt.Printf("Network mode: %s  LSD: %s\n", status.Network.Mode, enabledDisabled(status.Network.Capabilities.LSD))
	if cfg, err := config.LoadService(); err == nil && cfg.LANHMACEnabledValue() {
		fmt.Println("LAN HMAC auth: enabled")
	}

	if len(status.Seeding) == 0 {
		fmt.Println("\nNo models seeding yet.")
	} else {
		fmt.Printf("\n%-42s  %-8s  %s\n", "SEEDING", "STATUS", "PEERS")
		fmt.Printf("%-42s  %-8s  %s\n", strings.Repeat("-", 42), "--------", "-----")
		for _, s := range status.Seeding {
			peers := s.Peers
			if peers == "" {
				peers = "—"
			}
			fmt.Printf("%-42s  %-8s  %s\n", s.ModelID, s.Status, peers)
			if s.MagnetURI != "" {
				fmt.Printf("  magnet  %s\n", s.MagnetURI)
			}
		}
	}

	if len(status.LAN) > 0 {
		fmt.Printf("\n%-42s  %-8s  %s\n", "LAN AVAILABLE", "PEERS", "INFOHASH")
		fmt.Printf("%-42s  %-8s  %s\n", strings.Repeat("-", 42), "-----", "--------")
		for _, l := range status.LAN {
			ih := l.Infohash
			if len(ih) > 12 {
				ih = ih[:12] + "…"
			}
			fmt.Printf("%-42s  %-8d  %s\n", l.ModelID, l.Peers, ih)
		}
	}
	return nil
}

// runDaemonRun is the internal entry point for the daemon process.
func runDaemonRun(_ *cobra.Command, _ []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("HOME not available: %w", err)
	}
	base := filepath.Join(home, ".hali")
	if err := os.MkdirAll(base, 0700); err != nil {
		return fmt.Errorf("cannot write to ~/.hali: %w", err)
	}
	os.Chown(base, os.Getuid(), os.Getgid())

	dataDir := config.ServiceDataDir()
	if err := os.MkdirAll(config.ServiceLogDir(), 0755); err != nil {
		return err
	}
	cfg, _ := config.LoadService()
	daemon.InitLogging(config.ServiceLogDir(), cfg.DebugValue())

	if err := os.MkdirAll(filepath.Join(dataDir, "cache"), 0755); err != nil {
		return err
	}

	store := cache.NewStoreAt(config.ServiceModelsDir())
	torrentDir := filepath.Join(dataDir, "torrents")

	engine, err := torrent.NewEngine(store.Root, torrentDir)
	if err != nil {
		return fmt.Errorf("starting torrent engine: %w", err)
	}
	defer engine.Close()

	stats := torrent.NewStatsCollector(engine)
	srv := daemon.NewServer(engine, store, stats)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		srv.Stop()
	}()

	err = srv.Run()
	if errors.Is(err, daemon.ErrRestartRequested) {
		if launchErr := daemon.Launch(); launchErr != nil {
			return fmt.Errorf("restart after config change: %w", launchErr)
		}
		return nil
	}
	return err
}
