package types

type ContentBlockType string

const (
	ContentBlockText       ContentBlockType = "text"
	ContentBlockToolUse    ContentBlockType = "tool_use"
	ContentBlockToolResult ContentBlockType = "tool_result"
	ContentBlockThinking   ContentBlockType = "thinking"
)

// Usage holds token consumption from an API response.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// Total returns input + output tokens.
func (u *Usage) Total() int {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.OutputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

type ContentBlock struct {
	Type ContentBlockType `json:"type"`

	// Text blocks
	Text string `json:"text,omitempty"`

	// Tool use blocks
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`

	// Tool result blocks
	Content string `json:"content,omitempty"`
	IsError bool   `json:"is_error,omitempty"`

	// Thinking blocks (DeepSeek outputs these for chain-of-thought)
	Thinking string `json:"thinking,omitempty"`
}

type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
	Usage   *Usage         `json:"usage,omitempty"`
}

type Messages []Message

func NewMessage(role string, content []ContentBlock) Message {
	return Message{Role: role, Content: content}
}
