package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hali/internal/cache"
	"hali/internal/config"
	"hali/internal/daemon"
	"hali/internal/events"
	"hali/internal/hf"
	"hali/internal/model"
	"hali/internal/pull"
	"hali/internal/torrent"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var pullCmd = &cobra.Command{
	Use:   "pull <model>",
	Short: "Download a model",
	Long: `Download a model from Hugging Face (or LAN peers if available).

<model> can be:
	- a search query:              hali pull mistral
	- a Hugging Face repo ID:      hali pull TheBloke/Mistral-7B-Instruct-v0.2-GGUF
	- a canonical model ID:        hali pull mistral:7b:instruct:q4_k_m

By default this command is interactive when multiple choices exist.

Automation:
	--model-index N        Select Nth model match without prompt
	--file-index N         Select Nth file variant without prompt
	--file-name NAME       Select exact file variant without prompt
	--non-interactive      Disable prompts globally
	--json                 JSON mode for machine-readable integrations

Examples:
	hali pull mistral --model-index 1 --file-index 1 --non-interactive
	hali pull TheBloke/Mistral-7B-Instruct-v0.2-GGUF --file-name mistral-7b-instruct-v0.2.Q4_K_M.gguf --non-interactive
	hali pull mistral:7b:instruct:q4_k_m --non-interactive`,
	Args: cobra.ExactArgs(1),
	RunE: runPull,
}

var (
	pullModelIndex int
	pullFileIndex  int
	pullFileName   string
)

func configurePullFlags() {
	pullCmd.Flags().IntVar(&pullModelIndex, "model-index", 0, "Select the Nth model search match (1-based) without prompting")
	pullCmd.Flags().IntVar(&pullFileIndex, "file-index", 0, "Select the Nth file variant (1-based) without prompting")
	pullCmd.Flags().StringVar(&pullFileName, "file-name", "", "Select an exact file variant name without prompting")
}

func runPull(cmd *cobra.Command, args []string) error {
	repo, fileName := parseRepoArg(args[0])
	if pullFileName != "" {
		fileName = pullFileName
	}
	return runPullWithOptions(cmd, pull.Options{
		Repo:           repo,
		Revision:       "",
		FileName:       fileName,
		NonInteractive: nonInteractive,
	})
}

// parseRepoArg splits a raw pull argument into repo and an optional file name.
// It handles the ?file= query param convention (e.g. "owner/repo?file=model.gguf").
func parseRepoArg(raw string) (repo, fileName string) {
	i := strings.Index(raw, "?")
	if i < 0 {
		return raw, ""
	}
	repo = raw[:i]
	for _, kv := range strings.Split(raw[i+1:], "&") {
		if v, ok := strings.CutPrefix(kv, "file="); ok {
			return repo, v
		}
	}
	return repo, ""
}

