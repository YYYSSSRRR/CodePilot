package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type GrepTool struct{}

func (t *GrepTool) Name() string        { return "Grep" }
func (t *GrepTool) Description() string { return "Search files for a pattern using ripgrep or grep." }

func (t *GrepTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "The search pattern (regex).",
			},
			"include": map[string]any{
				"type":        "string",
				"description": "File glob pattern to filter (e.g. *.go).",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory to search in (default: current directory).",
			},
			"headLimit": map[string]any{
				"type":        "number",
				"description": "Maximum number of result lines to return.",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) Call(ctx context.Context, input map[string]any) (string, error) {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	include, _ := input["include"].(string)
	path, _ := input["path"].(string)
	if path == "" {
		path = "."
	}
	headLimit := 0
	if hl, ok := input["headLimit"].(float64); ok {
		headLimit = int(hl)
	}

	// Try ripgrep first, fall back to grep
	output, rgErr := t.tryRG(ctx, pattern, include, path, headLimit)
	if rgErr == nil {
		return t.applyHeadLimit(string(output), headLimit), nil
	}

	// Fallback to grep
	output, err := t.tryGrep(ctx, pattern, include, path)
	if err != nil {
		// grep exits 1 when no matches found
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// Check if it really is "no matches" vs a real error
			if len(exitErr.Stderr) == 0 {
				return "", nil
			}
		}
		return "", fmt.Errorf("search failed: %w", err)
	}
	return t.applyHeadLimit(string(output), headLimit), nil
}

func (t *GrepTool) tryRG(ctx context.Context, pattern, include, path string, headLimit int) ([]byte, error) {
	args := []string{"--no-heading", "-n"}
	if include != "" {
		args = append(args, "--glob", include)
	}
	if headLimit > 0 {
		args = append(args, "--max-count", fmt.Sprintf("%d", headLimit))
	}
	args = append(args, pattern, path)
	return exec.CommandContext(ctx, "rg", args...).Output()
}

func (t *GrepTool) tryGrep(ctx context.Context, pattern, include, path string) ([]byte, error) {
	args := []string{"-r", "-n"}
	if include != "" {
		args = append(args, "--include="+include)
	}
	args = append(args, pattern, path)
	return exec.CommandContext(ctx, "grep", args...).Output()
}

func (t *GrepTool) applyHeadLimit(output string, headLimit int) string {
	if headLimit <= 0 {
		return output
	}
	lines := strings.SplitN(output, "\n", headLimit+1)
	if len(lines) > headLimit {
		return strings.Join(lines[:headLimit], "\n") + "\n... (truncated)"
	}
	return output
}

func (t *GrepTool) MaxResultSize() int { return 100000 }

func (t *GrepTool) IsConcurrencySafe(input map[string]any) bool { return true }

func (t *GrepTool) IsReadOnly(input map[string]any) bool { return true }

func (t *GrepTool) CheckPermissions(input map[string]any) (bool, string, string, error) {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return false, "deny", "pattern is required", nil
	}
	return true, "", "", nil
}

func (t *GrepTool) ValidateInput(input map[string]any) error {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return fmt.Errorf("pattern is required")
	}
	return nil
}