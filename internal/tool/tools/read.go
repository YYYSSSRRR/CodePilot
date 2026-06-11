package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type ReadTool struct{}

func (t *ReadTool) Name() string        { return "Read" }
func (t *ReadTool) Description() string { return "Read the contents of a file." }

func (t *ReadTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The absolute or relative path to the file.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadTool) Call(ctx context.Context, input map[string]any) (string, error) {
	path, _ := input["path"].(string)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	return string(data), nil
}

func (t *ReadTool) MaxResultSize() int { return 0 }

func (t *ReadTool) IsConcurrencySafe(input map[string]any) bool { return true }

func (t *ReadTool) IsReadOnly(input map[string]any) bool { return true }

func (t *ReadTool) CheckPermissions(input map[string]any) (bool, string, string, error) {
	path, _ := input["path"].(string)
	if path == "" {
		return false, "deny", "path is required", nil
	}
	return true, "", "", nil
}

func (t *ReadTool) ValidateInput(input map[string]any) error {
	path, _ := input["path"].(string)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	return nil
}