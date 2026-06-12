package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ServerConfig describes how to connect to an MCP server via stdio transport.
type ServerConfig struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// LoadConfigs reads all MCP server configs from ~/.codepilot/mcp/*.json.
func LoadConfigs() ([]ServerConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	dir := filepath.Join(home, ".codepilot", "mcp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read mcp dir: %w", err)
	}

	var configs []ServerConfig
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcp: skip %s: %v\n", entry.Name(), err)
			continue
		}
		var cfg ServerConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "mcp: skip %s: %v\n", entry.Name(), err)
			continue
		}
		if cfg.Command == "" {
			fmt.Fprintf(os.Stderr, "mcp: skip %s: missing command\n", entry.Name())
			continue
		}
		if cfg.Name == "" {
			cfg.Name = strings.TrimSuffix(entry.Name(), ".json")
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}
