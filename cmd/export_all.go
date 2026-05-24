package cmd

import "github.com/spf13/cobra"

var exportAllCmd = &cobra.Command{
	Use:   "all <model_id>",
	Short: "Export a model to all detected runtimes",
	Long: `Detect locally installed runtimes and export the model to each supported target.

Example:
  hali export all mistral:7b:instruct:q4_k_m`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeExport(args[0], nil, exportStrictFlag)
	},
}
