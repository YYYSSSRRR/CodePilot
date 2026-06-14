package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/YYYSSSRRR/codepilot/internal/api"
	"github.com/YYYSSSRRR/codepilot/internal/compact"
	"github.com/YYYSSSRRR/codepilot/internal/query"
	"github.com/YYYSSSRRR/codepilot/internal/token"
	"github.com/YYYSSSRRR/codepilot/internal/tool"
	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// RunnerDeps holds the dependencies needed to run a sub-agent loop.
type RunnerDeps struct {
	APIKey     string
	BaseURL    string
	Model      string
	MaxTokens  int
	ContextWin int

	Registry   *tool.Registry
	CanUseTool func(ctx context.Context, toolName string, input map[string]any, toolUseID string) (bool, string)

	// Transcript callback for recording sub-agent messages.
	RecordTranscript func(messages []types.Message)
}

// RunSubAgent runs a sub-agent with the given agent config and task.
// It reuses query.Runner internally but with its own deps and message list.
func RunSubAgent(ctx context.Context, deps RunnerDeps, agent Agent, messages []types.Message) ([]types.Message, error) {
	// Build filtered tool list from the tools the agent is allowed to use
	allowedTools := filterTools(deps.Registry, agent.Tools)

	// Determine model — agent-specific or main model
	model := agent.Model
	if model == "" {
		model = deps.Model
	}

	ctxWin := deps.ContextWin
	if ctxWin <= 0 {
		ctxWin = 128000
	}
	counter := token.NewCounter(model, ctxWin, deps.MaxTokens)

	queryDeps := query.QueryDeps{
		CallModel: func(ctx context.Context, req *api.Request) (api.Streamer, error) {
			client := api.NewDeepSeek(deps.APIKey, deps.BaseURL)
			return client.StreamMessages(ctx, req)
		},
		CanUseTool: deps.CanUseTool,
		ExecuteTool: func(ctx context.Context, toolName string, input map[string]any) (string, bool, error) {
			t := findTool(allowedTools, toolName)
			if t == nil {
				return fmt.Sprintf("tool %q is not available for agent %q", toolName, agent.Name), true, nil
			}
			if err := t.ValidateInput(input); err != nil {
				return fmt.Sprintf("invalid input: %v", err), true, nil
			}
			result, err := t.Call(ctx, input)
			if err != nil {
				return result, true, err
			}
			return result, false, nil
		},
		CompactSystem: &compact.ToolUseContext{
			ContentReplacement: &compact.ContentReplacementState{
				Replacements: make(map[string]string),
			},
			UnlimitedTools: make(map[string]bool),
		},
		Microcompact: func(msgs []types.Message) *compact.CompactResult {
			return compact.MaybeTimeBasedMicrocompact(msgs, compact.DefaultMicrocompactConfig())
		},
		AutoCompact: func(ctx context.Context, msgs []types.Message) ([]types.Message, error) {
			client := api.NewDeepSeek(deps.APIKey, deps.BaseURL)
			callLLM := func(innerCtx context.Context, prompt string) (string, error) {
				req := &api.Request{
					Model:     deps.Model,
					MaxTokens: 1024,
					Messages:  []api.APIMessage{{Role: "user", Content: prompt}},
				}
				return client.CallMessages(innerCtx, req)
			}
			return compact.AutoCompact(ctx, msgs, callLLM)
		},
		RecordTranscript: deps.RecordTranscript,
		TokenCount:           counter.TokenCount,
		AutoCompactThreshold: counter.AutoCompactThreshold,
		EffectiveWindow:      counter.EffectiveWindow,
	}

	// Build tool definitions for the API
	toolDefs := make([]types.ToolParam, 0, len(allowedTools))
	for _, t := range allowedTools {
		toolDefs = append(toolDefs, types.ToolParam{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}

	// Run the sub-agent loop
	runner := query.NewRunner(queryDeps, model, deps.MaxTokens)
	events := make(chan query.Event, 512)
	go func() {
		for range events {
			// drain events silently
		}
	}()

	result := runner.Run(ctx, agent.SystemPrompt, messages, toolDefs, events)
	return result, nil
}

// ExtractLastAssistantText returns the text from the last assistant message, for use
// as the AgentTool result.
func ExtractLastAssistantText(messages []types.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			var b strings.Builder
			for _, block := range messages[i].Content {
				if block.Type == types.ContentBlockText {
					b.WriteString(block.Text)
				}
			}
			if b.Len() > 0 {
				return b.String()
			}
		}
	}
	return "(no response)"
}

func filterTools(reg *tool.Registry, allowedNames []string) []tool.Tool {
	if len(allowedNames) == 0 {
		// No restriction — use all tools
		return reg.GetAll()
	}

	allowed := make(map[string]bool, len(allowedNames))
	for _, name := range allowedNames {
		allowed[strings.TrimSpace(name)] = true
	}

	var out []tool.Tool
	for _, t := range reg.GetAll() {
		if allowed[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

func findTool(tools []tool.Tool, name string) tool.Tool {
	for _, t := range tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}
