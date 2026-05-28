package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"hali/internal/cache"
	"hali/internal/config"
	"hali/internal/daemon"
	"hali/internal/model"

	"github.com/spf13/cobra"
)

var networkCmd = &cobra.Command{
	Use:   "network",
	Short: "Inspect networking mode and capabilities",
}

var networkStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show effective networking mode and capabilities",
	RunE:  runNetworkStatus,
}

var networkSeenPull bool

var networkSeenCmd = &cobra.Command{
	Use:   "seen",
	Short: "List models observed from LAN announcements",
	RunE:  runNetworkSeen,
}

var networkPullCmd = &cobra.Command{
	Use:   "pull [model_id]",
	Short: "Pull a model directly from observed LAN announcements",
	Long: `Pull a model from LAN announcements.

If model_id is omitted, this command shows observed LAN models and prompts for a selection.
If model_id is provided, it attempts that model directly from LAN observations only.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runNetworkPull,
}

func runNetworkStatus(_ *cobra.Command, _ []string) error {
	if !daemon.IsRunning() {
		fmt.Println("Daemon is not running.")
		return nil
	}
	resp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdStatus})
	if err != nil || !resp.OK {
		if err != nil {
			return err
		}
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return err
	}
	var status daemon.StatusData
	if err := json.Unmarshal(raw, &status); err != nil {
		return err
	}

	fmt.Printf("Mode: %s\n\n", status.Network.Mode)
	fmt.Println("Capabilities:")
	fmt.Printf("- LSD: %s\n", enabledDisabled(status.Network.Capabilities.LSD))
	return nil
}

func runNetworkSeen(cmd *cobra.Command, _ []string) error {
	ctx, stop := withInterruptContext(cmd.Context())
	defer stop()

	rows, err := fetchLANSeenRows()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("No recent LAN announcements observed.")
		return nil
	}
	if networkSeenPull {
		idx, err := pickLANRow(rows)
		if err != nil {
			return err
		}
		return runNetworkPullFromSeen(ctx, rows[idx])
	}

	const (
		revCol = 10
	)
	modelCol := len("MODEL")
	for _, row := range rows {
		if l := len(row.ModelID); l > modelCol {
			modelCol = l
		}
	}
	fmt.Printf("%-*s  %-*s  %-5s  %s\n", modelCol, "MODEL", revCol, "REVISION", "PEERS", "LAST SEEN")
	fmt.Printf("%-*s  %-*s  %-5s  %s\n", modelCol, strings.Repeat("-", modelCol), revCol, strings.Repeat("-", revCol), "-----", "---------")
	for _, row := range rows {
		rev := strings.TrimSpace(row.Revision)
		if rev == "" {
			rev = "-"
		}
		endpoints := "-"
		if len(row.PeerAddrs) > 0 {
			endpoints = strings.Join(row.PeerAddrs, ",")
		}
		fmt.Printf("%-*s  %-*s  %-5d  %s ago\n", modelCol, row.ModelID, revCol, truncateCol(rev, revCol), row.PeerCount, humanizeAgeUnix(row.LastSeen))
		fmt.Printf("  endpoints: %s\n", endpoints)
	}

	return nil
}

func runNetworkPull(cmd *cobra.Command, args []string) error {
	ctx, stop := withInterruptContext(cmd.Context())
	defer stop()

	rows, err := fetchLANSeenRows()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("no recent LAN announcements observed")
	}

	if len(args) == 1 {
		best := -1
		for i, row := range rows {
			if row.ModelID != args[0] {
				continue
			}
			if best < 0 || row.LastSeen > rows[best].LastSeen {
				best = i
			}
		}
		if best < 0 {
			return fmt.Errorf("model %q not found in recent LAN announcements", args[0])
		}
		return runNetworkPullFromSeen(ctx, rows[best])
	}

	idx, err := pickLANRow(rows)
	if err != nil {
		return err
	}
	return runNetworkPullFromSeen(ctx, rows[idx])
}

func runNetworkPullFromSeen(ctx context.Context, row daemon.LanSeenEntry) error {
	id, err := model.Parse(row.ModelID)
	if err != nil {
		return fmt.Errorf("invalid LAN model id %q: %w", row.ModelID, err)
	}

	store := cache.NewStore()
	if daemonAliasesService() {
		store = cache.NewStoreAt(config.ServiceModelsDir())
	}

	if store.Has(id) {
		fmt.Printf("Already downloaded: %s\n", id)
		return nil
	}
	if !daemon.IsRunning() {
		return fmt.Errorf("daemon is not running")
	}

	ih := strings.TrimSpace(row.Infohash)
	if ih == "" {
		return fmt.Errorf("selected LAN announcement has no infohash")
	}
	hfRepo := strings.TrimSpace(row.HFRepo)
	hfRevision := strings.TrimSpace(row.Revision)
	if hfRepo == "" {
		qResp, qErr := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdLanQuery, ModelID: id.String()})
		if qErr == nil && qResp.OK && qResp.Data != nil {
			qRaw, mErr := json.Marshal(qResp.Data)
			if mErr == nil {
				var q daemon.LanQueryData
				if uErr := json.Unmarshal(qRaw, &q); uErr == nil {
					hfRepo = strings.TrimSpace(q.HFRepo)
					if hfRevision == "" {
						hfRevision = strings.TrimSpace(q.Revision)
					}
				}
			}
		}
	}
	if hfRepo == "" || hfRevision == "" {
		return fmt.Errorf("selected LAN announcement lacks required provenance (hf_repo/hf_revision); upgrade/restart source peer so announcements include repo and revision, then retry")
	}
	fmt.Printf("\nFound on LAN (infohash %s)\n", ih[:8]+"…")
	if len(row.PeerAddrs) > 0 {
		fmt.Printf("Direct peer endpoints: %s\n", strings.Join(row.PeerAddrs, ", "))
	} else {
		fmt.Println("Direct peer endpoints: none observed (waiting on LSD/passive discovery)")
	}
	fmt.Printf("Downloading %s from LAN peers\n", id)

	modelDir := store.Dir(id)
	dc := daemon.DefaultClient()
	startResp, err := dc.Send(daemon.Request{
		Cmd:        daemon.CmdDownload,
		ModelID:    id.String(),
		Infohash:   row.Infohash,
		InfohashV2: row.InfohashV2,
		PeerAddrs:  row.PeerAddrs,
		Dir:        modelDir,
	})
	if err != nil {
		return fmt.Errorf("LAN download failed: %w", err)
	}
	if !startResp.OK {
		reason := strings.TrimSpace(startResp.Error)
		if reason == "" {
			reason = "daemon rejected download request"
		}
		return fmt.Errorf("LAN download failed: %s", reason)
	}

	raw, err := json.Marshal(startResp.Data)
	if err != nil {
		return err
	}
	var jobData struct {
		JobID     string   `json:"job_id"`
		PeerAddrs []string `json:"peer_addrs"`
	}
	if err := json.Unmarshal(raw, &jobData); err != nil {
		return err
	}
	jobID := jobData.JobID
	if strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("LAN download failed: missing job id")
	}
	if len(jobData.PeerAddrs) > 0 {
		fmt.Printf("Injected direct peers: %s\n", strings.Join(jobData.PeerAddrs, ", "))
	} else {
		fmt.Println("Injected direct peers: none")
	}

	metaWaitTicks := 0
	_, err = FinishDownloadJob(ctx, dc, jobID, modelDir, id, store, hfRepo, hfRevision, row.Infohash, row.InfohashV2, func(js daemon.JobStatusData) {
		metaWaitTicks++
		if metaWaitTicks == 1 || metaWaitTicks%5 == 0 {
			fmt.Printf("\nWaiting for metadata (active=%d pending=%d half-open=%d total=%d)\n", js.ActivePeers, js.PendingPeers, js.HalfOpenPeers, js.TotalPeers)
		}
	})
	return err
}

func pickLANRow(rows []daemon.LanSeenEntry) (int, error) {

	labels := make([]string, 0, len(rows))
	for _, row := range rows {
		rev := strings.TrimSpace(row.Revision)
		if rev == "" {
			rev = "-"
		}
		endpoints := "-"
		if len(row.PeerAddrs) > 0 {
			endpoints = strings.Join(row.PeerAddrs, ",")
		}
		labels = append(labels, fmt.Sprintf("%-42s  rev=%-10s  peers=%d  seen=%s ago  ep=%s", row.ModelID, rev, row.PeerCount, humanizeAgeUnix(row.LastSeen), endpoints))
	}

	fmt.Println("Observed on LAN:")
	idx, err := pickOne("Select model", labels)
	if err != nil {
		return 0, err
	}
	return idx, nil
}

func fetchLANSeenRows() ([]daemon.LanSeenEntry, error) {
	if !daemon.IsRunning() {
		return nil, fmt.Errorf("daemon is not running")
	}
	resp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdLanSeen})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, err
	}
	var seen daemon.LanSeenData
	if err := json.Unmarshal(raw, &seen); err != nil {
		return nil, err
	}
	return seen.Announcements, nil
}

func enabledDisabled(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}

func truncateCol(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return s[:width-1] + "…"
}

func configureNetworkFlags() {
	networkSeenCmd.Flags().BoolVarP(&networkSeenPull, "pull", "p", false, "after listing LAN announcements, choose one and run pull")
}
