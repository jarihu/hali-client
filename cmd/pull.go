package cmd

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hali/internal/cache"
	"hali/internal/config"
	"hali/internal/daemon"
	"hali/internal/events"
	"hali/internal/hf"
	"hali/internal/model"
	"hali/internal/pull"
	"hali/internal/registry"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

const progressBarWidth = 28

type downloadedArtifact struct {
	RepoID       string
	Revision     string
	FileName     string
	FileSize     int64
	ModelID      model.ModelID
	ModelDir     string
	ModelPath    string
	SnapshotHash string
	Seed         daemon.SeedStatusData
}

type collectionResult struct {
	CollectionKey string
	RepoDir       string
	Files         []string
	Infohash      string
	MagnetURI     string
}

type collectionState struct {
	Key      string   `json:"key"`
	Infohash string   `json:"infohash"`
	Files    []string `json:"files"`
}

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
	pullFilesFlag  string
)

func configurePullFlags() {
	pullCmd.Flags().IntVar(&pullModelIndex, "model-index", 0, "Select the Nth model search match (1-based) without prompting")
	pullCmd.Flags().IntVar(&pullFileIndex, "file-index", 0, "Select the Nth file variant (1-based) without prompting")
	pullCmd.Flags().StringVar(&pullFileName, "file-name", "", "Select an exact file variant name without prompting")
	pullCmd.Flags().StringVar(&pullFilesFlag, "files", "", "Download specific files (comma-separated, e.g. model.Q4_K_M.gguf,model.Q8_0.gguf)")
}

func runPull(cmd *cobra.Command, args []string) error {
	repo, fileName := parseRepoArg(args[0])
	if pullFileName != "" {
		fileName = pullFileName
	}
	var files []string
	for _, name := range strings.Split(pullFilesFlag, ",") {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return runPullWithOptions(cmd, pull.Options{
		Repo:           repo,
		Revision:       "",
		FileName:       fileName,
		Files:          files,
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
	files = onlyGGUFFiles(files)
	if len(files) == 0 {
		return fmt.Errorf("no GGUF files found in %s", repoID)
	}

	// Enrich with the registry download plan (best-effort; falls back to HF file list on error).
	var plan *registry.DownloadPlan
	if parts := strings.SplitN(repoID, "/", 2); len(parts) == 2 {
		rc := registry.New()
		if p, planErr := rc.DownloadPlan(ctx, parts[0], parts[1]); planErr == nil && len(p.Files) > 0 {
			planFiles := make([]hf.File, 0, len(p.Files))
			for _, f := range p.Files {
				if !isGGUFName(f.Name) {
					continue
				}
				planFiles = append(planFiles, hf.File{Name: f.Name, Size: f.Size})
			}
			if len(planFiles) > 0 {
				files = onlyGGUFFiles(planFiles)
				plan = p
			}
		}
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
			if obs, ok := lanObserved[daemon.ArtifactKey(id.String(), revision)]; ok {
				suffix += fmt.Sprintf("  [LAN observed: %d peer(s), seen %s ago]", obs.PeerCount, humanizeAgeUnix(obs.LastSeen))
			}
		}

		labels[i] = fmt.Sprintf("%-60s  %s%s", f.Name, sizeStr, suffix)
	}
	fmt.Println("Available files:")
	for _, label := range labels {
		fmt.Println(" ", label)
	}

	// Route to the appropriate download path.
	if opts.FileName != "" {
		idx := -1
		for i, f := range files {
			if strings.EqualFold(strings.TrimSpace(f.Name), strings.TrimSpace(opts.FileName)) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("file %q not found in available variants", opts.FileName)
		}
		fmt.Printf("Using file variant: %s\n", files[idx].Name)
		_, err := downloadSelectedFiles(ctx, repoID, revision, []hf.File{files[idx]}, store, client)
		return err
	}

	if pullFileIndex != 0 {
		picked, err := pickOneWithMode("Select file", labels, pullFileIndex)
		if err != nil {
			return err
		}
		_, err = downloadSelectedFiles(ctx, repoID, revision, []hf.File{files[picked]}, store, client)
		return err
	}

	if len(opts.Files) > 0 {
		selected := make([]hf.File, 0, len(opts.Files))
		for _, wanted := range opts.Files {
			idx := -1
			for i, f := range files {
				if strings.EqualFold(strings.TrimSpace(f.Name), wanted) {
					idx = i
					break
				}
			}
			if idx < 0 {
				return fmt.Errorf("file %q not found in repo %s", wanted, repoID)
			}
			selected = append(selected, files[idx])
		}
		_, err := downloadSelectedFiles(ctx, repoID, revision, selected, store, client)
		return err
	}

	// Default: download all GGUF files.
	if plan != nil && plan.Grouped && len(plan.ShardGroups) > 0 {
		return downloadByShardGroups(ctx, repoID, revision, plan, store, client)
	}
	return downloadFullRepoWithCollection(ctx, repoID, revision, files, store, client)
}

func downloadByShardGroups(ctx context.Context, repoID, revision string, plan *registry.DownloadPlan, store *cache.Store, client *hf.Client) error {
	covered := make(map[string]bool)
	ordered := make([]hf.File, 0, len(plan.Files))
	for _, sg := range plan.ShardGroups {
		for _, f := range sg.Files {
			if !isGGUFName(f.Name) {
				continue
			}
			if covered[f.Name] {
				continue
			}
			covered[f.Name] = true
			ordered = append(ordered, hf.File{Name: f.Name, Size: f.Size})
		}
	}
	for _, pf := range plan.Files {
		if !isGGUFName(pf.Name) || covered[pf.Name] {
			continue
		}
		covered[pf.Name] = true
		ordered = append(ordered, hf.File{Name: pf.Name, Size: pf.Size})
	}
	ordered = onlyGGUFFiles(ordered)
	if len(ordered) == 0 {
		return fmt.Errorf("no GGUF files found in %s", repoID)
	}
	return downloadFullRepoWithCollection(ctx, repoID, revision, ordered, store, client)
}

func isGGUFName(name string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), ".gguf")
}

