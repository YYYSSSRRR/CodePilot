package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/YYYSSSRRR/codepilot/internal/permissions"
)

// Config holds all application configuration loaded from ~/.codepilot/settings.json.
type Config struct {
	APIKey       string
	BaseURL      string
	Model        string
	WorkingDir   string
	Settings     *permissions.Settings
	SettingsPath string
}

// settingsFile mirrors the JSON structure in ~/.codepilot/settings.json.
type settingsFile struct {
	APIKey          string                      `json:"apiKey"`
	BaseURL         string                      `json:"baseUrl"`
	Model           string                      `json:"model"`
	PermissionMode  permissions.PermissionMode  `json:"permissionMode,omitempty"`
	PermissionRules []permissions.PermissionRule `json:"permissionRules,omitempty"`
}

// Load reads all configuration from ~/.codepilot/settings.json.
func Load() (*Config, error) {
	home, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	settingsPath := filepath.Join(home, ".codepilot", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", settingsPath, err)
	}

	var sf settingsFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("decode %s: %w", settingsPath, err)
	}

	if sf.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required in %s", settingsPath)
	}
	if sf.BaseURL == "" {
		return nil, fmt.Errorf("baseUrl is required in %s", settingsPath)
	}
	if sf.Model == "" {
		return nil, fmt.Errorf("model is required in %s", settingsPath)
	}

	pMode := sf.PermissionMode
	if pMode == "" {
		pMode = permissions.ModeDefault
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working dir: %w", err)
	}

	return &Config{
		APIKey:       sf.APIKey,
		BaseURL:      sf.BaseURL,
		Model:        sf.Model,
		WorkingDir:   wd,
		SettingsPath: settingsPath,
		Settings: &permissions.Settings{
			PermissionMode:  pMode,
			PermissionRules: sf.PermissionRules,
		},
	}, nil
}