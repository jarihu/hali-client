package cmd

import (
	"fmt"
	"hali/internal/cache"
	"strings"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List downloaded models",
	Long: `List all models in the local cache.

Shows model ID, file size, and download date for each cached model.

Example:
  hali list`,
	RunE: runList,
}

func runList(cmd *cobra.Command, args []string) error {
	store := cache.NewStore()
	entries, err := store.List()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("No models downloaded. Run 'hali pull <model>' to get started.")
		return nil
	}
	fmt.Printf("%-42s  %-10s  %s\n", "MODEL ID", "SIZE", "DOWNLOADED")
	fmt.Printf("%-42s  %-10s  %s\n", strings.Repeat("-", 42), "----------", "----------")
	for _, e := range entries {
		size := cache.FormatSize(e.Meta.Size)
		date := e.Meta.DownloadedAt
		if len(date) > 10 {
			date = date[:10]
		}
		fmt.Printf("%-42s  %-10s  %s\n", e.ID, size, date)
	}
	return nil
}
