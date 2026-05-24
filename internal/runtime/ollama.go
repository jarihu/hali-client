package runtime

import (
	"hali/internal/config"
	"os"
	"os/exec"
	"path/filepath"
)

type OllamaRuntime struct{}

func (OllamaRuntime) Name() string {
	return "ollama"
}

func (OllamaRuntime) Detect() bool {
	path, err := (OllamaRuntime{}).ModelsPath()
	if err == nil {
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			return true
		}
	}
	_, err = exec.LookPath("ollama")
	return err == nil
}

func (OllamaRuntime) ModelsPath() (string, error) {
	if path, err := config.OllamaModelsDir(); err != nil {
		return "", err
	} else if path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ollama", "models"), nil
}
