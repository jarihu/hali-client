package cmd

import (
	"fmt"
	"hali/internal/runtime"

	"github.com/spf13/cobra"
)

var runtimeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List detected local runtimes",
	Long: `Show known runtimes and whether they are installed on this machine.

Example:
  hali runtime list`,
	Args: cobra.NoArgs,
	RunE: runRuntimeList,
}

func runRuntimeList(cmd *cobra.Command, args []string) error {
	reg := runtime.NewRegistry()
	all := reg.All()

	fmt.Printf("%-10s %-10s %s\n", "RUNTIME", "STATUS", "MODELS PATH")
	for _, rt := range all {
		path, err := rt.ModelsPath()
		if err != nil {
			path = "(unresolved)"
		}
		status := "not found"
		if rt.Detect() {
			status = "detected"
		}
		fmt.Printf("%-10s %-10s %s\n", rt.Name(), status, path)
	}
	return nil
}
