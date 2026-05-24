package cmd

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"hali/internal/config"

	"github.com/spf13/cobra"
)

var configRootCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect daemon configuration",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show effective configuration values",
	RunE:  runConfigShow,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a daemon configuration value",
	Long: `Set daemon configuration values.

Supported keys:
	streaming_hash          true|false
	debug                   true|false
	telemetry.enabled       true|false
	lan.hmac_enabled        true|false
	lan.hmac_shared_secret  <64-char-hex> | default
	models_dir              <path> | default
	lmstudio_models_dir     <path> | default
	ollama_models_dir       <path> | default
	max_upload_mbps         <integer Mbps> (0 = unlimited)
	max_download_mbps       <integer Mbps> (0 = unlimited)`,
	Args: cobra.ExactArgs(2),
	RunE: runConfigSet,
}

func runConfigShow(_ *cobra.Command, _ []string) error {
	cfg, err := config.LoadService()
	if err != nil {
		return err
	}
	configPath := filepath.Join(config.ServiceDataDir(), "config.json")
	fmt.Printf("config path: %s\n", configPath)
	fmt.Printf("streaming_hash: %t\n", cfg.StreamingHash)
	fmt.Printf("debug: %t\n", cfg.DebugValue())
	fmt.Printf("effective telemetry.enabled: %t\n", cfg.TelemetryEnabledValue())
	fmt.Printf("effective telemetry.ingest_url: %s\n", cfg.RegistryIngestURLValue())
	fmt.Printf("lan.hmac_enabled: %t\n", cfg.LANHMACEnabledValue())
	fmt.Printf("lan.hmac_shared_secret: %s\n", strings.TrimSpace(cfg.LANHMACSharedSecret))

	modelsDir := strings.TrimSpace(cfg.ModelsDir)
	if modelsDir == "" {
		modelsDir = filepath.Join(config.ServiceDataDir(), "models")
	}
	fmt.Printf("models_dir: %s\n", modelsDir)
	fmt.Printf("lmstudio_models_dir: %s\n", strings.TrimSpace(cfg.LMStudioModels))
	fmt.Printf("ollama_models_dir: %s\n", strings.TrimSpace(cfg.OllamaModelsDir))
	fmt.Printf("max_upload_mbps: %d\n", cfg.MaxUploadMBps)
	fmt.Printf("max_download_mbps: %d\n", cfg.MaxDownloadMBps)

	addr, err := config.DaemonListenAddr()
	if err != nil {
		return err
	}
	fmt.Printf("daemon_listen_addr: %s\n", addr)
	return nil
}

func runConfigSet(_ *cobra.Command, args []string) error {
	key := strings.TrimSpace(strings.ToLower(args[0]))
	value := strings.TrimSpace(args[1])

	cfg, err := config.LoadService()
	if err != nil {
		return err
	}

	switch key {
	case "streaming_hash":
		parsed, err := parseBoolValue(value)
		if err != nil {
			return fmt.Errorf("invalid value for %s: %w", args[0], err)
		}
		cfg.StreamingHash = parsed
	case "debug":
		parsed, err := parseBoolValue(value)
		if err != nil {
			return fmt.Errorf("invalid value for %s: %w", args[0], err)
		}
		cfg.Debug = &parsed
	case "telemetry.enabled", "telemetry_enabled":
		parsed, err := parseBoolValue(value)
		if err != nil {
			return fmt.Errorf("invalid value for %s: %w", args[0], err)
		}
		cfg.TelemetryEnabled = &parsed
	case "telemetry.ingest_url", "registry.ingest_url", "registry_ingest_url":
		return fmt.Errorf("unsupported key %q: telemetry ingest URL is internal (dev/debug only)", args[0])
	case "lan.hmac_enabled", "lan_hmac_enabled":
		parsed, err := parseBoolValue(value)
		if err != nil {
			return fmt.Errorf("invalid value for %s: %w", args[0], err)
		}
		cfg.LANHMACEnabled = &parsed
	case "lan.hmac_shared_secret", "lan_hmac_shared_secret":
		if strings.EqualFold(value, "default") {
			cfg.LANHMACSharedSecret = ""
		} else {
			decoded, err := config.DecodeLANHMACSecret(value)
			if err != nil {
				return err
			}
			cfg.LANHMACSharedSecret = strings.ToLower(strings.TrimSpace(value))
			if len(decoded) != 32 {
				return fmt.Errorf("invalid lan.hmac_shared_secret: must decode to 32 bytes")
			}
		}
	case "models_dir":
		if strings.EqualFold(value, "default") {
			cfg.ModelsDir = ""
		} else {
			if value == "" {
				return fmt.Errorf("invalid value for %s: path cannot be empty", args[0])
			}
			cfg.ModelsDir = value
		}
	case "lmstudio_models_dir":
		if strings.EqualFold(value, "default") {
			cfg.LMStudioModels = ""
		} else {
			cfg.LMStudioModels = value
		}
	case "ollama_models_dir":
		if strings.EqualFold(value, "default") {
			cfg.OllamaModelsDir = ""
		} else {
			cfg.OllamaModelsDir = value
		}
	case "daemon_listen_addr":
		return fmt.Errorf("unsupported key %q: daemon listen address is fixed to 0.0.0.0", args[0])
	case "max_upload_mbps":
		parsed, err := parseNonNegativeInt(value)
		if err != nil {
			return fmt.Errorf("invalid value for %s: %w", args[0], err)
		}
		cfg.MaxUploadMBps = parsed
	case "max_download_mbps":
		parsed, err := parseNonNegativeInt(value)
		if err != nil {
			return fmt.Errorf("invalid value for %s: %w", args[0], err)
		}
		cfg.MaxDownloadMBps = parsed
	case "network.mode", "network_mode":
		return fmt.Errorf("unsupported key %q: network mode config was removed; only LAN mode is supported", args[0])
	default:
		return fmt.Errorf("unsupported key %q", args[0])
	}

	if err := config.SaveService(cfg); err != nil {
		return err
	}

	fmt.Printf("Updated %s\n", key)
	fmt.Printf("Config path: %s\n", filepath.Join(config.ServiceDataDir(), "config.json"))
	fmt.Println("Restart daemon to apply changes.")
	return nil
}

func parseBoolValue(raw string) (bool, error) {
	v := strings.TrimSpace(strings.ToLower(raw))
	switch v {
	case "true", "1", "yes", "on", "enabled":
		return true, nil
	case "false", "0", "no", "off", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("must be true or false")
	}
}

func parseNonNegativeInt(raw string) (int, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, fmt.Errorf("must be a non-negative integer")
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("must be a non-negative integer")
	}
	if n < 0 {
		return 0, fmt.Errorf("must be a non-negative integer")
	}
	return n, nil
}
