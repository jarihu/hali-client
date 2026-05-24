package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	powershellCompletionStartMarker = "# hali completion start"
	powershellCompletionEndMarker   = "# hali completion end"
)

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	completionCmd := &cobra.Command{
		Use:   "completion",
		Short: "Generate or install shell completions",
		Long: `Generate shell completion scripts.

For PowerShell, running 'hali completion powershell' installs completion into
your PowerShell profile by default.

Use 'hali completion powershell --stdout' to print the raw script.`,
		Args: cobra.NoArgs,
	}

	completionCmd.AddCommand(
		newBashCompletionCmd(root),
		newZshCompletionCmd(root),
		newFishCompletionCmd(root),
		newPowerShellCompletionCmd(root),
	)

	return completionCmd
}

func newBashCompletionCmd(root *cobra.Command) *cobra.Command {
	var noDescriptions bool
	cmd := &cobra.Command{
		Use:   "bash",
		Short: "Generate the autocompletion script for bash",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if noDescriptions {
				return root.GenBashCompletionV2(cmd.OutOrStdout(), true)
			}
			return root.GenBashCompletion(cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&noDescriptions, "no-descriptions", false, "Disable completion descriptions")
	return cmd
}

func newZshCompletionCmd(root *cobra.Command) *cobra.Command {
	var noDescriptions bool
	cmd := &cobra.Command{
		Use:   "zsh",
		Short: "Generate the autocompletion script for zsh",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if noDescriptions {
				return root.GenZshCompletionNoDesc(cmd.OutOrStdout())
			}
			return root.GenZshCompletion(cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&noDescriptions, "no-descriptions", false, "Disable completion descriptions")
	return cmd
}

func newFishCompletionCmd(root *cobra.Command) *cobra.Command {
	var noDescriptions bool
	cmd := &cobra.Command{
		Use:   "fish",
		Short: "Generate the autocompletion script for fish",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return root.GenFishCompletion(cmd.OutOrStdout(), !noDescriptions)
		},
	}
	cmd.Flags().BoolVar(&noDescriptions, "no-descriptions", false, "Disable completion descriptions")
	return cmd
}

func newPowerShellCompletionCmd(root *cobra.Command) *cobra.Command {
	var noDescriptions bool
	var stdout bool
	var profilePath string

	cmd := &cobra.Command{
		Use:   "powershell",
		Short: "Install or generate the autocompletion script for powershell",
		Long: `By default this installs hali completion into your PowerShell profile.

Use --stdout to print the script instead.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var script bytes.Buffer
			if noDescriptions {
				if err := root.GenPowerShellCompletion(&script); err != nil {
					return err
				}
			} else {
				if err := root.GenPowerShellCompletionWithDesc(&script); err != nil {
					return err
				}
			}

			if stdout {
				_, err := cmd.OutOrStdout().Write(script.Bytes())
				return err
			}

			target := strings.TrimSpace(profilePath)
			if target == "" {
				var err error
				target, err = defaultPowerShellProfilePath()
				if err != nil {
					return err
				}
			}

			block := buildPowerShellCompletionBlock(script.String())
			action, err := installPowerShellCompletionBlock(target, block)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "PowerShell completion %s profile: %s\n", action, target)
			fmt.Fprintf(cmd.OutOrStdout(), "Reload with: . %s\n", target)
			return nil
		},
	}

	cmd.Flags().BoolVar(&noDescriptions, "no-descriptions", false, "Disable completion descriptions")
	cmd.Flags().BoolVar(&stdout, "stdout", false, "Print the script to stdout instead of installing to profile")
	cmd.Flags().StringVar(&profilePath, "profile", "", "PowerShell profile path for installation (defaults to $PROFILE)")
	return cmd
}

func defaultPowerShellProfilePath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("PROFILE")); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"), nil
}

func buildPowerShellCompletionBlock(script string) string {
	cleanScript := strings.TrimSpace(script)
	return powershellCompletionStartMarker + "\n" + cleanScript + "\n" + powershellCompletionEndMarker + "\n"
}

func installPowerShellCompletionBlock(profilePath, block string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(profilePath), 0755); err != nil {
		return "", fmt.Errorf("create profile dir: %w", err)
	}

	existing, err := os.ReadFile(profilePath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read profile: %w", err)
	}

	content := string(existing)
	start := strings.Index(content, powershellCompletionStartMarker)
	end := strings.Index(content, powershellCompletionEndMarker)

	action := "installed in"
	var updated string
	if start >= 0 && end > start {
		action = "updated in"
		end += len(powershellCompletionEndMarker)
		if end < len(content) && content[end] == '\n' {
			end++
		}
		updated = content[:start] + block + content[end:]
	} else {
		updated = content
		if strings.TrimSpace(updated) != "" && !strings.HasSuffix(updated, "\n") {
			updated += "\n"
		}
		if strings.TrimSpace(updated) != "" {
			updated += "\n"
		}
		updated += block
	}

	if err := os.WriteFile(profilePath, []byte(updated), 0644); err != nil {
		return "", fmt.Errorf("write profile: %w", err)
	}

	return action, nil
}
