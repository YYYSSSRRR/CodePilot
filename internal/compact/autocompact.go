package compact

import (
	"context"
	"fmt"
	"strings"

	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

const (
	// AutoCompactKeepMessages is the number of most recent messages to
	// preserve verbatim during auto-compaction.
	AutoCompactKeepMessages = 10
)

// BuildCompactPrompt constructs a prompt for the LLM to summarize the
// given messages into a concise summary.
func BuildCompactPrompt(messages []types.Message) string {
	var b strings.Builder
	b.WriteString("You are a conversation summarizer. Summarize the following conversation turns " +
		"into a single concise paragraph that captures the key context needed to continue working. " +
		"Include: the task goal, what has been done so far, key decisions, and current state. " +
		"Omit: tool output details, error messages, and verbose file contents.\n\n")

	b.WriteString("## Conversation History\n\n")
	for _, msg := range messages {
		role := msg.Role
		b.WriteString(fmt.Sprintf("=== %s ===\n", role))
		for _, block := range msg.Content {
			switch block.Type {
			case types.ContentBlockText:
				b.WriteString(block.Text)
				b.WriteString("\n")
			case types.ContentBlockToolUse:
				b.WriteString(fmt.Sprintf("[Tool: %s]\n", block.Name))
			case types.ContentBlockToolResult:
				// Skip verbose tool results
				b.WriteString("[Tool Result]\n")
			case types.ContentBlockThinking:
				b.WriteString("[Thinking]\n")
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("\n---\nProvide ONLY the summary paragraph, no preamble or explanation.")
	return b.String()
}

// AutoCompact summarizes all messages except the last AutoCompactKeepMessages
// using the provided LLM call, then replaces them with a compacted system
// message and marks the compact boundary.
//
// callLLM should send the prompt to a model and return the response text.
func AutoCompact(ctx context.Context, msgs []types.Message, callLLM func(ctx context.Context, prompt string) (string, error)) ([]types.Message, error) {
	if len(msgs) <= AutoCompactKeepMessages {
		// Not enough messages to compact
		return msgs, nil
	}

	// Find the split: keep the last N messages verbatim
	splitIdx := len(msgs) - AutoCompactKeepMessages

	// Summarize everything before the split
	toCompact := msgs[:splitIdx]
	prompt := BuildCompactPrompt(toCompact)

	summary, err := callLLM(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("auto-compact LLM call: %w", err)
	}

	summary = strings.TrimSpace(summary)

	// Build result: compact summary + recent messages
	result := make([]types.Message, 0, AutoCompactKeepMessages+1)

	// Compact boundary marker (system role message with summary)
	result = append(result, types.Message{
		Role: "system",
		Content: []types.ContentBlock{
			{Type: types.ContentBlockText, Text: summary},
		},
		CompactBoundary: true,
		Time:            msgs[splitIdx].Time,
	})

	// Append the recent messages, preserving their timestamps
	for _, msg := range msgs[splitIdx:] {
		m := types.Message{
			Role:    msg.Role,
			Content: msg.Content,
			Usage:   msg.Usage,
			Time:    msg.Time,
		}
		result = append(result, m)
	}

	return result, nil
}
