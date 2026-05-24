package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"hali/internal/daemon"
	"hali/internal/torrent"

	"github.com/spf13/cobra"
)

var statsWebFlag bool

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show download/upload statistics",
	Long: `Show transfer statistics from the running daemon.

Displays current speeds, session totals, and per-model breakdown.
Use --web to open a live browser dashboard instead.

Examples:
  hali stats
  hali stats --web`,
	RunE: runStats,
}

func configureStatsFlags() {
	statsCmd.Flags().BoolVarP(&statsWebFlag, "web", "w", false, "open live web dashboard in browser")
}

func runStats(_ *cobra.Command, _ []string) error {
	if statsWebFlag {
		return runStatsWeb()
	}

	if !daemon.IsRunning() {
		fmt.Println("Daemon is not running.")
		return nil
	}

	client := daemon.DefaultClient()
	resp, err := client.Send(daemon.Request{Cmd: daemon.CmdStats})
	if err != nil {
		fmt.Println("Daemon is not running.")
		return nil
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return fmt.Errorf("marshaling stats: %w", err)
	}
	var snap torrent.StatsSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return fmt.Errorf("parsing stats: %w", err)
	}

	printStats(snap)
	return nil
}

func runStatsWeb() error {
	if !daemon.IsRunning() {
		fmt.Println("Daemon is not running.")
		return nil
	}
	url := fmt.Sprintf("http://%s", daemon.HTTPAddr())
	fmt.Println("Opening", url)
	openBrowser(url)
	return nil
}

func printStats(s torrent.StatsSnapshot) {
	fmt.Printf("Daemon:   running  (uptime %s)\n", s.Uptime)
	fmt.Printf("Speeds:   ↓ %s   ↑ %s\n", fmtSpeed(s.DownSpeed), fmtSpeed(s.UpSpeed))
	fmt.Printf("Session:  ↓ %s   ↑ %s\n", fmtBytes(s.TotalDown), fmtBytes(s.TotalUp))

	if len(s.Models) == 0 {
		fmt.Println("\nNo active torrents.")
		return
	}

	fmt.Println()
	fmt.Printf("%-44s  %-12s  %-10s  %-10s  %s\n", "MODEL", "STATUS", "↓ SPEED", "↑ SPEED", "PEERS")
	fmt.Printf("%s  %s  %s  %s  %s\n",
		strings.Repeat("-", 44), strings.Repeat("-", 12),
		strings.Repeat("-", 10), strings.Repeat("-", 10), "-----")
	for _, m := range s.Models {
		status := m.Status
		if m.Status == "downloading" && m.Progress > 0 {
			status = fmt.Sprintf("dl %d%%", m.Progress)
		}
		dSpeed := "—"
		if m.DownSpeed > 0 {
			dSpeed = fmtSpeed(m.DownSpeed)
		}
		uSpeed := "—"
		if m.UpSpeed > 0 {
			uSpeed = fmtSpeed(m.UpSpeed)
		}
		fmt.Printf("%-44s  %-12s  %-10s  %-10s  %d\n",
			truncateID(m.ModelID, 44), status, dSpeed, uSpeed, m.Peers)
	}
}

func fmtSpeed(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B/s", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB/s", float64(n)/1024)
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB/s", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.2f GB/s", float64(n)/(1<<30))
	}
}

func fmtBytes(n int64) string {
	switch {
	case n == 0:
		return "0 B"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	}
}

func truncateID(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func openBrowser(url string) {
	// Validate URL is http/https before passing to any external process.
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// explorer.exe is not a shell: does not interpret metacharacters.
		cmd = exec.Command("explorer.exe", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start() //nolint:errcheck
}
