package cmd

import (
	"errors"
	"fmt"
	exportcore "hali/internal/export"
	"hali/internal/export/lmstudio"
	"hali/internal/export/ollama"
	"hali/internal/runtime"

	"github.com/spf13/cobra"
)

var (
	exportAllFlag    bool
	exportStrictFlag bool
)

var exportCmd = &cobra.Command{
	Use:   "export [model_id]",
	Short: "Export a cached model to a runtime format",
	Long: `Export a cached model into runtime-specific local formats.

Modes:
	- hali export all <model_id>
	- hali export <model_id> --all

Use --strict to fail if a selected runtime is missing.

Automation:
	--all                 Export to all detected runtimes
	--strict              Treat missing runtime as failure
	--json                JSON mode for machine-readable integrations
	--non-interactive     Disable prompts globally

Examples:
	hali export ollama mistral:7b:instruct:q4_k_m
	hali export all mistral:7b:instruct:q4_k_m
	hali export mistral:7b:instruct:q4_k_m --all
	hali export mistral:7b:instruct:q4_k_m --all --strict --json`,
	Args: cobra.MaximumNArgs(1),
	RunE: runExport,
}

func configureExportFlags() {
	exportCmd.Flags().BoolVar(&exportAllFlag, "all", false, "Export to all detected runtimes")
	exportCmd.Flags().BoolVar(&exportStrictFlag, "strict", false, "Fail if a selected runtime is missing")
}

func runExport(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if !exportAllFlag {
		return fmt.Errorf("use --all or a runtime subcommand, e.g. 'hali export all <model_id>'")
	}
	return executeExport(args[0], nil, exportStrictFlag)
}

func newExportEngine() *exportcore.ExportEngine {
	return exportcore.NewEngine(
		runtime.NewRegistry(),
		ollama.NewExporter(),
		lmstudio.NewExporter(),
	)
}

func executeExport(modelID string, targets []string, strict bool) error {
	engine := newExportEngine()
	results, err := engine.Export(modelID, targets, strict)
	if err != nil {
		return err
	}

	failures := 0
	for _, res := range results {
		if res.Err != nil {
			fmt.Printf("%s: failed (%v)\n", res.Runtime, res.Err)
			failures++
			continue
		}
		if res.Skipped {
			switch res.Reason {
			case "not detected":
				fmt.Printf("warning: %s not detected, skipping\n", res.Runtime)
			case "unsupported model":
				fmt.Printf("%s: skipped (unsupported model)\n", res.Runtime)
			default:
				fmt.Printf("%s: skipped (%s)\n", res.Runtime, res.Reason)
			}
			continue
		}
		fmt.Printf("%s: exported\n", res.Runtime)
		if p := runtimeExportPath(res.Runtime, modelID); p != "" {
			fmt.Printf("  path: %s\n", p)
		}
	}

	if failures > 0 {
		return fmt.Errorf("export completed with %d failure(s)", failures)
	}
	return nil
}

func runtimeExportPath(runtimeName, modelID string) string {
	switch runtimeName {
	case "ollama":
		p, err := ollama.ManifestPath(modelID)
		if err == nil {
			return p
		}
	case "lmstudio":
		p, err := lmstudio.ExportPath(modelID)
		if err == nil {
			return p
		}
	}
	return ""
}

func isNoChange(runtimeName string, err error) bool {
	switch runtimeName {
	case "ollama":
		return errors.Is(err, ollama.ErrNoChange)
	case "lmstudio":
		return errors.Is(err, lmstudio.ErrNoChange)
	default:
		return false
	}
}
