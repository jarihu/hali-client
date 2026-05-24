package cmd

import (
	"sync"
	"testing"

	"hali/editionapi"

	"github.com/spf13/cobra"
)

var (
	rootForTestsOnce sync.Once
	rootForTests     *cobra.Command
)

func testRootCmd(t *testing.T) *cobra.Command {
	t.Helper()
	rootForTestsOnce.Do(func() {
		rootForTests = NewRootCmd(&editionapi.Runtime{})
	})
	if rootForTests == nil {
		t.Fatal("root command was not constructed")
	}
	return rootForTests
}

func findSubcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, sub := range cmd.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	return nil
}

func assertHasSubcommands(t *testing.T, cmd *cobra.Command, names []string) {
	t.Helper()
	for _, name := range names {
		if findSubcommand(cmd, name) == nil {
			t.Fatalf("missing subcommand %q under %q", name, cmd.Name())
		}
	}
}

func TestNewRootCmdIdentity(t *testing.T) {
	root := testRootCmd(t)
	if root.Name() != "hali" {
		t.Fatalf("root command name = %q, want %q", root.Name(), "hali")
	}
	if root.Use != "hali" {
		t.Fatalf("root Use = %q, want %q", root.Use, "hali")
	}
}

func TestNewRootCmdTopLevelTree(t *testing.T) {
	root := testRootCmd(t)
	assertHasSubcommands(t, root, []string{
		"daemon",
		"export",
		"runtime",
		"service",
		"telemetry",
		"config",
		"profile",
		"pull",
		"search",
		"list",
		"stats",
		"version",
		"completion",
	})
}

func TestNewRootCmdDaemonSubtree(t *testing.T) {
	root := testRootCmd(t)
	daemon := findSubcommand(root, "daemon")
	if daemon == nil {
		t.Fatal("daemon command not registered")
	}
	assertHasSubcommands(t, daemon, []string{"start", "stop", "status", "_run"})
}

func TestNewRootCmdExportSubtree(t *testing.T) {
	root := testRootCmd(t)
	export := findSubcommand(root, "export")
	if export == nil {
		t.Fatal("export command not registered")
	}
	assertHasSubcommands(t, export, []string{"all", "ollama", "lmstudio"})
}

func TestNewRootCmdRuntimeSubtree(t *testing.T) {
	root := testRootCmd(t)
	runtime := findSubcommand(root, "runtime")
	if runtime == nil {
		t.Fatal("runtime command not registered")
	}
	assertHasSubcommands(t, runtime, []string{"list"})
}

func TestNewRootCmdServiceSubtree(t *testing.T) {
	root := testRootCmd(t)
	service := findSubcommand(root, "service")
	if service == nil {
		t.Fatal("service command not registered")
	}
	assertHasSubcommands(t, service, []string{
		"install",
		"uninstall",
		"start",
		"stop",
		"status",
	})
}

func TestNewRootCmdConfigSubtree(t *testing.T) {
	root := testRootCmd(t)
	configCmd := findSubcommand(root, "config")
	if configCmd == nil {
		t.Fatal("config command not registered")
	}
	assertHasSubcommands(t, configCmd, []string{"show", "set"})
}
