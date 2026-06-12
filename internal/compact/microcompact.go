package compact

import (
	"math"
	"time"

	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// CompactedClearedMessage is the placeholder for micro-compacted tool results.
const CompactedClearedMessage = "[tool result cleared by time-based compaction]"

// MicrocompactConfig configures the time-based micro-compactor.
type MicrocompactConfig struct {
	// GapThresholdMinutes is the minimum time gap (in minutes) between
	// consecutive messages that triggers a micro-compaction.
	GapThresholdMinutes float64

	// KeepRecent is the minimum number of the most recent compactable
	// tool results to preserve (floor at 1).
	KeepRecent int
}

// DefaultMicrocompactConfig returns sensible defaults for micro-compaction.
func DefaultMicrocompactConfig() MicrocompactConfig {
	return MicrocompactConfig{
		GapThresholdMinutes: 30,
		KeepRecent:          1,
	}
}

// MaybeTimeBasedMicrocompact checks for a time-based trigger and, if fired,
// clears tool results from messages older than the detected gap, keeping the
// N most recent compactable tool IDs. Returns nil when no trigger fires or
// nothing can be saved.
func MaybeTimeBasedMicrocompact(msgs []types.Message, cfg MicrocompactConfig) *CompactResult {
	_, ok := evaluateTimeBasedTrigger(msgs, cfg)
	if !ok {
		return nil
	}

	compactableIDs := collectCompactableToolIDs(msgs)

	// Floor at 1: never clear ALL results
	keepRecent := cfg.KeepRecent
	if keepRecent < 1 {
		keepRecent = 1
	}

	keepSet := make(map[string]bool)
	// Keeping nothing degenerate — keep at least one
	if keepRecent > 0 && len(compactableIDs) > 0 {
		start := len(compactableIDs) - keepRecent
		if start < 0 {
			start = 0
		}
		for _, id := range compactableIDs[start:] {
			keepSet[id] = true
		}
	}

	clearSet := make([]string, 0, len(compactableIDs))
	for _, id := range compactableIDs {
		if !keepSet[id] {
			clearSet = append(clearSet, id)
		}
	}

	if len(clearSet) == 0 {
		return nil
	}

	var tokensSaved int
	result := make([]types.Message, len(msgs))
	copy(result, msgs)

	for i, msg := range result {
		if msg.Role != "user" || msg.Time.IsZero() {
			continue
		}
		modified := false
		newContent := make([]types.ContentBlock, len(msg.Content))
		copy(newContent, msg.Content)

		for j, block := range msg.Content {
			if block.Type != types.ContentBlockToolResult {
				continue
			}
			if !contains(clearSet, block.ToolUseID) {
				continue
			}
			if block.Content == CompactedClearedMessage {
				continue // already cleared
			}
			tokensSaved += EstimateToolResultTokens(block)
			newContent[j] = types.ContentBlock{
				Type:      types.ContentBlockToolResult,
				ToolUseID: block.ToolUseID,
				Content:   CompactedClearedMessage,
				IsError:   block.IsError,
			}
			modified = true
		}
		if modified {
			result[i] = types.Message{Role: msg.Role, Content: newContent, Time: msg.Time}
		}
	}

	if tokensSaved == 0 {
		return nil
	}

	return &CompactResult{
		Messages:          result,
		ClearedToolUseIDs: clearSet,
		TokensSaved:       tokensSaved,
	}
}

// evaluateTimeBasedTrigger scans messages for the largest time gap between
// consecutive messages. Returns the gap in minutes (rounded) and true when
// the gap exceeds the configured threshold.
func evaluateTimeBasedTrigger(msgs []types.Message, cfg MicrocompactConfig) (float64, bool) {
	if len(msgs) < 2 {
		return 0, false
	}

	var maxGap time.Duration
	for i := 1; i < len(msgs); i++ {
		t1, t2 := msgs[i-1].Time, msgs[i].Time
		if t1.IsZero() || t2.IsZero() {
			continue
		}
		gap := t2.Sub(t1)
		if gap > maxGap {
			maxGap = gap
		}
	}

	if maxGap <= 0 {
		return 0, false
	}

	gapMinutes := maxGap.Minutes()
	if gapMinutes < cfg.GapThresholdMinutes {
		return 0, false
	}

	return math.Round(gapMinutes), true
}

// collectCompactableToolIDs returns the tool_use_ids from all tool_result
// blocks in the message list, in order of appearance (oldest first).
func collectCompactableToolIDs(msgs []types.Message) []string {
	var ids []string
	seen := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role != "user" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type != types.ContentBlockToolResult {
				continue
			}
			if block.ToolUseID == "" || seen[block.ToolUseID] {
				continue
			}
			// Skip already-cleared results
			if block.Content == CompactedClearedMessage {
				continue
			}
			seen[block.ToolUseID] = true
			ids = append(ids, block.ToolUseID)
		}
	}
	return ids
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}