package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/YYYSSSRRR/codepilot/internal/permission"
)

// Config holds all application configuration.
type Config struct {
	APIKey       string
	BaseURL      string
	Model        string
	SmallModel   string // cheaper model for memory classification (falls back to Model)
	MaxTokens    int
	WorkingDir   string
	Settings     *permission.Settings
	SettingsPath string // path to the settings file that was loaded
}

// settingsFile mirrors the JSON structure in settings.json.
type settingsFile struct {
	APIKey          string                    `json:"apiKey"`
	BaseURL         string                    `json:"baseUrl"`
	Model           string                    `json:"model"`
	SmallModel      string                    `json:"smallModel,omitempty"`
	MaxTokens       int                       `json:"maxTokens,omitempty"`
	PermissionMode  permission.PermissionMode `json:"permissionMode,omitempty"`
	PermissionRules []permission.PermissionRule `json:"permissionRules,omitempty"`
}

// Load reads configuration from ~/.codepilot/settings.json (global) or
// <cwd>/.codepilot/settings.json (local). Local takes precedence when both exist.
// WorkingDir is set to the current working directory (the user's project).
func Load() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	globalPath := filepath.Join(homeDir, ".codepilot", "settings.json")

	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working dir: %w", err)
	}
	localPath := filepath.Join(wd, ".codepilot", "settings.json")

	// Select which path to use
	globalExists := fileExists(globalPath)
	localExists := fileExists(localPath)

	var settingsPath string
	switch {
	case localExists:
		settingsPath = localPath
	case globalExists:
		settingsPath = globalPath
	default:
		return nil, fmt.Errorf("no settings.json found — checked %s and %s", globalPath, localPath)
	}

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
		pMode = permission.ModeDefault
	}

	maxTokens := sf.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}

	return &Config{
		APIKey:       sf.APIKey,
		BaseURL:      sf.BaseURL,
		Model:        sf.Model,
		SmallModel:   sf.SmallModel,
		MaxTokens:    maxTokens,
		WorkingDir:   wd,
		SettingsPath: settingsPath,
		Settings: &permission.Settings{
			PermissionMode:  pMode,
			PermissionRules: sf.PermissionRules,
		},
	}, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