func runPullWithOptions(cmd *cobra.Command, opts pull.Options) error {
	if opts.NonInteractive {
		orig := nonInteractive
		nonInteractive = true
		defer func() { nonInteractive = orig }()
	}

	query := opts.Repo
	ctx, stop := withInterruptContext(cmd.Context())
	defer stop()
	store := cache.NewStore()
	if daemonAliasesService() {
		store = cache.NewStoreAt(config.ServiceModelsDir())
	}
	client := hf.NewClient()

	_ = config.EnsureModelsDirPersisted()

	// If input is a canonical model ID, check local cache and try LAN first.
	if id, err := model.Parse(query); err == nil {
		if store.Has(id) {
			fmt.Printf("Already downloaded: %s\n", id)
			return nil
		}
		ok, lanErr := tryLanDownload(ctx, id, store, "", "", "")
		if lanErr != nil {
			return lanErr
		}
		if ok {
			return nil
		}
	}

	// Resolve to a HF repo ID.
	repoID, err := resolveRepo(ctx, client, query)
	if err != nil {
		return err
	}

	// Fetch GGUF file list.
	fmt.Printf("\nFetching file list for %s...\n\n", repoID)
	files, revision, err := client.GetFiles(ctx, repoID, opts.Revision)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no GGUF files found in %s", repoID)
	}
	lanObserved := fetchLANObserved()

	labels := make([]string, len(files))
	for i, f := range files {
		sizeStr := "unknown size"
		if f.Size > 0 {
			sizeStr = cache.FormatSize(f.Size)
		}
		suffix := ""
		if i == 0 {
			suffix = "  ← smallest"
		}

		id := model.FromHF(repoID, f.Name)
		if !id.IsZero() {
			id.Revision = revision
			if obs, ok := lanObserved[artifactKeyForPull(id.String(), revision)]; ok {
				suffix += fmt.Sprintf("  [LAN observed: %d peer(s), seen %s ago]", obs.PeerCount, humanizeAgeUnix(obs.LastSeen))
			}
		}

		labels[i] = fmt.Sprintf("%-60s  %s%s", f.Name, sizeStr, suffix)
	}
	fmt.Println("Available files:")
	idx := -1
	if requested := strings.TrimSpace(opts.FileName); requested != "" {
		for i, f := range files {
			if strings.EqualFold(strings.TrimSpace(f.Name), requested) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("file %q not found in available variants", requested)
		}
		fmt.Printf("Using file variant: %s\n", files[idx].Name)
	} else {
		picked, err := pickOneWithMode("Select file", labels, pullFileIndex)
		if err != nil {
			return err
		}
		idx = picked
	}
	selected := files[idx]

	// Derive canonical model ID.
	id := model.FromHF(repoID, selected.Name)
	if id.IsZero() {
		quant := strings.TrimSuffix(strings.ToLower(selected.Name), ".gguf")
		quant = filepath.Base(quant)
		size := inferFallbackSize(repoID, selected.Name)
		id = model.ModelID{
			Base:     buildFallbackBase(repoID, size),
			Size:     size,
			Variant:  "base",
			Quant:    quant,
			Format:   "gguf",
			Revision: revision,
		}
	} else {
		id.Revision = revision
	}

	if store.Has(id) {
		fmt.Printf("\nAlready downloaded: %s\n", id)
		return nil
	}

	// Check LAN before going to HF.
	if !id.IsZero() {
		ok, lanErr := tryLanDownload(ctx, id, store, selected.Name, repoID, revision)
		if lanErr != nil {
			return lanErr
		}
		if ok {
			return nil
		}
	}

	// HF download. When ENABLE_STREAMING_HASH=true, piece hashes are computed
	// concurrently with the write so the daemon can seed without re-reading the file.
	fmt.Printf("\nPulling %s from Hugging Face\n", id)
	var (
		written int64
		pieces  []byte
	)
	snapshotHasher := sha256.New()
	streamingHashEnabled, err := config.StreamingHashEnabled()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if streamingHashEnabled && selected.Size > 0 {
		// Piece size must match what buildHybridSingleFileInfo will use (choosePieceSize(fileSize)).
		// selected.Size is the declared HF file size; fileSize after download equals written.
		// If selected.Size is unknown we skip streaming hash and let the daemon re-hash.
		pieceLen := torrent.ChoosePieceSize(selected.Size)
		ph := torrent.NewPieceHasher(pieceLen)
		written, err = client.Download(ctx, repoID, selected.Name, revision, store.Dir(id), printProgress, io.MultiWriter(ph, snapshotHasher))
		if err == nil {
			pieces, err = ph.Finalize()
		}
	} else {
		written, err = client.Download(ctx, repoID, selected.Name, revision, store.Dir(id), printProgress, snapshotHasher)
	}
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	fmt.Println()
	snapshotHash := hex.EncodeToString(snapshotHasher.Sum(nil))

	meta := cache.Metadata{
		HFRepo:     repoID,
		HFRevision: revision,
		HFSnapshot: snapshotHash,
		Files:      []string{selected.Name},
		Size:       written,
	}
	if err := store.Save(id, meta); err != nil {
		return fmt.Errorf("saving metadata: %w", err)
	}
	fmt.Printf("Saved %s  (%s)\n", id, cache.FormatSize(written))
	fmt.Printf("  model:    %s\n", filepath.Join(store.Dir(id), selected.Name))
	fmt.Printf("  metadata: %s\n", filepath.Join(store.Dir(id), "metadata.json"))
	if raw := strings.TrimSpace(os.Getenv("HALI_CACHE_MAX_BYTES")); raw != "" {
		if maxBytes, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil && maxBytes > 0 {
			res, evictErr := store.EvictLRU(maxBytes)
			if evictErr != nil {
				fmt.Printf("Warning: cache eviction failed: %v\n", evictErr)
			} else if res.EvictedModels > 0 {
				fmt.Printf("Evicted %d cached model(s), freed %s\n", res.EvictedModels, cache.FormatSize(res.BytesFreed))
			}
		}
	}

	notifyDaemonSeed(ctx, id.String(), store.Dir(id), selected.Name, repoID, revision, snapshotHash, pieces, written, id.Quant)
	return nil
}