func onlyGGUFFiles(files []hf.File) []hf.File {
	out := make([]hf.File, 0, len(files))
	for _, f := range files {
		if !isGGUFName(f.Name) {
			continue
		}
		out = append(out, hf.File{Name: strings.TrimSpace(f.Name), Size: f.Size})
	}
	sort.SliceStable(out, func(i, j int) bool {
		li := strings.ToLower(out[i].Name)
		lj := strings.ToLower(out[j].Name)
		if li == lj {
			return out[i].Name < out[j].Name
		}
		return li < lj
	})
	return out
}

func downloadSelectedFiles(ctx context.Context, repoID, revision string, selected []hf.File, store *cache.Store, client *hf.Client) ([]downloadedArtifact, error) {
	selected = onlyGGUFFiles(selected)
	if len(selected) == 0 {
		return nil, fmt.Errorf("no GGUF files found in %s", repoID)
	}
	if err := ensureEnoughDiskSpace(repoID, revision, selected, store); err != nil {
		return nil, err
	}
	agg := newMultiFileProgress(selected)
	results := make([]downloadedArtifact, 0, len(selected))
	var errs []error
	concurrency := loadPullConcurrency()
	if len(selected) <= 1 {
		concurrency = 1
	}
	if concurrency > len(selected) {
		concurrency = len(selected)
	}
	if len(selected) > 1 {
		fmt.Printf("\nDownloading %d GGUF file(s) from %s (concurrency=%d)\n", len(selected), repoID, concurrency)
	}
	if concurrency > 1 {
		type downloadJob struct {
			idx  int
			file hf.File
		}
		type downloadResult struct {
			idx      int
			artifact downloadedArtifact
			err      error
		}

		jobs := make(chan downloadJob)
		out := make(chan downloadResult, len(selected))
		var wg sync.WaitGroup

		worker := func() {
			defer wg.Done()
			for job := range jobs {
				if ctx.Err() != nil {
					out <- downloadResult{idx: job.idx, err: context.Canceled}
					continue
				}
				fmt.Printf("\n[%d/%d] %s\n", job.idx+1, len(selected), job.file.Name)
				artifact, err := downloadOneFile(ctx, repoID, revision, job.file, store, client, agg)
				out <- downloadResult{idx: job.idx, artifact: artifact, err: err}
			}
		}

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go worker()
		}

		go func() {
			for i, f := range selected {
				jobs <- downloadJob{idx: i, file: f}
			}
			close(jobs)
			wg.Wait()
			close(out)
		}()

		ordered := make([]downloadedArtifact, len(selected))
		succeeded := make([]bool, len(selected))
		for r := range out {
			if r.err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", selected[r.idx].Name, r.err))
				continue
			}
			ordered[r.idx] = r.artifact
			succeeded[r.idx] = true
		}
		for i := range ordered {
			if succeeded[i] {
				results = append(results, ordered[i])
			}
		}

		fmt.Printf("\nCompleted %d/%d file(s).\n", len(results), len(selected))
		if len(errs) > 0 {
			return results, errors.Join(errs...)
		}
		return results, nil
	}

	for i, f := range selected {
		select {
		case <-ctx.Done():
			return results, context.Canceled
		default:
		}
		fmt.Printf("\n[%d/%d] %s\n", i+1, len(selected), f.Name)
		artifact, err := downloadOneFile(ctx, repoID, revision, f, store, client, agg)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Name, err))
			continue
		}
		results = append(results, artifact)
	}
	fmt.Printf("\nCompleted %d/%d file(s).\n", len(results), len(selected))
	if len(errs) > 0 {
		return results, errors.Join(errs...)
	}
	return results, nil
}

