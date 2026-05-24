package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPowerShellCompletionBlockIncludesMarkers(t *testing.T) {
	block := buildPowerShellCompletionBlock("  script-body\n")
	if !strings.Contains(block, powershellCompletionStartMarker) {
		t.Fatal("missing start marker")
	}
	if !strings.Contains(block, powershellCompletionEndMarker) {
		t.Fatal("missing end marker")
	}
	if !strings.Contains(block, "script-body") {
		t.Fatal("missing script body")
	}
}

func TestInstallPowerShellCompletionBlockInstallsThenUpdates(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "Microsoft.PowerShell_profile.ps1")

	action, err := installPowerShellCompletionBlock(profile, buildPowerShellCompletionBlock("first"))
	if err != nil {
		t.Fatalf("installPowerShellCompletionBlock install: %v", err)
	}
	if action != "installed in" {
		t.Fatalf("action = %q, want installed in", action)
	}

	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("ReadFile after install: %v", err)
	}
	content := string(data)
	if strings.Count(content, powershellCompletionStartMarker) != 1 {
		t.Fatalf("start marker count = %d, want 1", strings.Count(content, powershellCompletionStartMarker))
	}
	if !strings.Contains(content, "first") {
		t.Fatal("missing first script content")
	}

	action, err = installPowerShellCompletionBlock(profile, buildPowerShellCompletionBlock("second"))
	if err != nil {
		t.Fatalf("installPowerShellCompletionBlock update: %v", err)
	}
	if action != "updated in" {
		t.Fatalf("action = %q, want updated in", action)
	}

	data, err = os.ReadFile(profile)
	if err != nil {
		t.Fatalf("ReadFile after update: %v", err)
	}
	content = string(data)
	if strings.Count(content, powershellCompletionStartMarker) != 1 {
		t.Fatalf("start marker count = %d, want 1", strings.Count(content, powershellCompletionStartMarker))
	}
	if strings.Contains(content, "first") {
		t.Fatal("old script content should be replaced")
	}
	if !strings.Contains(content, "second") {
		t.Fatal("missing updated script content")
	}
}

func TestDefaultPowerShellProfilePathUsesEnvProfile(t *testing.T) {
	want := `C:\\Users\\tester\\Documents\\PowerShell\\Microsoft.PowerShell_profile.ps1`
	t.Setenv("PROFILE", want)
	got, err := defaultPowerShellProfilePath()
	if err != nil {
		t.Fatalf("defaultPowerShellProfilePath: %v", err)
	}
	if got != want {
		t.Fatalf("defaultPowerShellProfilePath = %q, want %q", got, want)
	}
}
