package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/YYYSSSRRR/codepilot/internal/api"
	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// ToolUseInfo records one tool_use from the assistant.
type ToolUseInfo struct {
	Index int
	ID    string
	Name  string
	Input map[string]any
}

// QueryDeps holds injected dependencies for the ReAct loop.
type QueryDeps struct {
	CallModel   func(ctx context.Context, req *api.Request) (api.Streamer, error)
	CanUseTool  func(ctx context.Context, toolName string, input map[string]any, toolUseID string) (bool, string)
	ExecuteTool func(ctx context.Context, toolName string, input map[string]any) (string, bool, error)
	Compact     func(messages []types.Message) []types.Message
	// RecordTranscript persists the current message list after each turn.
	// Called fire-and-forget from the streaming goroutine.
	RecordTranscript func(messages []types.Message)

	// TokenCount returns the total token count for the message list.
	// Uses exact API usage + local estimation for tail messages.
	TokenCount func(messages []types.Message) int

	// AutoCompactThreshold returns the token count at which compaction triggers.
	AutoCompactThreshold func() int

	// EffectiveWindow returns the usable context window (total minus output reservation).
	EffectiveWindow func() int
}

// Runner drives the ReAct loop. Create via NewRunner.
type Runner struct {
	deps      QueryDeps
	system    string
	model     string
	maxTokens int
}

func NewRunner(deps QueryDeps, system, model string, maxTokens int) *Runner {
	return &Runner{
		deps:      deps,
		system:    system,
		model:     model,
		maxTokens: maxTokens,
	}
}

// Run starts the ReAct loop. It blocks until the conversation turn is done.
// Returns the final message list (including assistant responses and tool results).
func (r *Runner) Run(ctx context.Context, messages []types.Message, tools []types.ToolParam, events chan<- Event) []types.Message {
	defer close(events)

	turnMsgs := messages

	for turn := 0; ; turn++ {
		if err := ctx.Err(); err != nil {
			events <- Event{Type: EventTurnComplete, StopReason: "user_interrupt"}
			events <- Event{Type: EventQueryDone}
			return turnMsgs
		}

		// ── Token budget checks ──────────────────────────────────────
		if r.deps.TokenCount != nil && r.deps.AutoCompactThreshold != nil {
			total := r.deps.TokenCount(turnMsgs)
			threshold := r.deps.AutoCompactThreshold()
			var hardLimit int
			if r.deps.EffectiveWindow != nil {
				hardLimit = r.deps.EffectiveWindow() - 3000
			}

			// Hard limit: prompt too long
			if hardLimit > 0 && total >= hardLimit {
				events <- Event{
					Type: EventError,
					Err:  fmt.Errorf("prompt too long: ~%d tokens (limit %d)", total, hardLimit),
				}
				events <- Event{Type: EventQueryDone}
				return turnMsgs
			}

			// Auto-compact threshold
			if total >= threshold && r.deps.Compact != nil {
				turnMsgs = r.deps.Compact(turnMsgs)
			}
		} else if r.deps.Compact != nil {
			// Fallback: compact unconditionally if no token info available
			turnMsgs = r.deps.Compact(turnMsgs)
		}

		// Process a single model turn — streams response and collects tool_uses
		executor := NewStreamingToolExecutor(r.deps)
		turnResult := r.processTurn(ctx, turnMsgs, tools, executor, events)

		if turnResult.Err != nil {
			if errors.Is(turnResult.Err, context.Canceled) {
				events <- Event{Type: EventTurnComplete, StopReason: "user_interrupt"}
				events <- Event{Type: EventQueryDone}
				return turnMsgs
			}
			events <- Event{Type: EventError, Err: turnResult.Err}
			events <- Event{Type: EventQueryDone}
			return turnMsgs
		}

		events <- Event{
			Type:       EventTurnComplete,
			StopReason: turnResult.StopReason,
			Usage:      turnResult.AssistantMsg.Usage,
		}

		// No tool uses — model response is final
		if len(turnResult.ToolUses) == 0 {
			if r.deps.RecordTranscript != nil {
				// Include the final assistant message for transcript completeness
				complete := turnMsgs
				if turnResult.AssistantMsg.Role != "" {
					complete = append(complete, turnResult.AssistantMsg)
				}
				r.deps.RecordTranscript(complete)
			}
			events <- Event{Type: EventQueryDone}
			return turnMsgs
		}

		// Append assistant message to history
		if turnResult.AssistantMsg.Role != "" {
			turnMsgs = append(turnMsgs, turnResult.AssistantMsg)
		}

		// Execute all tools synchronously (one at a time) and emit results
		allUpdates := executor.ExecuteAll(ctx)
		for _, update := range allUpdates {
			events <- Event{
				Type:    EventToolExecResult,
				ToolID:  update.ToolUseID,
				Result:  update.Content,
				IsError: update.IsError,
			}
		}

		// Check for interruption during tool execution
		if err := ctx.Err(); err != nil {
			events <- Event{Type: EventTurnComplete, StopReason: "user_interrupt"}
			events <- Event{Type: EventQueryDone}
			return turnMsgs
		}

		// Append tool results as a user message and loop back
		turnMsgs = append(turnMsgs, types.NewMessage("user", UpdatesToBlocks(allUpdates)))

		// Persist after each complete turn (assistant response + tool results)
		if r.deps.RecordTranscript != nil {
			r.deps.RecordTranscript(turnMsgs)
		}
	}
}

