package compact

import (
	"strings"

	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// ContentReplacementState tracks tool results whose content has been
// replaced with a placeholder during budget compaction.
type ContentReplacementState struct {
	Replacements map[string]string // tool_use_id → original content
}

// CompactResult carries the output of a compaction operation.
type CompactResult struct {
	Messages          []types.Message
	ClearedToolUseIDs []string
	TokensSaved       int
}

// ToolUseContext holds per-turn state for tool operations and compaction.
type ToolUseContext struct {
	ContentReplacement *ContentReplacementState
	UnlimitedTools     map[string]bool // tool names exempt from result budget
}

// ---------------------------------------------------------------------------
// Compact boundary
// ---------------------------------------------------------------------------

// FindLastCompactBoundaryIndex scans messages backward for the most recent
// message with CompactBoundary set. Returns -1 if none found.
func FindLastCompactBoundaryIndex(msgs []types.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].CompactBoundary {
			return i
		}
	}
	return -1
}

// GetMessagesAfterCompactBoundary returns the slice of messages that follow
// the last compact boundary. If no boundary exists, all messages are returned.
func GetMessagesAfterCompactBoundary(msgs []types.Message) []types.Message {
	idx := FindLastCompactBoundaryIndex(msgs)
	if idx < 0 {
		return msgs
	}
	return msgs[idx+1:]
}

// ---------------------------------------------------------------------------
// Tool result budget
// ---------------------------------------------------------------------------

const (
	// DefaultToolResultBudget is the default max chars for a tool result
	// before it gets replaced during budget compaction.
	DefaultToolResultBudget = 20_000

	// CompactedPlaceholder is the replacement text for compacted tool results.
	CompactedPlaceholder = "[tool result content removed by compaction]"
)

// EstimateToolResultTokens returns a rough token estimate for a tool result block.
func EstimateToolResultTokens(block types.ContentBlock) int {
	if block.Type != types.ContentBlockToolResult {
		return 0
	}
	return len(block.Content)/4 + 4 // chars/4 + block overhead
}

// ApplyToolResultBudget replaces tool results whose content exceeds budget
// with a placeholder. Original content is saved in state. Tools listed in
// unlimitedTools are always kept at full size.
// Returns the modified messages and estimated tokens saved.
func ApplyToolResultBudget(msgs []types.Message, state *ContentReplacementState, unlimitedTools map[string]bool, budget int) ([]types.Message, int) {
	if state == nil {
		return msgs, 0
	}
	if state.Replacements == nil {
		state.Replacements = make(map[string]string)
	}

	var saved int
	result := make([]types.Message, len(msgs))
	copy(result, msgs)

	for i, msg := range result {
		if msg.Role != "user" {
			continue
		}
		modified := false
		newContent := make([]types.ContentBlock, len(msg.Content))
		copy(newContent, msg.Content)

		for j, block := range msg.Content {
			if block.Type != types.ContentBlockToolResult {
				continue
			}
			// Skip tools with no max result limit
			toolName := findToolNameForResult(result, block.ToolUseID)
			if toolName != "" && unlimitedTools[toolName] {
				continue
			}
			// Skip already-replaced blocks
			if strings.HasPrefix(block.Content, "[") && strings.HasSuffix(block.Content, "]") {
				if state.Replacements[block.ToolUseID] != "" {
					continue
				}
			}
			if len(block.Content) > budget {
				if _, exists := state.Replacements[block.ToolUseID]; !exists {
					state.Replacements[block.ToolUseID] = block.Content
				}
				saved += EstimateToolResultTokens(block)
				newContent[j] = types.ContentBlock{
					Type:      types.ContentBlockToolResult,
					ToolUseID: block.ToolUseID,
					Content:   CompactedPlaceholder,
					IsError:   block.IsError,
				}
				modified = true
			}
		}
		if modified {
			result[i] = types.Message{Role: msg.Role, Content: newContent, Time: msg.Time}
		}
	}

	return result, saved
}

// findToolNameForResult scans messages for a tool_use block matching the
// given tool_use_id and returns its tool name.
func findToolNameForResult(msgs []types.Message, toolUseID string) string {
	for _, msg := range msgs {
		for _, block := range msg.Content {
			if block.Type == types.ContentBlockToolUse && block.ToolUseID == toolUseID {
				return block.Name
			}
		}
	}
	return ""
}