type lanObservedFact struct {
	PeerCount int
	LastSeen  int64
}

func fetchLANObserved() map[string]lanObservedFact {
	out := make(map[string]lanObservedFact)
	if !daemon.IsRunning() {
		return out
	}

	dc := daemon.DefaultClient()
	resp, err := dc.Send(daemon.Request{Cmd: daemon.CmdLanSeen})
	if err != nil || !resp.OK || resp.Data == nil {
		return out
	}

	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return out
	}
	var seen daemon.LanSeenData
	if err := json.Unmarshal(raw, &seen); err != nil {
		return out
	}

	for _, row := range seen.Announcements {
		if row.ModelID == "" || row.PeerCount <= 0 || row.LastSeen <= 0 {
			continue
		}
		k := strings.TrimSpace(row.ArtifactKey)
		if k == "" {
			k = artifactKeyForPull(row.ModelID, row.Revision)
		}
		out[k] = lanObservedFact{PeerCount: row.PeerCount, LastSeen: row.LastSeen}
	}
	return out
}

func artifactKeyForPull(modelID, revision string) string {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return modelID
	}
	return modelID + "@" + revision
}

func humanizeAgeUnix(ts int64) string {
	if ts <= 0 {
		return "unknown"
	}
	age := time.Since(time.Unix(ts, 0))
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(age.Hours()))
	}
}

var fallbackNumericSize = regexp.MustCompile(`(?i)\d+\.?\d*[bm]`)

func tokenizeModelString(s string) []string {
	lower := strings.ToLower(strings.TrimSpace(s))
	if idx := strings.Index(lower, "/"); idx >= 0 {
		lower = lower[idx+1:]
	}
	return strings.FieldsFunc(lower, func(r rune) bool {
		isLetter := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		return !(isLetter || isDigit || r == '.')
	})
}

func inferFallbackSize(repoID, filename string) string {
	if m := fallbackNumericSize.FindString(strings.ToLower(filename)); m != "" {
		return m
	}
	if m := fallbackNumericSize.FindString(strings.ToLower(repoID)); m != "" {
		return m
	}

	known := map[string]struct{}{
		"tiny": {}, "nano": {}, "micro": {}, "mini": {}, "small": {},
		"medium": {}, "med": {}, "large": {}, "xl": {}, "xxl": {},
	}
	for _, tok := range tokenizeModelString(filename + " " + repoID) {
		if _, ok := known[tok]; ok {
			return tok
		}
	}
	return "base"
}

func buildFallbackBase(repoID, size string) string {
	short := strings.TrimSpace(repoID)
	if idx := strings.LastIndex(short, "/"); idx >= 0 {
		short = short[idx+1:]
	}
	tokens := tokenizeModelString(short)
	baseTokens := make([]string, 0, len(tokens))
	removedSize := false
	for _, tok := range tokens {
		if tok == "gguf" {
			continue
		}
		if !removedSize && tok == size {
			removedSize = true
			continue
		}
		baseTokens = append(baseTokens, tok)
	}
	if len(baseTokens) == 0 {
		return "model"
	}
	return strings.Join(baseTokens, "_")
}

