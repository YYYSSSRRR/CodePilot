package memoryutils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/YYYSSSRRR/codepilot/internal/api"
	"github.com/YYYSSSRRR/codepilot/internal/memory"
	"github.com/YYYSSSRRR/codepilot/internal/transcript"
	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// PrefetchConfig holds the configuration for the async memory prefetch.
type PrefetchConfig struct {
	APIKey     string
	BaseURL    string
	SmallModel string // falls back to main model if empty
}

// Prefetch starts an async goroutine that finds memories relevant to the
// user query using a small model classifier, and delivers them as user
// messages. The channel is closed after delivery. Returns nil immediately
// if no git root is found (memory directory unavailable).
func Prefetch(ctx context.Context, cfg PrefetchConfig, userQuery string) <-chan []types.Message {
	dir := memoryDir()
	if dir == "" {
		return nil
	}

	ch := make(chan []types.Message, 1)
	go func() {
		store := memory.NewStore(dir)
		classifier := makeClassifier(cfg)
		retriever := memory.NewRetriever(store, classifier)

		files, err := retriever.FindRelevant(ctx, userQuery, 5)
		if err != nil || len(files) == 0 {
			close(ch)
			return
		}

		msgs := make([]types.Message, 0, len(files))
		for _, f := range files {
			msgs = append(msgs, formatMessage(f))
		}
		ch <- msgs
		close(ch)
	}()
	return ch
}

// ---------------------------------------------------------------------------
// Memory directory
// ---------------------------------------------------------------------------

func memoryDir() string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	gitRoot := strings.TrimSpace(string(output))
	if gitRoot == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	hash := transcript.ProjectDirHash(gitRoot)
	return filepath.Join(home, ".codepilot", "projects", hash, "memory")
}

// ---------------------------------------------------------------------------
// Classifier
// ---------------------------------------------------------------------------

func makeClassifier(cfg PrefetchConfig) memory.ClassifierFunc {
	return func(ctx context.Context, query string, entries []memory.IndexEntry) ([]memory.IndexEntry, error) {
		model := cfg.SmallModel
		if model == "" {
			model = "deepseek-chat"
		}

		prompt := memory.ClassificationPrompt(query, entries)
		req := &api.Request{
			Model:     model,
			MaxTokens: 300,
			System:    "You are a memory retrieval system. Respond with ONLY a JSON array of relevant memory names.",
			Messages:  []api.APIMessage{{Role: "user", Content: prompt}},
		}
		client := api.NewDeepSeek(cfg.APIKey, cfg.BaseURL)
		resp, err := client.CallMessages(ctx, req)
		if err != nil {
			return nil, err
		}

		names := memory.ParseClassification(resp)
		nameSet := make(map[string]bool, len(names))
		for _, n := range names {
			nameSet[n] = true
		}
		var result []memory.IndexEntry
		for _, entry := range entries {
			if nameSet[entry.Name] {
				result = append(result, entry)
			}
		}
		return result, nil
	}
}

// ---------------------------------------------------------------------------
// Formatting
// ---------------------------------------------------------------------------

func formatMessage(f memory.File) types.Message {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Memory: %s (%s)\n\n", f.Meta.Name, f.Meta.Type))
	b.WriteString(f.Content)
	return types.NewMessage("user", []types.ContentBlock{
		{Type: types.ContentBlockText, Text: b.String()},
	})
}