// ---------------------------------------------------------------------------
// Turn — per-stream state
// ---------------------------------------------------------------------------

type turn struct {
	ctx       context.Context
	events    chan<- Event
	res       *TurnResultExt
	accBlocks []accBlock
	toolUses  []ToolUseInfo
	usage     types.Usage // accumulated across streaming events
}

type accBlock struct {
	block  types.ContentBlock
	jsonSB strings.Builder
}

func newTurn(ctx context.Context, events chan<- Event) *turn {
	return &turn{
		ctx:    ctx,
		events: events,
		res:    &TurnResultExt{},
	}
}

// TurnResult holds the assistant message and tool usage info from one API turn.
type TurnResult struct {
	AssistantMsg types.Message
	ToolUses     []ToolUseInfo
	StopReason   string
	Usage        types.Usage
}

// TurnResultExt extends TurnResult with errors.
type TurnResultExt struct {
	TurnResult
	Err error
}

func (r *Runner) processTurn(ctx context.Context, msgs []types.Message, tools []types.ToolParam, executor *StreamingToolExecutor, events chan<- Event) *TurnResultExt {
	t := newTurn(ctx, events)

	req := api.MarshalRequest(msgs, tools, r.system, r.model, r.maxTokens)
	stream, err := r.deps.CallModel(ctx, req)
	if err != nil {
		t.res.Err = fmt.Errorf("API call: %w", err)
		return t.res
	}
	defer stream.Close()

	// Stream close on cancel to unblock Recv
	go func() {
		<-ctx.Done()
		stream.Close()
	}()

	for {
		sseEvent, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				t.res.Err = ctx.Err()
				return t.res
			}
			t.res.Err = fmt.Errorf("stream recv: %w", err)
			return t.res
		}
		if err := ctx.Err(); err != nil {
			t.res.Err = err
			return t.res
		}

		switch sseEvent.Type {
		case "message_start":
			t.handleMessageStart(sseEvent.Data)

		case "content_block_start":
			t.handleBlockStart(sseEvent.Data)

		case "content_block_delta":
			t.handleBlockDelta(sseEvent.Data)

		case "content_block_stop":
			t.handleBlockStop(sseEvent.Data, executor)

		case "message_delta":
			t.handleMessageDelta(sseEvent.Data)

		case "message_stop":
			// All content blocks received

		case "error":
			t.handleAPIError(sseEvent.Data)
			return t.res

		case "ping":
			// heartbeat
		}

		if t.res.Err != nil {
			return t.res
		}
	}

	// Build assistant message from accumulated blocks
	blocks := make([]types.ContentBlock, 0, len(t.accBlocks))
	for _, ab := range t.accBlocks {
		blocks = append(blocks, ab.block)
	}
	t.res.AssistantMsg = types.NewMessage("assistant", blocks)
	t.res.AssistantMsg.Usage = &t.usage
	t.res.ToolUses = t.toolUses

	return t.res
}

// ---------------------------------------------------------------------------
// SSE event handlers
// ---------------------------------------------------------------------------

