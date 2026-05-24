package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var exportLMStudioCmd = &cobra.Command{
	Use:   "lmstudio <model_id>",
	Short: "Export a GGUF model into LM Studio local models",
	Long: `Export a cached GGUF model into LM Studio's local model directory.

This command prefers creating a symbolic link so model bytes are not duplicated.
If symlink creation is unavailable, it falls back to copying.

Example:
  hali export lmstudio mistral:7b:instruct:q4_k_m`,
	Args: cobra.ExactArgs(1),
	RunE: runExportLMStudio,
}

func runExportLMStudio(cmd *cobra.Command, args []string) error {
	modelID := args[0]
	fmt.Println("Exporting to LM Studio...")
	if err := executeExport(modelID, []string{"lmstudio"}, false); err != nil {
		return err
	}
	fmt.Printf("model: %s\n", modelID)
	return nil
}
