package mcp

import (
	"context"
	"fmt"
)

// Tool wraps an MCP-discovered tool as a codepilot Tool.
type Tool struct {
	client      *Client
	name        string
	description string
	inputSchema map[string]any
}

// NewTool creates a new MCP tool wrapper.
func NewTool(client *Client, def ToolDef) *Tool {
	return &Tool{
		client:      client,
		name:        def.Name,
		description: def.Description,
		inputSchema: def.InputSchema,
	}
}

func (t *Tool) Name() string                    { return t.name }
func (t *Tool) Description() string             { return t.description }
func (t *Tool) InputSchema() map[string]any     { return t.inputSchema }
func (t *Tool) MaxResultSize() int              { return 0 }
func (t *Tool) IsConcurrencySafe(map[string]any) bool { return true }
func (t *Tool) IsReadOnly(map[string]any) bool  { return true }

func (t *Tool) CheckPermissions(map[string]any) (bool, string, string, error) {
	return true, "", "", nil
}

func (t *Tool) ValidateInput(input map[string]any) error {
	return nil
}

func (t *Tool) Call(ctx context.Context, input map[string]any) (string, error) {
	result, err := t.client.CallTool(ctx, t.name, input)
	if err != nil {
		return result, fmt.Errorf("MCP tool %q error: %w", t.name, err)
	}
	return result, nil
}