func downloadOneFile(ctx context.Context, repoID, revision string, selected hf.File, store *cache.Store, client *hf.Client, agg *multiFileProgress) (downloadedArtifact, error) {
	result := downloadedArtifact{RepoID: repoID, Revision: revision, FileName: filepath.Base(selected.Name), FileSize: selected.Size}
	if err := ctx.Err(); err != nil {
		return result, context.Canceled
	}
	id := modelIDForDownload(repoID, revision, selected)
	result.ModelID = id
	modelDir := store.Dir(id)
	result.ModelDir = modelDir
	modelPath := filepath.Join(modelDir, filepath.Base(selected.Name))
	result.ModelPath = modelPath
	fmt.Printf("  save to:  %s\n", modelPath)

	ok, lanErr := tryLanDownload(ctx, id, store, selected.Name, repoID, revision)
	if lanErr != nil {
		return result, lanErr
	}
	if ok {
		result.FileSize = selected.Size
		if agg != nil {
			agg.finishFile(selected.Size)
		}
		return result, nil
	}

	fmt.Printf("Downloading %s from Hugging Face\n", selected.Name)
	progressFn := printProgress
	if agg != nil {
		progressFn = agg.wrap()
	}
	written, err := client.Download(ctx, repoID, selected.Name, revision, modelDir, progressFn, nil)
	if err != nil {
		return result, err
	}
	result.FileSize = written
	if agg != nil {
		agg.finishFile(written)
	}
	fmt.Println()

	snapshotHash, err := hashFileSHA256(filepath.Join(modelDir, filepath.Base(selected.Name)))
	if err != nil {
		return result, fmt.Errorf("download completed, but snapshot hash failed: %w", err)
	}
	result.SnapshotHash = snapshotHash

	meta := cache.Metadata{
		HFRepo:     repoID,
		HFRevision: revision,
		HFSnapshot: snapshotHash,
		Files:      []string{filepath.Base(selected.Name)},
		Size:       written,
	}
	if err := store.Save(id, meta); err != nil {
		return result, fmt.Errorf("save metadata: %w", err)
	}

	seedData, err := seedAndWait(ctx, id, modelDir, filepath.Base(selected.Name), repoID, revision)
	if err != nil {
		return result, err
	}
	result.Seed = seedData
	if seedData.Infohash != "" {
		if err := store.SetInfohash(modelDir, seedData.Infohash); err != nil {
			fmt.Printf("Warning: failed to persist infohash for %s: %v\n", id, err)
		}
	}

	fmt.Printf("Saved %s  (%s)\n", id, cache.FormatSize(written))
	fmt.Printf("  model:    %s\n", filepath.Join(modelDir, filepath.Base(selected.Name)))
	fmt.Printf("  metadata: %s\n", filepath.Join(modelDir, "metadata.json"))
	if seedData.MagnetURI != "" {
		fmt.Printf("  magnet:   %s\n", seedData.MagnetURI)
	}
	enqueueTorrentIngestEvent(id, repoID, revision, filepath.Base(selected.Name), seedData.Infohash, seedData.MagnetURI, snapshotHash, written)
	return result, nil
}

