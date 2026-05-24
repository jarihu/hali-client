package cmd

import "github.com/spf13/cobra"

var runtimeCmd = &cobra.Command{
	Use:   "runtime",
	Short: "Inspect local model runtime availability",
}
