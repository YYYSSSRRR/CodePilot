package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type WriteTool struct{}

func (t *WriteTool) Name() string        { return "Write" }
func (t *WriteTool) Description() string { return "Write content to a file, creating it if necessary." }

func (t *WriteTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The absolute or relative path to the file.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to write to the file.",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteTool) Call(ctx context.Context, input map[string]any) (string, error) {
	path, _ := input["path"].(string)
	content, _ := input["content"].(string)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return "", fmt.Errorf("create parent directories: %w", err)
	}

	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("wrote %d bytes to %s", len(content), absPath), nil
}

func (t *WriteTool) MaxResultSize() int { return 0 }

func (t *WriteTool) IsConcurrencySafe(input map[string]any) bool { return false }

func (t *WriteTool) IsReadOnly(input map[string]any) bool { return false }

func (t *WriteTool) CheckPermissions(input map[string]any) (bool, string, string, error) {
	return false, "ask", "this will modify or create a file", nil
}

func (t *WriteTool) ValidateInput(input map[string]any) error {
	path, _ := input["path"].(string)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	_, hasContent := input["content"]
	if !hasContent {
		return fmt.Errorf("content is required")
	}
	return nil
}