// tryLanDownload checks whether any LAN peer has the model and, if so,
// downloads it via torrent. Returns true on success.
func tryLanDownload(ctx context.Context, id model.ModelID, store *cache.Store, filename, hfRepo, hfRevision string) (bool, error) {
	if !daemon.IsRunning() {
		return false, nil
	}

	dc := daemon.DefaultClient()
	resp, err := dc.Send(daemon.Request{Cmd: daemon.CmdLanQuery, ModelID: id.String()})
	if err != nil || !resp.OK || resp.Data == nil {
		return false, nil
	}

	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return false, nil
	}
	var lq daemon.LanQueryData
	if err := json.Unmarshal(raw, &lq); err != nil || lq.Infohash == "" {
		return false, nil
	}
	effectiveHFRepo := strings.TrimSpace(hfRepo)
	if effectiveHFRepo == "" {
		effectiveHFRepo = strings.TrimSpace(lq.HFRepo)
	}
	effectiveHFRevision := strings.TrimSpace(hfRevision)
	if effectiveHFRevision == "" {
		effectiveHFRevision = strings.TrimSpace(lq.Revision)
	}
	if effectiveHFRepo == "" || effectiveHFRevision == "" {
		fmt.Printf("LAN candidate missing provenance (hf_repo=%t hf_revision=%t) — falling back to HF\n", effectiveHFRepo != "", effectiveHFRevision != "")
		return false, nil
	}

	fmt.Printf("\nFound on LAN (infohash %s)\n", lq.Infohash[:8]+"…")
	if len(lq.PeerAddrs) > 0 {
		fmt.Printf("Direct peer endpoints: %s\n", strings.Join(lq.PeerAddrs, ", "))
	} else {
		fmt.Println("Direct peer endpoints: none observed (waiting on LSD/passive discovery)")
	}
	fmt.Printf("Downloading %s from LAN peers\n", id)

	modelDir := store.Dir(id)
	startResp, err := dc.Send(daemon.Request{
		Cmd:        daemon.CmdDownload,
		ModelID:    id.String(),
		Infohash:   lq.Infohash,
		InfohashV2: lq.InfohashV2,
		PeerAddrs:  lq.PeerAddrs,
		Dir:        modelDir,
	})
	if err != nil {
		fmt.Printf("LAN download failed: %v — falling back to HF\n", err)
		return false, nil
	}
	if !startResp.OK {
		reason := strings.TrimSpace(startResp.Error)
		if reason == "" {
			reason = "daemon rejected download request"
		}
		fmt.Printf("LAN download failed: %s — falling back to HF\n", reason)
		return false, nil
	}

	raw, err = json.Marshal(startResp.Data)
	if err != nil {
		return false, nil
	}
	var jobData struct {
		JobID     string   `json:"job_id"`
		PeerAddrs []string `json:"peer_addrs"`
	}
	if err := json.Unmarshal(raw, &jobData); err != nil {
		return false, nil
	}
	jobID := jobData.JobID
	if strings.TrimSpace(jobID) == "" {
		fmt.Println("LAN download failed: missing job id — falling back to HF")
		return false, nil
	}
	if len(jobData.PeerAddrs) > 0 {
		fmt.Printf("Injected direct peers: %s\n", strings.Join(jobData.PeerAddrs, ", "))
	} else {
		fmt.Println("Injected direct peers: none")
	}

	// Poll progress until done.
	dc2 := daemon.DefaultClient()
	metaWaitTicks := 0
	for {
		select {
		case <-ctx.Done():
			cancelLANJob(jobID)
			fmt.Println("\nLAN download canceled.")
			return false, fmt.Errorf("download canceled by user")
		case <-time.After(time.Second):
		}
		pr, err := dc2.Send(daemon.Request{
			Cmd:   daemon.CmdJobStatus,
			JobID: jobID,
			Dir:   modelDir,
		})
		if err != nil {
			fmt.Printf("\nLAN download error: %v — falling back to HF\n", err)
			return false, nil
		}
		raw, err = json.Marshal(pr.Data)
		if err != nil {
			continue
		}
		var js daemon.JobStatusData
		if err := json.Unmarshal(raw, &js); err != nil {
			continue
		}
		if js.Error != "" {
			fmt.Printf("\nLAN download failed: %s — falling back to HF\n", js.Error)
			return false, nil
		}
		if js.Total == 0 && js.Written == 0 && !js.Done {
			metaWaitTicks++
			if metaWaitTicks == 1 || metaWaitTicks%5 == 0 {
				fmt.Printf("\nWaiting for metadata (active=%d pending=%d half-open=%d total=%d)\n", js.ActivePeers, js.PendingPeers, js.HalfOpenPeers, js.TotalPeers)
			}
		}
		printTorrentProgress(js.Written, js.Total, js.RateBps, js.ElapsedSec, js.ETASeconds)
		if js.Done {
			fmt.Println()
			snapshotHash, hashErr := hashFileSHA256(filepath.Join(modelDir, js.Filename))
			if hashErr != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to compute snapshot hash for %s: %v\n", id, hashErr)
				return false, nil
			}
			// Write metadata.json
			meta := cache.Metadata{
				HFRepo:     effectiveHFRepo,
				HFRevision: effectiveHFRevision,
				HFSnapshot: snapshotHash,
				Infohash:   lq.Infohash,
				InfohashV2: lq.InfohashV2,
				Files:      []string{js.Filename},
				Size:       js.Total,
			}
			if err := store.Save(id, meta); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save metadata for %s: %v\n", id, err)
			}
			fmt.Printf("Saved %s  (%s)  [from LAN]\n", id, cache.FormatSize(js.Total))
			fmt.Printf("  model:    %s\n", filepath.Join(store.Dir(id), js.Filename))
			fmt.Printf("  metadata: %s\n", filepath.Join(store.Dir(id), "metadata.json"))
			if js.MagnetURI != "" {
				fmt.Printf("  magnet:   %s\n", js.MagnetURI)
			}
			return true, nil
		}
	}
}

