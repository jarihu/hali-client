package cmd

import (
	"hali/editionapi"
	"hali/internal/config"
	"log/slog"

	"github.com/spf13/cobra"
)

var nonInteractive bool
var jsonOutput bool

// NewRootCmd constructs the full CLI tree in one deterministic composition step.
func NewRootCmd(rt *editionapi.Runtime) *cobra.Command {
	if err := config.EnsureConfigMaterialized(); err != nil {
		slog.Warn("config materialize failed", "error", err)
	}
	if err := config.EnsureServiceConfigMaterialized(); err != nil {
		slog.Warn("service config materialize failed", "error", err)
	}

	root := &cobra.Command{
		Use:   "hali",
		Short: "Local-first model cache with optional P2P acceleration",
		Long: `hali — local model cache for LLMs with optional LAN acceleration.

Downloads AI models from Hugging Face, caches them locally, and seeds
completed downloads to other machines on your LAN via BitTorrent.

Works fully offline after download. No account, no cloud, no central server.

Quick start:
  hali search mistral          # find a model
  hali pull mistral            # download it
  hali list                    # show local cache
  hali daemon start            # start background seeder
  hali daemon status           # check what is being seeded

Automation (non-interactive):
  Use --non-interactive and per-command selectors.
  Use --json for machine-readable output (also implies non-interactive).
  See: hali search --help, hali pull --help, hali profile create --help

Use "hali <command> --help" for detailed help on any command.`,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if jsonOutput {
			nonInteractive = true
		}
	}

	configureExportFlags()
	configureStatsFlags()
	configurePullFlags()
	configureSearchFlags()
	configureProfileFlags()
	configureProtocolCommands()
	root.PersistentFlags().BoolVar(&allowUnreachablePublish, "allow-unreachable-publish", false, "Allow publish actions that may be unreachable from internet peers in lan_only mode")
	root.PersistentFlags().BoolVar(&nonInteractive, "non-interactive", false, "Disable interactive prompts")
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Emit JSON output when supported (implies non-interactive)")

	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd, daemonRunCmd)
	root.AddCommand(daemonCmd)

	exportCmd.AddCommand(exportAllCmd, exportOllamaCmd, exportLMStudioCmd)
	root.AddCommand(exportCmd)

	runtimeCmd.AddCommand(runtimeListCmd)
	root.AddCommand(runtimeCmd)

	serviceCmd.AddCommand(
		serviceInstallCmd,
		serviceUninstallCmd,
		serviceStartCmd,
		serviceStopCmd,
		serviceRestartCmd,
		serviceStatusCmd,
	)
	root.AddCommand(serviceCmd)

	telemetryCmd.AddCommand(telemetryEnableCmd, telemetryDisableCmd, telemetryStatusCmd)
	root.AddCommand(telemetryCmd)

	configRootCmd.AddCommand(configShowCmd, configSetCmd)
	root.AddCommand(configRootCmd)

	profileCmd.AddCommand(profileCreateCmd)

	root.AddCommand(pullCmd, searchCmd, listCmd, statsCmd, versionCmd, openCmd)
	root.AddCommand(protocolCmd)
	root.AddCommand(profileCmd)
	root.AddCommand(newCompletionCmd(root))

	RegisterEnterpriseCommands(root, rt)
	return root
}
