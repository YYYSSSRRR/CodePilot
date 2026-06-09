package permissions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LoadSettings merges settings from multiple layers:
// built-in defaults → user global ~/.codepilot/settings.json → project .codepilot/settings.json.
// Later layers override earlier ones (deep merge of rules).
func LoadSettings(projectDir string) (*Settings, error) {
	s := Default()

	// Global
	home, err := os.UserHomeDir()
	if err == nil {
		globalPath := filepath.Join(home, ".codepilot", "settings.json")
		if _, err := os.Stat(globalPath); err == nil {
			merged, err := mergeFile(s, globalPath, SourceGlobal)
			if err != nil {
				return nil, fmt.Errorf("global settings: %w", err)
			}
			s = merged
		}
	}

	// Project-local
	projectPath := filepath.Join(projectDir, ".codepilot", "settings.json")
	if _, err := os.Stat(projectPath); err == nil {
		merged, err := mergeFile(s, projectPath, SourceProject)
		if err != nil {
			return nil, fmt.Errorf("project settings: %w", err)
		}
		s = merged
	}

	return s, nil
}

func Default() *Settings {
	return &Settings{
		PermissionMode:  ModeDefault,
		PermissionRules: nil,
	}
}

func mergeFile(base *Settings, path string, source RuleSource) (*Settings, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var fileSettings Settings
	if err := json.NewDecoder(f).Decode(&fileSettings); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	merged := *base
	if fileSettings.PermissionMode != "" {
		merged.PermissionMode = fileSettings.PermissionMode
	}

	// Mark rules with their source and append.
	merged.PermissionRules = make([]PermissionRule, 0, len(base.PermissionRules)+len(fileSettings.PermissionRules))
	merged.PermissionRules = append(merged.PermissionRules, base.PermissionRules...)
	for _, r := range fileSettings.PermissionRules {
		r.Source = source
		merged.PermissionRules = append(merged.PermissionRules, r)
	}

	return &merged, nil
}