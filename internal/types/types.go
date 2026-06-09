package types

import (
	"encoding/json"
	"fmt"
)

type ContentBlockType string

const (
	ContentBlockText       ContentBlockType = "text"
	ContentBlockToolUse    ContentBlockType = "tool_use"
	ContentBlockToolResult ContentBlockType = "tool_result"
	ContentBlockThinking   ContentBlockType = "thinking"
)

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
}

// Messages is a named slice for attaching serialization methods.
type Messages []Message

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

// NewMessage is the single constructor for this package.
func NewMessage(role string, content []ContentBlock) Message {
	return Message{Role: role, Content: content}
}

// ---------------------------------------------------------------------------
// Message serialization
// ---------------------------------------------------------------------------

// ToAPI converts a Messages slice to the API wire format.
func (msgs Messages) ToAPI() []APIMessage {
	out := make([]APIMessage, len(msgs))
	for i, m := range msgs {
		out[i] = m.toAPI()
	}
	return out
}

func (m Message) toAPI() APIMessage {
	if len(m.Content) == 1 && m.Content[0].Type == ContentBlockText {
		return APIMessage{Role: m.Role, Content: m.mustJSON(m.Content[0].Text)}
	}
	blocks := make([]contentBlockWire, len(m.Content))
	for j, b := range m.Content {
		blocks[j] = b.toAPI()
	}
	return APIMessage{Role: m.Role, Content: m.mustJSON(blocks)}
}

func (m Message) mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("types: json marshal: " + err.Error())
	}
	return b
}

// ---------------------------------------------------------------------------
// ContentBlock serialization
// ---------------------------------------------------------------------------

func (b ContentBlock) toAPI() contentBlockWire {
	out := contentBlockWire{Type: b.Type}
	switch b.Type {
	case ContentBlockText:
		out.Text = b.Text
	case ContentBlockToolUse:
		out.ID = b.ToolUseID
		out.Name = b.Name
		out.Input = b.Input
	case ContentBlockToolResult:
		out.ToolUseID = b.ToolUseID
		out.IsError = b.IsError
		out.Content = b.Content
	case ContentBlockThinking:
		out.Thinking = b.Thinking
		if out.Thinking == "" {
			out.Thinking = " "
		}
	}
	return out
}

// ParseStart deserializes a content_block_start SSE payload into the receiver.
func (b *ContentBlock) ParseStart(raw json.RawMessage) error {
	var wire struct {
		Type     ContentBlockType `json:"type"`
		Text     string           `json:"text"`
		ID       string           `json:"id"`
		Name     string           `json:"name"`
		Input    any              `json:"input"`
		Thinking string           `json:"thinking"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fmt.Errorf("parse content block: %w", err)
	}
	b.Type = wire.Type
	switch wire.Type {
	case ContentBlockText:
		b.Text = wire.Text
	case ContentBlockToolUse:
		b.ToolUseID = wire.ID
		b.Name = wire.Name
		if wire.Input != nil {
			if m, ok := wire.Input.(map[string]any); ok {
				b.Input = m
			}
		}
		if b.Input == nil {
			b.Input = make(map[string]any)
		}
	case ContentBlockThinking:
		b.Thinking = wire.Thinking
	}
	return nil
}

// ---------------------------------------------------------------------------
// API wire format
// ---------------------------------------------------------------------------

type APIRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system,omitempty"`
	Messages  []APIMessage   `json:"messages"`
	Tools     []APIToolParam `json:"tools,omitempty"`
	Stream    bool           `json:"stream"`
}

type APIMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []contentBlockWire
}

type contentBlockWire struct {
	Type      ContentBlockType `json:"type"`
	Text      string           `json:"text,omitempty"`
	ID        string           `json:"id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Input     any              `json:"input,omitempty"`
	ToolUseID string           `json:"tool_use_id,omitempty"`
	Content   any              `json:"content,omitempty"`
	IsError   bool             `json:"is_error,omitempty"`
	Thinking  string           `json:"thinking,omitempty"`
}

type APIToolParam struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}