package cmd

import (
	"fmt"

	"hali/internal/config"

	"github.com/spf13/cobra"
)

var telemetryCmd = &cobra.Command{
	Use:   "telemetry",
	Short: "Manage model pull event telemetry",
	Long:  "Enable or disable best-effort pull event delivery from the daemon.",
}

var telemetryEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable telemetry delivery",
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := setTelemetryEnabled(true); err != nil {
			return err
		}
		fmt.Println("Telemetry enabled.")
		return nil
	},
}

var telemetryDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable telemetry delivery",
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := setTelemetryEnabled(false); err != nil {
			return err
		}
		fmt.Println("Telemetry disabled. Queued events will remain on disk.")
		return nil
	},
}

var telemetryStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show telemetry status",
	RunE: func(_ *cobra.Command, _ []string) error {
		cfg, err := config.LoadService()
		if err != nil {
			return err
		}
		state := "enabled"
		if !cfg.TelemetryEnabledValue() {
			state = "disabled"
		}
		fmt.Printf("Telemetry %s\n", state)
		return nil
	},
}

func setTelemetryEnabled(enabled bool) error {
	cfg, err := config.LoadService()
	if err != nil {
		return err
	}
	cfg.TelemetryEnabled = &enabled
	return config.SaveService(cfg)
}