func notifyDaemonSeed(ctx context.Context, modelID, dir, filename, hfRepo, hfRevision, localHash string, pieces []byte, fileSize int64, quantization string) {
	if !daemon.IsRunning() {
		fmt.Print("Starting daemon...")
		if err := daemon.Launch(); err != nil {
			fmt.Printf(" failed (%v) — run 'hali daemon start' manually\n", err)
			return
		}
		fmt.Println(" done.")
	}

	dc := daemon.DefaultClient()
	resp, err := dc.Send(daemon.Request{
		Cmd:        daemon.CmdSeed,
		ModelID:    modelID,
		Dir:        dir,
		Filename:   filename,
		HFRepo:     hfRepo,
		HFRevision: hfRevision,
		Pieces:     pieces,
		FileSize:   fileSize,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: seed command failed: %v\n", err)
		return
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "error: daemon rejected seed request: %s\n", resp.Error)
		return
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: seed response marshal: %v\n", err)
		return
	}
	var seedResp map[string]string
	if err := json.Unmarshal(raw, &seedResp); err != nil {
		fmt.Fprintf(os.Stderr, "error: seed response unmarshal: %v\n", err)
		return
	}
	jobID := seedResp["job_id"]
	if jobID == "" {
		fmt.Fprintf(os.Stderr, "error: daemon returned empty seed job id\n")
		return
	}
	seedStatus, ok := waitForSeedFinalization(ctx, dc, jobID, dir)
	if !ok {
		return
	}
	if seedStatus.Infohash == "" || seedStatus.MagnetURI == "" {
		fmt.Fprintf(os.Stderr, "error: seed job completed but infohash/magnet are empty (job %s)\n", jobID)
		return
	}
	ingestModelID := strings.TrimSpace(hfRepo)
	if ingestModelID == "" {
		ingestModelID = modelID
	}

	publisherPubKey := ""
	publisherSig := ""
	eventTimestamp := time.Now().UTC()
	if pk, err := config.LoadOrCreateNodePublicKeyHex(); err == nil {
		payload := ingestPublisherSigningPayload(ingestModelID, hfRevision, seedStatus.Infohash, seedStatus.MagnetURI, hfDownloadURL(hfRepo, hfRevision, filename), localHash, eventTimestamp, pk)
		if sig, sigErr := config.SignNodePayloadHex(payload); sigErr == nil {
			publisherPubKey = pk
			publisherSig = sig
		}
	}
	event := events.ModelPullEvent{
		ModelID:         ingestModelID,
		Revision:        hfRevision,
		InfoHash:        seedStatus.Infohash,
		PublisherPubKey: publisherPubKey,
		PublisherSig:    publisherSig,
		ModelSizeBytes:  fileSize,
		Quantization:    strings.TrimSpace(quantization),
		Magnet:          seedStatus.MagnetURI,
		SourceURL:       hfDownloadURL(hfRepo, hfRevision, filename),
		LocalHash:       localHash,
		Timestamp:       eventTimestamp,
	}
	_, _ = dc.Send(daemon.Request{Cmd: daemon.CmdEnqueueEvent, Event: &event, AllowUnreachablePublish: true, Dir: dir})
	fmt.Println("Seeding in background (hali daemon status to monitor).")
	fmt.Println("Magnet URI will appear in 'hali daemon status' and the web dashboard once seeding is active.")
}

func ingestPublisherSigningPayload(modelID, revision, infohash, magnet, sourceURL, localHash string, timestamp time.Time, pubkey string) []byte {
	canonical := strings.Join([]string{
		strings.TrimSpace(modelID),
		strings.TrimSpace(revision),
		strings.ToLower(strings.TrimSpace(infohash)),
		strings.TrimSpace(magnet),
		strings.TrimSpace(sourceURL),
		strings.TrimSpace(localHash),
		timestamp.UTC().Format(time.RFC3339Nano),
		strings.ToLower(strings.TrimSpace(pubkey)),
	}, "\n")
	return []byte(canonical)
}

