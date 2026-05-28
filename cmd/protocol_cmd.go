package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

var protocolCmd = &cobra.Command{
	Use:   "protocol",
	Short: "Manage hali:// URL protocol handler registration",
}

var protocolInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Register hali:// URL handler for this user",
	RunE: func(_ *cobra.Command, _ []string) error {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}
		exe, err = filepath.Abs(exe)
		if err != nil {
			return fmt.Errorf("normalize executable path: %w", err)
		}

		switch runtime.GOOS {
		case "windows":
			if err := installProtocolWindows(exe); err != nil {
				return err
			}
		case "linux":
			if err := installProtocolLinux(exe); err != nil {
				return err
			}
		default:
			return fmt.Errorf("protocol install not supported on %s", runtime.GOOS)
		}

		fmt.Println("Registered hali:// handler successfully.")
		fmt.Printf("Handler command: %s open %%u\n", exe)
		return nil
	},
}

var protocolUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove hali:// URL handler registration for this user",
	RunE: func(_ *cobra.Command, _ []string) error {
		switch runtime.GOOS {
		case "windows":
			if err := uninstallProtocolWindows(); err != nil {
				return err
			}
		case "linux":
			if err := uninstallProtocolLinux(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("protocol uninstall not supported on %s", runtime.GOOS)
		}
		fmt.Println("Removed hali:// handler registration.")
		return nil
	},
}

var protocolStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current hali:// URL handler registration status",
	RunE: func(_ *cobra.Command, _ []string) error {
		switch runtime.GOOS {
		case "windows":
			registered, command, err := protocolStatusWindows()
			if err != nil {
				return err
			}
			if !registered {
				fmt.Println("hali:// handler: not registered")
				return nil
			}
			fmt.Println("hali:// handler: registered")
			fmt.Printf("command: %s\n", command)
			return nil
		case "linux":
			registered, command, err := protocolStatusLinux()
			if err != nil {
				return err
			}
			if !registered {
				fmt.Println("hali:// handler: not registered")
				return nil
			}
			fmt.Println("hali:// handler: registered")
			fmt.Printf("desktop entry: %s\n", command)
			return nil
		default:
			return fmt.Errorf("protocol status not supported on %s", runtime.GOOS)
		}
	},
}

func configureProtocolCommands() {
	protocolCmd.AddCommand(protocolInstallCmd, protocolUninstallCmd, protocolStatusCmd)
}

func runProtocolCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), msg)
	}
	return nil
}

func runProtocolCommandOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(string(out)), nil
}

func installProtocolWindows(exe string) error {
	commandValue := fmt.Sprintf("\"%s\" open \"%%1\"", exe)
	if err := runProtocolCommand("reg", "add", "HKCU\\Software\\Classes\\hali", "/ve", "/d", "URL:Hali Protocol", "/f"); err != nil {
		return err
	}
	if err := runProtocolCommand("reg", "add", "HKCU\\Software\\Classes\\hali", "/v", "URL Protocol", "/d", "", "/f"); err != nil {
		return err
	}
	if err := runProtocolCommand("reg", "add", "HKCU\\Software\\Classes\\hali\\DefaultIcon", "/ve", "/d", exe+",0", "/f"); err != nil {
		return err
	}
	if err := runProtocolCommand("reg", "add", "HKCU\\Software\\Classes\\hali\\shell\\open\\command", "/ve", "/d", commandValue, "/f"); err != nil {
		return err
	}
	return nil
}

func uninstallProtocolWindows() error {
	_ = runProtocolCommand("reg", "delete", "HKCU\\Software\\Classes\\hali", "/f")
	return nil
}

func protocolStatusWindows() (bool, string, error) {
	out, err := runProtocolCommandOutput("reg", "query", "HKCU\\Software\\Classes\\hali\\shell\\open\\command", "/ve")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unable to find") {
			return false, "", nil
		}
		return false, "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "REG_SZ") {
			parts := strings.SplitN(line, "REG_SZ", 2)
			if len(parts) == 2 {
				return true, strings.TrimSpace(parts[1]), nil
			}
		}
	}
	return true, "", nil
}

func installProtocolLinux(exe string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home: %w", err)
	}
	dir := filepath.Join(home, ".local", "share", "applications")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create applications dir: %w", err)
	}
	desktopPath := filepath.Join(dir, "hali.desktop")
	content := fmt.Sprintf("[Desktop Entry]\nName=Hali\nComment=AI Model Cache System\nExec=\"%s\" open %%u\nType=Application\nTerminal=false\nNoDisplay=true\nMimeType=x-scheme-handler/hali;\n", strings.ReplaceAll(exe, "\"", "\\\""))
	if err := os.WriteFile(desktopPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write desktop entry: %w", err)
	}
	_ = runProtocolCommand("xdg-mime", "default", "hali.desktop", "x-scheme-handler/hali")
	_ = runProtocolCommand("update-desktop-database", dir)
	return nil
}

func uninstallProtocolLinux() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home: %w", err)
	}
	desktopPath := filepath.Join(home, ".local", "share", "applications", "hali.desktop")
	_ = os.Remove(desktopPath)
	return nil
}

func protocolStatusLinux() (bool, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, "", fmt.Errorf("resolve user home: %w", err)
	}
	desktopPath := filepath.Join(home, ".local", "share", "applications", "hali.desktop")
	data, err := os.ReadFile(desktopPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("read desktop entry: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Exec=") {
			return true, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Exec=")), nil
		}
	}
	return true, desktopPath, nil
}
