package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage the Hali service lifecycle",
	Long: `The service registers the hali daemon with the OS service manager so it starts
automatically on boot and restarts on crash.

The service is OPTIONAL. Without it the daemon still works — hali pull auto-launches
it when needed. The only thing missing without the service is persistence across reboots.

  Windows: Service Control Manager (SCM), service name HaliDaemon
  Linux:   systemd unit halid`,
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install and/or enable the Hali service",
	Long: `Register the hali daemon with the OS service manager.

After install the daemon starts automatically on every boot and restarts on crash.
Without the service installed you can still use hali normally — the daemon is
auto-launched by hali pull and can be started manually with hali daemon start.
The service is only needed for hands-off persistence across reboots.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := serviceInstallAction(); err != nil {
			return err
		}
		fmt.Println("Service installed/enabled.")
		return nil
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall and/or disable the Hali service",
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := serviceUninstallAction(); err != nil {
			return err
		}
		fmt.Println("Service uninstalled/disabled.")
		return nil
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Hali service",
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := serviceStartAction(); err != nil {
			return err
		}
		fmt.Println("Service started.")
		return nil
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the Hali service",
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := serviceStopAction(); err != nil {
			return err
		}
		fmt.Println("Service stopped.")
		return nil
	},
}

var serviceRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the Hali service",
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := serviceRestartAction(); err != nil {
			return err
		}
		fmt.Println("Service restarted.")
		return nil
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Hali service status",
	RunE: func(_ *cobra.Command, _ []string) error {
		status, err := serviceStatusAction()
		if err != nil {
			return err
		}
		fmt.Println(status)
		return nil
	},
}
