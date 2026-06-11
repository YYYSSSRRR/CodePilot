package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type GlobTool struct{}

func (t *GlobTool) Name() string        { return "Glob" }
func (t *GlobTool) Description() string { return "List files matching a glob pattern, sorted by modification time (newest first)." }

func (t *GlobTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Glob pattern (e.g. **/*.go, src/**/*.ts).",
			},
			"headLimit": map[string]any{
				"type":        "number",
				"description": "Maximum number of results to return.",
			},
		},
		"required": []string{"pattern"},
	}
}

type globEntry struct {
	path string
	mod  time.Time
}

func (t *GlobTool) Call(ctx context.Context, input map[string]any) (string, error) {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	headLimit := 0
	if hl, ok := input["headLimit"].(float64); ok {
		headLimit = int(hl)
	}

	var entries []globEntry
	root := "."
	hasStarStar := strings.Contains(pattern, "**")

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible files
		}
		if info.IsDir() {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		matched := false
		if hasStarStar {
			// For ** patterns, match the pattern against the walk path
			matched, _ = filepath.Match(pattern, path)
		}
		if !matched {
			// Also try matching against just the filename
			matched, _ = filepath.Match(pattern, info.Name())
		}
		if !matched {
			// Try the basename pattern
			basePattern := filepath.Base(pattern)
			if basePattern != pattern && basePattern != "." {
				matched, _ = filepath.Match(basePattern, info.Name())
			}
		}
		if matched {
			entries = append(entries, globEntry{path, info.ModTime()})
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("glob walk: %w", err)
	}

	// Sort by modification time, newest first
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].mod.After(entries[j].mod)
	})

	if headLimit > 0 && len(entries) > headLimit {
		entries = entries[:headLimit]
	}

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("%s  %s\n", e.mod.Format("Jan 02 15:04"), e.path))
	}
	return sb.String(), nil
}

func (t *GlobTool) MaxResultSize() int { return 50000 }

func (t *GlobTool) IsConcurrencySafe(input map[string]any) bool { return true }

func (t *GlobTool) IsReadOnly(input map[string]any) bool { return true }

func (t *GlobTool) CheckPermissions(input map[string]any) (bool, string, string, error) {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return false, "deny", "pattern is required", nil
	}
	return true, "", "", nil
}

func (t *GlobTool) ValidateInput(input map[string]any) error {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return fmt.Errorf("pattern is required")
	}
	return nil
}