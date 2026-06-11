package api

import (
	"context"
	"encoding/json"
	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// StreamEvent from the model API.
type StreamEvent struct {
	Type string
	Data interface{}
}

// Streamer yields stream events. Call Close when done.
type Streamer interface {
	Recv() (*StreamEvent, error)
	Close() error
}

// Client is the interface for calling language models.
type Client interface {
	StreamMessages(ctx context.Context, req *Request) (Streamer, error)
}

// Request is the API request body.
type Request struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system,omitempty"`
	Messages  []APIMessage        `json:"messages"`
	Tools     []types.ToolParam   `json:"tools,omitempty"`
	Stream    bool                `json:"stream"`
}

// APIMessage is the wire-format message.
type APIMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []ContentBlockWire
}

// ContentBlockWire is the wire-format content block.
type ContentBlockWire struct {
	Type      types.ContentBlockType `json:"type"`
	Text      string                 `json:"text,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     interface{}            `json:"input,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Content   interface{}            `json:"content,omitempty"`
	IsError   bool                   `json:"is_error,omitempty"`
	Thinking  string                 `json:"thinking,omitempty"`
}

// MarshalRequest converts a types.Message slice to API messages.
func MarshalRequest(msgs []types.Message, tools []types.ToolParam, system, model string, maxTokens int) *Request {
	apiMsgs := make([]APIMessage, len(msgs))
	for i, m := range msgs {
		apiMsgs[i] = marshalMessage(m)
	}
	return &Request{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  apiMsgs,
		Tools:     tools,
		Stream:    true,
	}
}

func marshalMessage(m types.Message) APIMessage {
	if len(m.Content) == 1 && m.Content[0].Type == types.ContentBlockText {
		b, _ := json.Marshal(m.Content[0].Text)
		return APIMessage{Role: m.Role, Content: json.RawMessage(b)}
	}
	blocks := make([]ContentBlockWire, len(m.Content))
	for j, b := range m.Content {
		blocks[j] = marshalBlock(b)
	}
	data, _ := json.Marshal(blocks)
	return APIMessage{Role: m.Role, Content: json.RawMessage(data)}
}

func marshalBlock(b types.ContentBlock) ContentBlockWire {
	out := ContentBlockWire{Type: b.Type}
	switch b.Type {
	case types.ContentBlockText:
		out.Text = b.Text
	case types.ContentBlockToolUse:
		out.ID = b.ToolUseID
		out.Name = b.Name
		out.Input = b.Input
	case types.ContentBlockToolResult:
		out.ToolUseID = b.ToolUseID
		out.IsError = b.IsError
		out.Content = b.Content
	case types.ContentBlockThinking:
		out.Thinking = b.Thinking
		if out.Thinking == "" {
			out.Thinking = " "
		}
	}
	return out
}
