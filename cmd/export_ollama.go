package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var exportOllamaCmd = &cobra.Command{
	Use:   "ollama <model_id>",
	Short: "Export a GGUF model into Ollama manifests",
	Long: `Create or update an Ollama-compatible manifest for a cached model.

This command is idempotent and does not copy model files.

Example:
  hali export ollama mistral:7b:instruct:q4_k_m`,
	Args: cobra.ExactArgs(1),
	RunE: runExportOllama,
}

func runExportOllama(cmd *cobra.Command, args []string) error {
	modelID := args[0]
	fmt.Println("Exporting to Ollama...")
	if err := executeExport(modelID, []string{"ollama"}, false); err != nil {
		return err
	}
	fmt.Printf("model: %s\n", modelID)
	return nil
}
