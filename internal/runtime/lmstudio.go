package runtime

import (
	"encoding/json"
	"hali/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type LMStudioRuntime struct{}

func (LMStudioRuntime) Name() string {
	return "lmstudio"
}

func (LMStudioRuntime) Detect() bool {
	modelsPath, err := (LMStudioRuntime{}).ModelsPath()
	if err == nil {
		if st, err := os.Stat(modelsPath); err == nil && st.IsDir() {
			return true
		}
	}

	for _, p := range knownInstallPaths() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return true
		}
	}

	_, err = exec.LookPath("lmstudio")
	if err == nil {
		return true
	}
	_, err = exec.LookPath("lms")
	return err == nil
}

func (LMStudioRuntime) ModelsPath() (string, error) {
	if path, err := config.LMStudioModelsDir(); err != nil {
		return "", err
	} else if path != "" {
		return path, nil
	}
	if path := lmstudioModelsPathFromSettings(knownLMStudioSettingsPaths()); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lmstudio", "models"), nil
}

type lmstudioSettings struct {
	DownloadsFolder string `json:"downloadsFolder"`
}

func lmstudioModelsPathFromSettings(paths []string) string {
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg lmstudioSettings
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		if folder := strings.TrimSpace(cfg.DownloadsFolder); folder != "" {
			return filepath.Clean(folder)
		}
	}
	return ""
}

func knownLMStudioSettingsPaths() []string {
	paths := []string{}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		paths = append(paths, filepath.Join(home, ".lmstudio", "settings.json"))
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		paths = append(paths, filepath.Join(appData, "LM Studio", "settings.json"))
	}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		paths = append(paths, filepath.Join(localAppData, "LM Studio", "settings.json"))
	}
	return paths
}

func knownInstallPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	paths := []string{}
	switch runtime.GOOS {
	case "windows":
		paths = append(paths,
			filepath.Join(home, "AppData", "Local", "Programs", "LM Studio", "LM Studio.exe"),
			filepath.Join(home, "AppData", "Local", "LM Studio", "LM Studio.exe"),
		)
	case "darwin":
		paths = append(paths, "/Applications/LM Studio.app")
	default:
		paths = append(paths,
			filepath.Join(home, ".local", "bin", "lmstudio"),
			"/usr/local/bin/lmstudio",
		)
	}
	return paths
}
