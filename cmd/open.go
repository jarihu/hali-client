package cmd

import (
	"fmt"
	"hali/internal/hf"
	"hali/internal/protocol"
	"hali/internal/pull"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var openCmd = &cobra.Command{
	Use:   "open <url>",
	Short: "Open a model from a hali:// URL",
	Long: `Open a model from a hali:// protocol URL.

This command is used by the OS protocol handler when a user clicks an
"Open in Hali" button on a website. It resolves the model, selects the
best available GGUF quantization, and starts the normal pull flow.

Example:
  hali open "hali://model/Qwen/Qwen3-32B?version=latest"`,
	Args: cobra.ExactArgs(1),
	RunE: runOpen,
}

func runOpen(cmd *cobra.Command, args []string) error {
	parsed, err := protocol.Parse(args[0])
	if err != nil {
		return fmt.Errorf("invalid hali:// URL: %w", err)
	}

	unlock, err := protocol.AcquireLock(parsed.RepositoryID())
	if err != nil {
		return err
	}
	defer unlock()

	client := hf.NewClient()
	files, _, err := client.GetFiles(cmd.Context(), parsed.RepositoryID(), parsed.Revision())
	if err != nil {
		return fmt.Errorf("resolve %s: %w", parsed.RepositoryID(), err)
	}

	chosen := selectBestGGUF(files)
	if chosen == "" {
		return fmt.Errorf("no GGUF files found for %s", parsed.RepositoryID())
	}

	return runPullWithOptions(cmd, pull.Options{
		Repo:           parsed.RepositoryID(),
		Revision:       parsed.Revision(),
		FileName:       chosen,
		NonInteractive: true,
	})
}

// normalizeQuant makes quantization token matching robust against dash/underscore variants.
func normalizeQuant(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "-", "_"))
}

// selectBestGGUF returns the filename of the best GGUF variant using a fixed
// preference order. The alphabetical fallback is deterministic, not random.
// Returns "" only if no GGUF files exist at all.
func selectBestGGUF(files []hf.File) string {
	gguf := filterGGUF(files)

	preference := []string{
		"q4_k_m", "q5_k_m", "q4_k_s", "q6_k",
		"q3_k_m", "q8_0", "f16", "fp16",
	}
	for _, p := range preference {
		for _, f := range gguf {
			if strings.Contains(normalizeQuant(f.Name), p) {
				return f.Name
			}
		}
	}

	if len(gguf) == 0 {
		return ""
	}
	// Alphabetical fallback — deterministic but logged so the user can see it.
	sort.Slice(gguf, func(i, j int) bool { return gguf[i].Name < gguf[j].Name })
	fmt.Fprintf(os.Stderr, "hali: no preferred quantization found; using %s\n", gguf[0].Name)
	return gguf[0].Name
}

// filterGGUF returns only the GGUF files from the provided file list.
func filterGGUF(files []hf.File) []hf.File {
	out := make([]hf.File, 0, len(files))
	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f.Name), ".gguf") {
			out = append(out, f)
		}
	}
	return out
}
