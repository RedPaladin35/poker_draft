package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Load reads configuration from the given path (or default locations if path
// is empty), applies environment variable overrides, and validates the result.
//
// Config files are JSON (.json) or YAML (.yaml/.yml).
// NOTE: YAML parsing requires gopkg.in/yaml.v3 — add it with:
//
//	go get gopkg.in/yaml.v3
//
// In this build, JSON is used as the config format.
// Search order when path == "":
//  1. ./config.json  (working directory)
//  2. ~/.poker/config.json
func Load(path string) (*Config, error) {
	cfg := Default()

	if path == "" {
		path = findConfigFile()
	}
	if path != "" {
		if err := loadJSON(path, cfg); err != nil {
			return nil, fmt.Errorf("Load: %w", err)
		}
	}

	applyEnv(cfg)

	// Expand ~ in DataDir.
	if len(cfg.DataDir) > 0 && cfg.DataDir[0] == '~' {
		home, err := os.UserHomeDir()
		if err == nil {
			cfg.DataDir = filepath.Join(home, cfg.DataDir[1:])
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("Load: invalid config: %w", err)
	}
	return cfg, nil
}

// LoadOrDefault returns the loaded config, or the default if no file found.
func LoadOrDefault(path string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = Default()
			if verr := cfg.Validate(); verr != nil {
				return nil, verr
			}
			return cfg, nil
		}
		return nil, err
	}
	return cfg, nil
}

func findConfigFile() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		"config.json", "config.yaml", "config.yml",
	}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".poker", "config.json"),
			filepath.Join(home, ".poker", "config.yaml"),
		)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func loadJSON(path string, cfg *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("loadJSON: read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		return fmt.Errorf("loadJSON: unmarshal %s: %w", path, err)
	}
	return nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("POKER_PLAYER_NAME"); v != "" {
		cfg.PlayerName = v
	}
	if v := os.Getenv("POKER_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("POKER_LISTEN_ADDR"); v != "" {
		cfg.Network.ListenAddr = v
	}
	if v := os.Getenv("POKER_TABLE_ID"); v != "" {
		cfg.Game.TableID = v
	}
	if v := os.Getenv("POKER_CHAIN_RPC"); v != "" {
		cfg.Chain.RPCURL = v
	}
	if v := os.Getenv("POKER_CONTRACT_ADDR"); v != "" {
		cfg.Chain.ContractAddress = v
	}
	if v := os.Getenv("POKER_PRIVATE_KEY"); v != "" {
		cfg.Chain.PrivateKeyHex = v
	}
	if v := os.Getenv("POKER_CHAIN_ENABLED"); v == "true" || v == "1" {
		cfg.Chain.Enabled = true
	}
}

// Save writes the config as JSON to path.
func Save(cfg *Config, path string) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("Save: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("Save: mkdir: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("Save: write: %w", err)
	}
	return nil
}

func parseDuration(s string, def time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}