func downloadFullRepoWithCollection(ctx context.Context, repoID, revision string, files []hf.File, store *cache.Store, client *hf.Client) error {
	artifacts, dlErr := downloadSelectedFiles(ctx, repoID, revision, files, store, client)
	if len(artifacts) == 0 {
		if dlErr != nil {
			return dlErr
		}
		return fmt.Errorf("no GGUF files downloaded for %s", repoID)
	}

	var errs []error
	if dlErr != nil {
		errs = append(errs, dlErr)
	}

	if upErr := uploadArtifactMetadataBatch(ctx, repoID, revision, artifacts); upErr != nil {
		errs = append(errs, upErr)
	}

	collection, collErr := seedCollectionFromArtifacts(ctx, repoID, revision, artifacts, store)
	if collErr != nil {
		errs = append(errs, collErr)
	} else {
		if upErr := uploadCollectionMetadata(ctx, repoID, revision, collection, artifacts); upErr != nil {
			errs = append(errs, upErr)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func uploadArtifactMetadataBatch(ctx context.Context, repoID, revision string, artifacts []downloadedArtifact) error {
	owner, repo, err := splitRepoID(repoID)
	if err != nil {
		return err
	}
	pubKey, err := loadPublisherPubKey()
	if err != nil {
		return err
	}

	rc := registry.New()
	var errs []error
	entries := make([]registry.RepoBatchIngestFile, 0, len(artifacts))
	for _, a := range artifacts {
		infohash := strings.ToLower(strings.TrimSpace(a.Seed.Infohash))
		if infohash == "" {
			continue
		}
		torrentB64, loadErr := loadTorrentFileBase64(infohash)
		if loadErr != nil {
			errs = append(errs, fmt.Errorf("load artifact torrent %s: %w", a.FileName, loadErr))
			continue
		}

		sourceURL := buildHFSourceURL(repoID, revision, a.FileName)
		if strings.TrimSpace(sourceURL) == "" {
			errs = append(errs, fmt.Errorf("artifact source url is empty for %s", a.FileName))
			continue
		}
		localHash := strings.TrimSpace(a.SnapshotHash)
		if localHash == "" && strings.TrimSpace(a.ModelPath) != "" {
			if snapshotHash, hashErr := hashFileSHA256(a.ModelPath); hashErr == nil {
				localHash = strings.TrimSpace(snapshotHash)
			}
		}
		if localHash == "" {
			errs = append(errs, fmt.Errorf("artifact local hash is empty for %s", a.FileName))
			continue
		}
		magnet := strings.TrimSpace(a.Seed.MagnetURI)
		if magnet == "" {
			errs = append(errs, fmt.Errorf("artifact magnet is empty for %s", a.FileName))
			continue
		}

		timestamp := time.Now().UTC().Format(time.RFC3339Nano)
		sig, sigErr := signRepoIngestPayload(repoID, revision, infohash, magnet, sourceURL, localHash, timestamp, pubKey)
		if sigErr != nil {
			errs = append(errs, fmt.Errorf("sign artifact payload %s: %w", a.FileName, sigErr))
			continue
		}

		entries = append(entries, registry.RepoBatchIngestFile{
			Torrent:        torrentB64,
			TorrentType:    "artifact",
			Files:          []string{a.FileName},
			Infohash:       infohash,
			Magnet:         magnet,
			SourceURL:      sourceURL,
			LocalHash:      localHash,
			ModelSizeBytes: a.FileSize,
			Quantization:   strings.TrimSpace(a.ModelID.Quant),
			DisplayName:    strings.TrimSpace(a.FileName),
			PublisherSig:   sig,
			Timestamp:      timestamp,
		})
	}

	if len(entries) > 0 {
		payload := registry.RepoBatchIngestRequest{
			ModelID:         strings.TrimSpace(repoID),
			Revision:        strings.TrimSpace(revision),
			PublisherPubKey: pubKey,
			Files:           entries,
		}
		if _, upErr := rc.UploadRepoBatchIngest(ctx, owner, repo, payload); upErr != nil {
			errs = append(errs, fmt.Errorf("upload artifact metadata batch: %w", upErr))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func uploadCollectionMetadata(ctx context.Context, repoID, revision string, coll collectionResult, artifacts []downloadedArtifact) error {
	owner, repo, err := splitRepoID(repoID)
	if err != nil {
		return err
	}
	pubKey, err := loadPublisherPubKey()
	if err != nil {
		return err
	}
	infohash := strings.ToLower(strings.TrimSpace(coll.Infohash))
	if infohash == "" {
		return fmt.Errorf("collection infohash is empty")
	}
	torrentB64, err := loadTorrentFileBase64(infohash)
	if err != nil {
		return fmt.Errorf("load collection torrent: %w", err)
	}
	magnet := strings.TrimSpace(coll.MagnetURI)
	if magnet == "" {
		return fmt.Errorf("collection magnet is empty")
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	sourceURL := buildHFRepoTreeURL(repoID, revision)
	localHash := buildCollectionLocalHash(coll, artifacts)
	sig, err := signRepoIngestPayload(repoID, revision, infohash, magnet, sourceURL, localHash, timestamp, pubKey)
	if err != nil {
		return fmt.Errorf("sign collection payload: %w", err)
	}

	var totalSize int64
	artifactByName := make(map[string]downloadedArtifact, len(artifacts))
	for _, a := range artifacts {
		artifactByName[strings.TrimSpace(a.FileName)] = a
	}
	for _, name := range coll.Files {
		if a, ok := artifactByName[strings.TrimSpace(name)]; ok && a.FileSize > 0 {
			totalSize += a.FileSize
		}
	}

	rc := registry.New()
	payload := registry.RepoBatchIngestRequest{
		ModelID:         strings.TrimSpace(repoID),
		Revision:        strings.TrimSpace(revision),
		PublisherPubKey: pubKey,
		Files: []registry.RepoBatchIngestFile{
			{
				Torrent:        torrentB64,
				TorrentType:    "collection",
				Files:          append([]string(nil), coll.Files...),
				Infohash:       infohash,
				Magnet:         magnet,
				SourceURL:      sourceURL,
				LocalHash:      localHash,
				ModelSizeBytes: totalSize,
				DisplayName:    strings.TrimSpace(coll.CollectionKey),
				PublisherSig:   sig,
				Timestamp:      timestamp,
			},
		},
	}
	_, err = rc.UploadRepoBatchIngest(ctx, owner, repo, payload)
	if err != nil {
		return fmt.Errorf("upload collection metadata: %w", err)
	}
	return nil
}

func splitRepoID(repoID string) (string, string, error) {
	repoID = strings.Trim(strings.TrimSpace(repoID), "/")
	parts := strings.SplitN(repoID, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("invalid repo id %q, expected owner/repo", repoID)
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

func loadPublisherPubKey() (string, error) {
	pubKey, err := config.LoadOrCreateNodePublicKeyHex()
	if err != nil {
		return "", fmt.Errorf("load publisher pubkey: %w", err)
	}
	pubKey = strings.ToLower(strings.TrimSpace(pubKey))
	if pubKey == "" {
		return "", fmt.Errorf("publisher pubkey is empty")
	}
	return pubKey, nil
}

func signRepoIngestPayload(modelID, revision, infohash, magnet, sourceURL, localHash, timestampRFC3339, pubKey string) (string, error) {
	payload := strings.Join([]string{
		strings.TrimSpace(modelID),
		strings.TrimSpace(revision),
		strings.ToLower(strings.TrimSpace(infohash)),
		strings.TrimSpace(magnet),
		strings.TrimSpace(sourceURL),
		strings.TrimSpace(localHash),
		strings.TrimSpace(timestampRFC3339),
		strings.ToLower(strings.TrimSpace(pubKey)),
	}, "\n")
	sig, err := config.SignNodePayloadHex([]byte(payload))
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(sig)), nil
}

func loadTorrentFileBase64(infohash string) (string, error) {
	infohash = strings.ToLower(strings.TrimSpace(infohash))
	if infohash == "" {
		return "", fmt.Errorf("missing infohash for torrent payload")
	}
	torrentPath := filepath.Join(config.ServiceDataDir(), "torrents", infohash+".torrent")
	data, err := os.ReadFile(torrentPath)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func buildHFRepoTreeURL(repoID, revision string) string {
	repoID = strings.Trim(strings.TrimSpace(repoID), "/")
	revision = strings.TrimSpace(revision)
	if repoID == "" {
		return ""
	}
	if revision == "" {
		revision = "main"
	}
	return fmt.Sprintf("https://huggingface.co/%s/tree/%s", repoID, revision)
}

func buildCollectionLocalHash(coll collectionResult, artifacts []downloadedArtifact) string {
	artifactByName := make(map[string]downloadedArtifact, len(artifacts))
	for _, a := range artifacts {
		artifactByName[strings.TrimSpace(a.FileName)] = a
	}
	parts := make([]string, 0, len(coll.Files)+1)
	for _, file := range coll.Files {
		name := strings.TrimSpace(file)
		a := artifactByName[name]
		parts = append(parts, name+"\n"+strings.TrimSpace(a.SnapshotHash)+"\n"+fmt.Sprintf("%d", a.FileSize))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return strings.ToLower(strings.TrimSpace(coll.Infohash))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func seedCollectionFromArtifacts(ctx context.Context, repoID, revision string, artifacts []downloadedArtifact, store *cache.Store) (collectionResult, error) {
	if len(artifacts) == 0 {
		return collectionResult{}, fmt.Errorf("no artifacts available for collection")
	}
	files := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		if !isGGUFName(a.FileName) {
			continue
		}
		files = append(files, a.FileName)
	}
	sort.SliceStable(files, func(i, j int) bool {
		li := strings.ToLower(files[i])
		lj := strings.ToLower(files[j])
		if li == lj {
			return files[i] < files[j]
		}
		return li < lj
	})
	if len(files) == 0 {
		return collectionResult{}, fmt.Errorf("no GGUF files available for collection")
	}

	key := buildCollectionKey(repoID, revision, artifacts)
	base := filepath.Join(store.Root, "_collections", sanitizeCollectionPart(repoID), sanitizeCollectionPart(revision), key)
	repoDir := filepath.Join(base, "repo")
	statePath := filepath.Join(base, "collection.json")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return collectionResult{}, fmt.Errorf("create collection dir: %w", err)
	}

	for _, a := range artifacts {
		target := filepath.Join(repoDir, filepath.Base(a.FileName))
		if _, err := os.Stat(target); err == nil {
			continue
		}
		if err := os.Link(a.ModelPath, target); err != nil {
			return collectionResult{}, fmt.Errorf("link %s into collection: %w", a.FileName, err)
		}
	}

	modelID := collectionModelID(repoID, revision, key)
	if st, err := loadCollectionState(statePath); err == nil && strings.EqualFold(strings.TrimSpace(st.Key), key) && strings.TrimSpace(st.Infohash) != "" {
		reused, reuseErr := seedFromTorrentFileAndWait(ctx, modelID, repoDir, st.Infohash)
		if reuseErr == nil {
			fmt.Printf("Collection torrent reused: %s\n", key)
			return collectionResult{
				CollectionKey: key,
				RepoDir:       repoDir,
				Files:         files,
				Infohash:      reused.Infohash,
				MagnetURI:     reused.MagnetURI,
			}, nil
		}
	}

	seedData, err := seedCollectionAndWait(ctx, modelID, repoID, repoDir, revision)
	if err != nil {
		return collectionResult{}, err
	}
	_ = saveCollectionState(statePath, collectionState{Key: key, Infohash: seedData.Infohash, Files: files})

	fmt.Printf("Collection torrent ready: %s\n", key)
	if seedData.MagnetURI != "" {
		fmt.Printf("  magnet:   %s\n", seedData.MagnetURI)
	}

	return collectionResult{
		CollectionKey: key,
		RepoDir:       repoDir,
		Files:         files,
		Infohash:      seedData.Infohash,
		MagnetURI:     seedData.MagnetURI,
	}, nil
}

func loadCollectionState(path string) (collectionState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return collectionState{}, err
	}
	var st collectionState
	if err := json.Unmarshal(data, &st); err != nil {
		return collectionState{}, err
	}
	return st, nil
}

func saveCollectionState(path string, st collectionState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func buildCollectionKey(repoID, revision string, artifacts []downloadedArtifact) string {
	parts := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		parts = append(parts, registry.BuildDeterministicTorrentMetadata(repoID, revision, a.FileName, a.FileSize).Infohash)
	}
	sort.Strings(parts)
	canonical := strings.TrimSpace(repoID) + "\n" + strings.TrimSpace(revision) + "\n" + strings.Join(parts, "\n")
	sum := sha1.Sum([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func sanitizeCollectionPart(raw string) string {
	v := strings.TrimSpace(strings.ToLower(raw))
	if v == "" {
		return "main"
	}
	v = strings.ReplaceAll(v, "/", "_")
	v = strings.ReplaceAll(v, "\\", "_")
	v = strings.ReplaceAll(v, ":", "_")
	return v
}

func collectionModelID(repoID, revision, key string) model.ModelID {
	repoSafe := sanitizeCollectionPart(repoID)
	revSafe := sanitizeCollectionPart(revision)
	if revSafe == "" {
		revSafe = "main"
	}
	return model.ModelID{
		Base:     "collection_" + repoSafe,
		Size:     "repo",
		Variant:  "all",
		Quant:    "set_" + key[:12],
		Format:   "gguf",
		Revision: revSafe,
	}
}

func modelIDForDownload(repoID, revision string, selected hf.File) model.ModelID {
	id := model.FromHF(repoID, selected.Name)
	if id.IsZero() {
		quant := strings.TrimSuffix(strings.ToLower(selected.Name), ".gguf")
		quant = filepath.Base(quant)
		size := inferFallbackSize(repoID, selected.Name)
		id = model.ModelID{
			Base:    buildFallbackBase(repoID, size),
			Size:    size,
			Variant: "base",
			Quant:   quant,
			Format:  "gguf",
		}
	}
	id.Revision = revision
	return id
}

func ensureEnoughDiskSpace(repoID, revision string, files []hf.File, store *cache.Store) error {
	if len(files) == 0 {
		return nil
	}

	checkPath := store.Root
	if _, err := os.Stat(checkPath); os.IsNotExist(err) {
		checkPath = filepath.Dir(checkPath)
	}

	var needBytes int64
	var totalBytes int64
	unknown := make([]string, 0)
	for _, f := range files {
		if f.Size <= 0 {
			unknown = append(unknown, f.Name)
			continue
		}
		totalBytes += f.Size

		id := modelIDForDownload(repoID, revision, f)
		modelDir := store.Dir(id)
		modelPath := filepath.Join(modelDir, filepath.Base(f.Name))
		existing := existingFileOrPartialSize(modelPath)
		remaining := f.Size - existing
		if remaining < 0 {
			remaining = 0
		}
		needBytes += remaining
	}

	if len(unknown) > 0 {
		return fmt.Errorf("disk preflight failed: unknown size for %d file(s): %s", len(unknown), strings.Join(unknown, ", "))
	}

	available, err := availableDiskBytes(checkPath)
	if err != nil {
		return fmt.Errorf("disk preflight failed: could not read free space at %s: %w", checkPath, err)
	}

	reused := totalBytes - needBytes
	if reused < 0 {
		reused = 0
	}
	fmt.Printf("Storage preflight: need %s free (total %s, reused %s), available %s at %s\n",
		cache.FormatSize(needBytes),
		cache.FormatSize(totalBytes),
		cache.FormatSize(reused),
		cache.FormatSize(available),
		checkPath,
	)

	if available < needBytes {
		return fmt.Errorf("insufficient disk space: need %s free but only %s available at %s", cache.FormatSize(needBytes), cache.FormatSize(available), checkPath)
	}
	return nil
}

func existingFileOrPartialSize(modelPath string) int64 {
	var size int64
	if st, err := os.Stat(modelPath); err == nil && st.Size() > size {
		size = st.Size()
	}
	if st, err := os.Stat(modelPath + ".tmp"); err == nil && st.Size() > size {
		size = st.Size()
	}
	return size
}

type multiFileProgress struct {
	mu           sync.Mutex
	start        time.Time
	totalKnown   bool
	totalBytes   int64
	completed    int64
	currentBytes int64
}

func newMultiFileProgress(files []hf.File) *multiFileProgress {
	if len(files) <= 1 {
		return nil
	}
	m := &multiFileProgress{start: time.Now(), totalKnown: true}
	for _, f := range files {
		if f.Size <= 0 {
			m.totalKnown = false
			continue
		}
		m.totalBytes += f.Size
	}
	if m.totalBytes <= 0 {
		m.totalKnown = false
	}
	return m
}

func (m *multiFileProgress) wrap() func(hf.Progress) {
	return func(p hf.Progress) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.currentBytes = p.Written
		printProgressWithTotal(p, m)
	}
}

func (m *multiFileProgress) finishFile(size int64) {
	if m == nil {
		return
	}
	if size < 0 {
		size = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed += size
	m.currentBytes = 0
}

func loadPullConcurrency() int {
	cfg, err := config.LoadService()
	if err != nil {
		return config.DefaultPullConcurrency
	}
	return cfg.PullConcurrencyValue()
}

func printProgressWithTotal(p hf.Progress, m *multiFileProgress) {
	const progressBarWidth = 28
	pct := 0.0
	if p.Total > 0 {
		pct = float64(p.Written) / float64(p.Total)
	}
	filled := int(pct * progressBarWidth)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", progressBarWidth-filled)
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

	totalDone := m.completed + m.currentBytes
	totalPart := "  total " + cache.FormatSize(totalDone) + " / ?"
	if m.totalKnown && m.totalBytes > 0 {
		pctTotal := float64(totalDone) / float64(m.totalBytes) * 100
		if pctTotal > 100 {
			pctTotal = 100
		}
		totalPart = fmt.Sprintf("  total %s / %s  %.0f%%", cache.FormatSize(totalDone), cache.FormatSize(m.totalBytes), pctTotal)
	}

	elapsedTotal := time.Since(m.start)
	totalEta := "  total eta --"
	if m.totalKnown && m.totalBytes > 0 && totalDone > 0 {
		seconds := elapsedTotal.Seconds()
		if seconds > 0 {
			avgRate := float64(totalDone) / seconds
			remaining := m.totalBytes - totalDone
			if remaining <= 0 {
				totalEta = "  total eta 0s"
			} else if avgRate > 0 {
				etaSec := int64(float64(remaining) / avgRate)
				totalEta = fmt.Sprintf("  total eta %s", formatDurationShort(time.Duration(etaSec)*time.Second))
			}
		}
	}
	totalElapsed := fmt.Sprintf("  total elapsed %s", formatDurationShort(elapsedTotal))

	fmt.Printf("\r[%s]  %s / %s  %.0f%%%s%s%s%s%s%s%s    ", bar, written, total, pct*100, rate, eta, elapsed, resume, totalPart, totalEta, totalElapsed)
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
			k = daemon.ArtifactKey(row.ModelID, row.Revision)
		}
		out[k] = lanObservedFact{PeerCount: row.PeerCount, LastSeen: row.LastSeen}
	}
	return out
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

// FinishDownloadJob polls a download job to completion, hashes the file,
// saves metadata, and prints the result. Returns the snapshot hash.
func FinishDownloadJob(ctx context.Context, dc *daemon.Client, jobID, modelDir string, id model.ModelID, store *cache.Store, hfRepo, hfRevision, infohash, infohashV2 string, onMetaWait func(js daemon.JobStatusData)) (string, error) {
	for {
		select {
		case <-ctx.Done():
			if !daemon.IsRunning() {
				return "", context.Canceled
			}
			daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdCancelJob, JobID: jobID})
			fmt.Println("\nDownload canceled.")
			return "", context.Canceled
		case <-time.After(time.Second):
		}

		pr, err := dc.Send(daemon.Request{Cmd: daemon.CmdJobStatus, JobID: jobID, Dir: modelDir})
		if err != nil {
			return "", fmt.Errorf("download error: %w", err)
		}
		raw, err := json.Marshal(pr.Data)
		if err != nil {
			continue
		}
		var js daemon.JobStatusData
		if err := json.Unmarshal(raw, &js); err != nil {
			continue
		}
		if js.Error != "" {
			return "", fmt.Errorf("download failed: %s", js.Error)
		}

		if onMetaWait != nil && js.Total == 0 && js.Written == 0 && !js.Done {
			onMetaWait(js)
		}

		printTorrentProgress(js.Written, js.Total, js.RateBps, js.ElapsedSec, js.ETASeconds)
		if !js.Done {
			continue
		}

		fmt.Println()
		snapshotHash, hashErr := hashFileSHA256(filepath.Join(modelDir, js.Filename))
		if hashErr != nil {
			return "", fmt.Errorf("download completed, but snapshot hash failed: %w", hashErr)
		}

		meta := cache.Metadata{
			HFRepo:     hfRepo,
			HFRevision: hfRevision,
			HFSnapshot: snapshotHash,
			Infohash:   infohash,
			InfohashV2: infohashV2,
			Files:      []string{js.Filename},
			Size:       js.Total,
		}
		if err := store.Save(id, meta); err != nil {
			fmt.Printf("Warning: failed to save metadata for %s: %v\n", id, err)
		}

		fmt.Printf("Saved %s  (%s)  [from LAN]\n", id, cache.FormatSize(js.Total))
		fmt.Printf("  model:    %s\n", filepath.Join(modelDir, js.Filename))
		fmt.Printf("  metadata: %s\n", filepath.Join(modelDir, "metadata.json"))
		if js.MagnetURI != "" {
			fmt.Printf("  magnet:   %s\n", js.MagnetURI)
		}
		enqueueTorrentIngestEvent(id, hfRepo, hfRevision, js.Filename, infohash, js.MagnetURI, snapshotHash, js.Total)
		return snapshotHash, nil
	}
}

func tryLanDownload(ctx context.Context, id model.ModelID, store *cache.Store, filename, hfRepo, hfRevision string) (bool, error) {
	if !daemon.IsRunning() {
		return false, nil
	}

	queryResp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdLanQuery, ModelID: id.String()})
	if err != nil || !queryResp.OK || queryResp.Data == nil {
		return false, nil
	}

	raw, err := json.Marshal(queryResp.Data)
	if err != nil {
		return false, nil
	}
	var q daemon.LanQueryData
	if err := json.Unmarshal(raw, &q); err != nil {
		return false, nil
	}
	if strings.TrimSpace(q.Infohash) == "" {
		return false, nil
	}

	if strings.TrimSpace(hfRepo) == "" {
		hfRepo = strings.TrimSpace(q.HFRepo)
	}
	if strings.TrimSpace(hfRevision) == "" {
		hfRevision = strings.TrimSpace(q.Revision)
	}
	if strings.TrimSpace(hfRepo) == "" || strings.TrimSpace(hfRevision) == "" {
		return false, nil
	}

	fmt.Printf("Found on LAN: %s (infohash %s)\n", id, q.Infohash[:8]+"…")
	if len(q.PeerAddrs) > 0 {
		fmt.Printf("Direct peer endpoints: %s\n", strings.Join(q.PeerAddrs, ", "))
	} else {
		fmt.Println("Direct peer endpoints: none observed (waiting on LSD/passive discovery)")
	}

	modelDir := store.Dir(id)
	dc := daemon.DefaultClient()
	startResp, err := dc.Send(daemon.Request{
		Cmd:        daemon.CmdDownload,
		ModelID:    id.String(),
		Infohash:   q.Infohash,
		InfohashV2: q.InfohashV2,
		PeerAddrs:  q.PeerAddrs,
		Dir:        modelDir,
	})
	if err != nil {
		return false, fmt.Errorf("LAN download failed: %w", err)
	}
	if !startResp.OK {
		reason := strings.TrimSpace(startResp.Error)
		if reason == "" {
			reason = "daemon rejected download request"
		}
		return false, fmt.Errorf("LAN download failed: %s", reason)
	}

	raw, err = json.Marshal(startResp.Data)
	if err != nil {
		return false, err
	}
	var jobData struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(raw, &jobData); err != nil {
		return false, err
	}
	if strings.TrimSpace(jobData.JobID) == "" {
		return false, fmt.Errorf("LAN download failed: missing job id")
	}

	_, err = FinishDownloadJob(ctx, dc, jobData.JobID, modelDir, id, store, hfRepo, hfRevision, q.Infohash, q.InfohashV2, nil)
	if err != nil {
		return false, err
	}
	return true, nil
}

func pollSeedJob(ctx context.Context, jobID string, dir string) (daemon.SeedStatusData, error) {
	dc := daemon.DefaultClient()
	for {
		select {
		case <-ctx.Done():
			return daemon.SeedStatusData{}, context.Canceled
		case <-time.After(time.Second):
		}

		stResp, err := dc.Send(daemon.Request{Cmd: daemon.CmdSeedStatus, JobID: jobID, Dir: dir})
		if err != nil {
			return daemon.SeedStatusData{}, err
		}
		if !stResp.OK {
			return daemon.SeedStatusData{}, fmt.Errorf("%s", strings.TrimSpace(stResp.Error))
		}
		raw, err := json.Marshal(stResp.Data)
		if err != nil {
			continue
		}
		var sd daemon.SeedStatusData
		if err := json.Unmarshal(raw, &sd); err != nil {
			continue
		}
		if !sd.Done {
			continue
		}
		if sd.Error != "" {
			return daemon.SeedStatusData{}, fmt.Errorf("%s", sd.Error)
		}
		return sd, nil
	}
}

func startSeedJobAndWait(ctx context.Context, req daemon.Request, label string) (daemon.SeedStatusData, error) {
	if !daemon.IsRunning() {
		return daemon.SeedStatusData{}, fmt.Errorf("daemon is not running")
	}
	dc := daemon.DefaultClient()
	resp, err := dc.Send(req)
	if err != nil {
		return daemon.SeedStatusData{}, fmt.Errorf("%s: %w", label, err)
	}
	if !resp.OK {
		reason := strings.TrimSpace(resp.Error)
		if reason == "" {
			reason = "daemon rejected request"
		}
		return daemon.SeedStatusData{}, fmt.Errorf("%s: %s", label, reason)
	}

	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return daemon.SeedStatusData{}, err
	}
	var seedJob struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(raw, &seedJob); err != nil {
		return daemon.SeedStatusData{}, err
	}
	if strings.TrimSpace(seedJob.JobID) == "" {
		return daemon.SeedStatusData{}, fmt.Errorf("%s: missing job id", label)
	}

	sd, err := pollSeedJob(ctx, seedJob.JobID, req.Dir)
	if err != nil {
		return daemon.SeedStatusData{}, fmt.Errorf("%s: %w", label, err)
	}
	return sd, nil
}

func seedAndWait(ctx context.Context, id model.ModelID, modelDir, filename, repoID, revision string) (daemon.SeedStatusData, error) {
	return startSeedJobAndWait(ctx, daemon.Request{
		Cmd:        daemon.CmdSeed,
		Dir:        modelDir,
		Filename:   filename,
		ModelID:    id.String(),
		HFRepo:     repoID,
		HFRevision: revision,
	}, "start seeding")
}

func seedCollectionAndWait(ctx context.Context, id model.ModelID, repoID, repoDir, revision string) (daemon.SeedStatusData, error) {
	return startSeedJobAndWait(ctx, daemon.Request{
		Cmd:        daemon.CmdSeedCollection,
		Dir:        repoDir,
		ModelID:    id.String(),
		HFRepo:     repoID,
		HFRevision: revision,
	}, "start collection seeding")
}

func seedFromTorrentFileAndWait(ctx context.Context, id model.ModelID, dir, infohash string) (daemon.SeedStatusData, error) {
	return startSeedJobAndWait(ctx, daemon.Request{
		Cmd:      daemon.CmdSeedFromFile,
		Dir:      dir,
		ModelID:  id.String(),
		Infohash: strings.ToLower(strings.TrimSpace(infohash)),
	}, "seed from file")
}

func enqueueTorrentIngestEvent(id model.ModelID, repoID, revision, filename, infohash, magnet, localHash string, size int64) {
	if !daemon.IsRunning() {
		return
	}
	if strings.TrimSpace(infohash) == "" {
		return
	}
	event := events.ModelPullEvent{
		ModelID:        id.String(),
		Revision:       strings.TrimSpace(revision),
		InfoHash:       strings.ToLower(strings.TrimSpace(infohash)),
		ModelSizeBytes: size,
		Quantization:   strings.TrimSpace(id.Quant),
		Magnet:         strings.TrimSpace(magnet),
		SourceURL:      buildHFSourceURL(repoID, revision, filename),
		LocalHash:      strings.TrimSpace(localHash),
		Timestamp:      time.Now().UTC(),
	}
	resp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdEnqueueEvent, Event: &event, AllowUnreachablePublish: allowUnreachablePublish})
	if err != nil {
		fmt.Printf("Warning: failed to enqueue registry ingest event for %s: %v\n", id, err)
		return
	}
	if !resp.OK {
		fmt.Printf("Warning: ingest enqueue rejected for %s: %s\n", id, strings.TrimSpace(resp.Error))
	}
}

func buildHFSourceURL(repoID, revision, filename string) string {
	repoID = strings.Trim(strings.TrimSpace(repoID), "/")
	filename = strings.Trim(strings.TrimSpace(filename), "/")
	revision = strings.TrimSpace(revision)
	if repoID == "" || filename == "" {
		return ""
	}
	if revision == "" {
		revision = "main"
	}
	return fmt.Sprintf("https://huggingface.co/%s/resolve/%s/%s", repoID, revision, filename)
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
	const progressBarWidth = 28
	pct := 0.0
	if p.Total > 0 {
		pct = float64(p.Written) / float64(p.Total)
	}
	filled := int(pct * progressBarWidth)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", progressBarWidth-filled)
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
	const progressBarWidth = 28
	pct := 0.0
	if total > 0 {
		pct = float64(written) / float64(total)
	}
	filled := int(pct * progressBarWidth)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", progressBarWidth-filled)
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
