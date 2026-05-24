package cmd

import (
	"encoding/json"
	"fmt"
	"hali/internal/hf"
	"strings"

	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search for models on Hugging Face",
	Long: `Search Hugging Face for GGUF models matching the query.

Results are ranked by download count.

By default this command is interactive:
	- shows matches
	- prompts for a selection
	- runs pull for the selected repo

Automation:
	--list                 List matches and exit
	--select N             Select Nth match without prompt
	--json                 Print full results as formatted JSON and exit
	--non-interactive      Disable prompts globally

Examples:
  hali search mistral
	hali search mistral --list
	hali search mistral --select 2 --non-interactive
	hali search mistral --json
  hali search "llama 3 instruct"
  hali search codellama`,
	Args: cobra.ExactArgs(1),
	RunE: runSearch,
}

var (
	searchSelectIndex int
	searchListOnly    bool
)

func configureSearchFlags() {
	searchCmd.Flags().IntVar(&searchSelectIndex, "select", 0, "Select the Nth result (1-based) without prompting")
	searchCmd.Flags().BoolVar(&searchListOnly, "list", false, "List search results only and exit")
}

func runSearch(cmd *cobra.Command, args []string) error {
	query := args[0]
	client := hf.NewClient()

	if !jsonOutput {
		fmt.Printf("Searching HuggingFace for %q...\n\n", query)
	}
	results, err := client.Search(cmd.Context(), query)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}
	if jsonOutput {
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return fmt.Errorf("encode search results as json: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}
	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}
	labels := make([]string, len(results))
	for i, r := range results {
		labels[i] = fmt.Sprintf("%-55s  %s downloads", r.ID, fmtDownloads(r.Downloads))
	}
	if searchListOnly {
		for i, label := range labels {
			fmt.Printf("%2d  %s\n", i+1, label)
		}
		return nil
	}
	fmt.Println("Select model:")
	idx, err := pickOneWithMode("Model", labels, searchSelectIndex)
	if err != nil {
		return err
	}

	return runPull(cmd, []string{results[idx].ID})
}

func fmtDownloads(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// pickOne prompts the user to select from a numbered list and returns the chosen index.
func pickOne(prompt string, items []string) (int, error) {
	if len(items) == 0 {
		return 0, fmt.Errorf("no options available")
	}

	for i, item := range items {
		fmt.Printf("  %2d  %s\n", i+1, item)
	}
	fmt.Printf("\n%s [1-%d]: ", prompt, len(items))
	var choice int
	if _, err := fmt.Scan(&choice); err != nil || choice < 1 || choice > len(items) {
		return 0, fmt.Errorf("invalid selection")
	}
	return choice - 1, nil
}

func pickOneWithMode(prompt string, items []string, selection int) (int, error) {
	if len(items) == 0 {
		return 0, fmt.Errorf("no options available")
	}
	if !nonInteractive {
		if selection > 0 {
			if selection > len(items) {
				return 0, fmt.Errorf("invalid %s selection %d (valid range: 1-%d)", strings.ToLower(prompt), selection, len(items))
			}
			return selection - 1, nil
		}
		return pickOne(prompt, items)
	}
	if selection <= 0 {
		selection = 1
	}
	if selection > len(items) {
		return 0, fmt.Errorf("invalid %s selection %d (valid range: 1-%d)", strings.ToLower(prompt), selection, len(items))
	}
	fmt.Printf("%s: selected %d (non-interactive)\n", prompt, selection)
	return selection - 1, nil
}