func waitForSeedFinalization(ctx context.Context, dc *daemon.Client, jobID, dir string) (daemon.SeedStatusData, bool) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	for {
		resp, err := dc.Send(daemon.Request{Cmd: daemon.CmdSeedStatus, JobID: jobID, Dir: dir})
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: seed status poll error (job %s): %v\n", jobID, err)
		} else if resp.OK && resp.Data != nil {
			raw, marshalErr := json.Marshal(resp.Data)
			if marshalErr == nil {
				var status daemon.SeedStatusData
				if unmarshalErr := json.Unmarshal(raw, &status); unmarshalErr == nil {
					if status.Done {
						if status.Error != "" {
							fmt.Fprintf(os.Stderr, "error: seed job failed (job %s): %s\n", jobID, status.Error)
							return status, false
						}
						return status, true
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "warning: seed finalization canceled (job %s)\n", jobID)
			return daemon.SeedStatusData{}, false
		case <-deadline.C:
			fmt.Fprintf(os.Stderr, "error: timed out waiting for seed job %s to finalize\n", jobID)
			return daemon.SeedStatusData{}, false
		case <-ticker.C:
		}
	}
}

func hfDownloadURL(repoID, revision, filename string) string {
	return fmt.Sprintf("https://huggingface.co/%s/resolve/%s/%s", repoID, revision, url.PathEscape(filename))
}

func resolveRepo(ctx context.Context, client *hf.Client, query string) (string, error) {
	if strings.Contains(query, "/") {
		return query, nil
	}
	fmt.Printf("Searching HuggingFace for %q...\n\n", query)
	results, err := client.Search(ctx, query)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	if len(results) == 0 {
		return "", fmt.Errorf("no models found for %q", query)
	}
	labels := make([]string, len(results))
	for i, r := range results {
		labels[i] = fmt.Sprintf("%-55s  %s downloads", r.ID, fmtDownloads(r.Downloads))
	}
	fmt.Println("Select model:")
	idx, err := pickOneWithMode("Model", labels, pullModelIndex)
	if err != nil {
		return "", err
	}
	return results[idx].ID, nil
}

func printProgress(p hf.Progress) {
	const width = 28
	pct := 0.0
	if p.Total > 0 {
		pct = float64(p.Written) / float64(p.Total)
	}
	filled := int(pct * width)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	written := cache.FormatSize(p.Written)
	total := "?"
	if p.Total > 0 {
		total = cache.FormatSize(p.Total)
	}
	rate := ""
	if p.Rate > 0 {
		rate = fmt.Sprintf("  %s/s", cache.FormatSize(int64(p.Rate)))
	}
	eta := "  eta --"
	if p.Total > 0 && p.Rate > 0 && p.Total > p.Written {
		etaSec := int64(float64(p.Total-p.Written) / p.Rate)
		eta = fmt.Sprintf("  eta %s", formatDurationShort(time.Duration(etaSec)*time.Second))
	}
	elapsed := ""
	if p.Rate > 0 {
		elapsedSec := int64(float64(p.Written-p.ResumeAt) / p.Rate)
		if elapsedSec < 0 {
			elapsedSec = 0
		}
		elapsed = fmt.Sprintf("  elapsed %s", formatDurationShort(time.Duration(elapsedSec)*time.Second))
	}
	resume := ""
	if p.Resumed {
		resume = fmt.Sprintf("  (resumed @ %s)", cache.FormatSize(p.ResumeAt))
	}
	fmt.Printf("\r[%s]  %s / %s  %.0f%%%s%s%s%s    ", bar, written, total, pct*100, rate, eta, elapsed, resume)
}

func printTorrentProgress(written, total, rateBps, elapsedSec, etaSec int64) {
	const width = 28
	pct := 0.0
	if total > 0 {
		pct = float64(written) / float64(total)
	}
	filled := int(pct * width)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	w := cache.FormatSize(written)
	t := "?"
	if total > 0 {
		t = cache.FormatSize(total)
	}
	rate := ""
	if rateBps > 0 {
		rate = fmt.Sprintf("  %s/s", cache.FormatSize(rateBps))
	}
	eta := "  eta --"
	if etaSec > 0 {
		eta = fmt.Sprintf("  eta %s", formatDurationShort(time.Duration(etaSec)*time.Second))
	} else if total > 0 && written >= total {
		eta = "  eta 0s"
	}
	elapsed := ""
	if elapsedSec > 0 || written > 0 {
		elapsed = fmt.Sprintf("  elapsed %s", formatDurationShort(time.Duration(elapsedSec)*time.Second))
	}
	fmt.Printf("\r[%s]  %s / %s  %.0f%%%s%s%s    ", bar, w, t, pct*100, rate, eta, elapsed)
}

func formatDurationShort(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int64(d.Seconds())
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%02dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%02ds", minutes, secs)
	}
	return fmt.Sprintf("%ds", secs)
}

func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
