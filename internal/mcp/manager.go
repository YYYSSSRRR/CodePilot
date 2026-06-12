package mcp

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/YYYSSSRRR/codepilot/internal/tool"
)

// Manager manages multiple MCP client connections and their tools.
type Manager struct {
	mu      sync.Mutex
	clients []*Client
	tools   []tool.Tool
}

// NewManager connects to all configured MCP servers and collects their tools.
func NewManager(ctx context.Context) (*Manager, error) {
	configs, err := LoadConfigs()
	if err != nil {
		return nil, fmt.Errorf("load mcp configs: %w", err)
	}

	m := &Manager{}
	for _, cfg := range configs {
		client, err := Connect(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcp: %s — %v\n", cfg.Name, err)
			continue
		}
		m.clients = append(m.clients, client)
		for _, td := range client.Tools() {
			m.tools = append(m.tools, NewTool(client, td))
		}
		fmt.Fprintf(os.Stderr, "mcp: connected %s (%d tools)\n", cfg.Name, len(client.Tools()))
	}
	return m, nil
}

// Tools returns all MCP-discovered tools.
func (m *Manager) Tools() []tool.Tool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]tool.Tool, len(m.tools))
	copy(out, m.tools)
	return out
}

// Close shuts down all MCP clients.
func (m *Manager) Close() {
	for _, c := range m.clients {
		c.Close()
	}
}