func (t *turn) handleMessageStart(data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		return
	}
	var msgData struct {
		Message struct {
			Usage types.Usage `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &msgData); err != nil {
		t.res.Err = fmt.Errorf("parse message_start: %w", err)
		return
	}
	t.usage.InputTokens = msgData.Message.Usage.InputTokens
	t.usage.CacheCreationInputTokens = msgData.Message.Usage.CacheCreationInputTokens
	t.usage.CacheReadInputTokens = msgData.Message.Usage.CacheReadInputTokens
}

func (t *turn) handleBlockStart(data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		return
	}
	var startData struct {
		Index        int             `json:"index"`
		ContentBlock json.RawMessage `json:"content_block"`
	}
	if err := json.Unmarshal(raw, &startData); err != nil {
		t.res.Err = fmt.Errorf("parse content_block_start: %w", err)
		return
	}

	var block types.ContentBlock
	if err := parseBlockStart(&block, startData.ContentBlock); err != nil {
		t.res.Err = fmt.Errorf("parse block: %w", err)
		return
	}

	for len(t.accBlocks) <= startData.Index {
		t.accBlocks = append(t.accBlocks, accBlock{})
	}
	t.accBlocks[startData.Index] = accBlock{block: block}

	if block.Type == types.ContentBlockToolUse {
		t.events <- Event{
			Type:     EventToolUseStart,
			ToolID:   block.ToolUseID,
			ToolName: block.Name,
		}
	}
}

func (t *turn) handleBlockDelta(data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		return
	}
	var deltaData struct {
		Index int `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(raw, &deltaData); err != nil {
		t.res.Err = fmt.Errorf("parse delta: %w", err)
		return
	}

	idx := deltaData.Index
	for len(t.accBlocks) <= idx {
		t.accBlocks = append(t.accBlocks, accBlock{})
	}

	switch deltaData.Delta.Type {
	case "text_delta":
		t.accBlocks[idx].block.Type = types.ContentBlockText
		t.accBlocks[idx].block.Text += deltaData.Delta.Text
		t.events <- Event{Type: EventTextChunk, Text: deltaData.Delta.Text}

	case "thinking_delta":
		t.accBlocks[idx].block.Type = types.ContentBlockThinking
		t.accBlocks[idx].block.Thinking += deltaData.Delta.Text

	case "input_json_delta":
		t.accBlocks[idx].jsonSB.WriteString(deltaData.Delta.PartialJSON)
		t.events <- Event{
			Type:       EventToolUseInput,
			ToolID:     t.accBlocks[idx].block.ToolUseID,
			InputDelta: deltaData.Delta.PartialJSON,
		}
	}
}

func (t *turn) handleBlockStop(data any, executor *StreamingToolExecutor) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		return
	}
	var stopData struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal(raw, &stopData); err != nil {
		t.res.Err = fmt.Errorf("parse content_block_stop: %w", err)
		return
	}

	idx := stopData.Index
	if idx >= len(t.accBlocks) {
		return
	}

	ab := &t.accBlocks[idx]

	// Finalize tool_use input from accumulated JSON deltas
	if ab.block.Type == types.ContentBlockToolUse && ab.jsonSB.Len() > 0 {
		var input map[string]any
		if err := json.Unmarshal([]byte(ab.jsonSB.String()), &input); err == nil {
			ab.block.Input = input
		}
	}

	if ab.block.Type != types.ContentBlockToolUse {
		return
	}

	tu := ToolUseInfo{
		Index: idx,
		ID:    ab.block.ToolUseID,
		Name:  ab.block.Name,
		Input: ab.block.Input,
	}

	t.events <- Event{
		Type:     EventToolUseDone,
		ToolID:   tu.ID,
		ToolName: tu.Name,
		Input:    tu.Input,
	}

	t.toolUses = append(t.toolUses, tu)

	// Queue tool for concurrent execution
	executor.AddTool(tu)
}

func (t *turn) handleMessageDelta(data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		return
	}
	var deltaMsg struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &deltaMsg); err != nil {
		t.res.Err = fmt.Errorf("parse message_delta: %w", err)
		return
	}
	t.res.StopReason = deltaMsg.Delta.StopReason
	t.usage.OutputTokens = deltaMsg.Usage.OutputTokens
}

func (t *turn) handleAPIError(data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		return
	}
	var errData struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &errData); err == nil {
		t.res.Err = fmt.Errorf("API error [%s]: %s", errData.Error.Type, errData.Error.Message)
		return
	}
	t.res.Err = fmt.Errorf("API error: %s", string(raw))
}

// ---------------------------------------------------------------------------
// Content block parsing
// ---------------------------------------------------------------------------

func parseBlockStart(b *types.ContentBlock, raw json.RawMessage) error {
	var wire struct {
		Type     types.ContentBlockType `json:"type"`
		Text     string                 `json:"text"`
		ID       string                 `json:"id"`
		Name     string                 `json:"name"`
		Input    any                    `json:"input"`
		Thinking string                 `json:"thinking"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fmt.Errorf("parse content block: %w", err)
	}
	b.Type = wire.Type
	switch wire.Type {
	case types.ContentBlockText:
		b.Text = wire.Text
	case types.ContentBlockToolUse:
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
	case types.ContentBlockThinking:
		b.Thinking = wire.Thinking
	}
	return nil
}
