package token

import "github.com/YYYSSSRRR/codepilot/pkg/types"

// Counter provides token counting and threshold computation
// using exact API-reported usage where available and local estimation for tail messages.
type Counter struct {
	model         string
	contextWindow int
	maxOutput     int
}

// NewCounter creates a counter for the given model and context limits.
func NewCounter(model string, contextWindow, maxOutput int) *Counter {
	return &Counter{
		model:         model,
		contextWindow: contextWindow,
		maxOutput:     maxOutput,
	}
}

// TokenCount returns the total token count for the message list.
// For messages with Usage set (assistant messages from API), it takes the
// exact API-reported total (input + output + cache). For subsequent messages,
// it falls back to local rough estimation (~chars/4).
func (c *Counter) TokenCount(messages []types.Message) int {
	// Find the last assistant message with API-reported usage
	lastExactIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && messages[i].Usage != nil {
			lastExactIdx = i
			break
		}
	}

	if lastExactIdx < 0 {
		// No API usage data yet — estimate everything
		return c.estimateAll(messages)
	}

	// Exact tokens up to and including the last message with usage
	exact := messages[lastExactIdx].Usage.Total()

	// Estimate tokens for messages after the last usage point
	tail := messages[lastExactIdx+1:]
	if len(tail) == 0 {
		return exact
	}

	return exact + c.estimateAll(tail)
}

// AutoCompactThreshold returns the token count threshold above which
// automatic compaction should trigger: effectiveWindow - buffer.
func (c *Counter) AutoCompactThreshold() int {
	effectiveWindow := c.contextWindow - min(c.maxOutput, 20000)
	buffer := bufferForWindow(c.contextWindow)
	return effectiveWindow - buffer
}

// HardLimit returns the hard limit above which the prompt is too long:
// effectiveWindow - 3000.
func (c *Counter) HardLimit() int {
	effectiveWindow := c.contextWindow - min(c.maxOutput, 20000)
	return effectiveWindow - 3000
}

// EffectiveWindow returns the usable context window (total minus output reservation).
func (c *Counter) EffectiveWindow() int {
	return c.contextWindow - min(c.maxOutput, 20000)
}

// ShouldAutoCompact reports whether the message list exceeds the auto-compact threshold.
func (c *Counter) ShouldAutoCompact(messages []types.Message) bool {
	return c.TokenCount(messages) >= c.AutoCompactThreshold()
}

// IsOverLimit reports whether the message list exceeds the hard limit.
func (c *Counter) IsOverLimit(messages []types.Message) bool {
	return c.TokenCount(messages) >= c.HardLimit()
}

// ---------------------------------------------------------------------------
// Estimation helpers
// ---------------------------------------------------------------------------

// estimateAll computes a total token estimate for all messages using local heuristics.
func (c *Counter) estimateAll(messages []types.Message) int {
	var total int
	for _, msg := range messages {
		total += EstimateMessageTokens(msg)
	}
	return total
}

// EstimateMessageTokens returns a rough token count for a single message
// using char/4 heuristic plus per-block overhead.
func EstimateMessageTokens(msg types.Message) int {
	var n int
	for _, block := range msg.Content {
		switch block.Type {
		case types.ContentBlockText:
			n += len(block.Text) / 4
		case types.ContentBlockToolUse:
			n += len(block.ToolUseID) / 4
			n += len(block.Name) / 4
			if block.Input != nil {
				n += 50 // rough estimate for JSON object overhead
			}
		case types.ContentBlockToolResult:
			n += len(block.Content) / 4
		case types.ContentBlockThinking:
			n += len(block.Thinking) / 4
		}
		n += 4 // per-block overhead
	}
	return n + 8 // per-message overhead
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// bufferForWindow returns the safety buffer based on context window size.
func bufferForWindow(w int) int {
	switch {
	case w >= 800_000:
		return 50_000
	case w >= 400_000:
		return 30_000
	default:
		return 13_000
	}
}
