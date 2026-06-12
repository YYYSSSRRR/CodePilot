package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/YYYSSSRRR/codepilot/internal/api"
	"github.com/YYYSSSRRR/codepilot/internal/compact"
	memoryutils "github.com/YYYSSSRRR/codepilot/internal/utils/memory"
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
	CompactSystem     *compact.ToolUseContext
	Microcompact      func(messages []types.Message) *compact.CompactResult
	AutoCompact       func(ctx context.Context, messages []types.Message) ([]types.Message, error)
	RecordTranscript func(messages []types.Message)

	TokenCount           func(messages []types.Message) int
	AutoCompactThreshold func() int
	EffectiveWindow      func() int

	// MemoryPrefetch config for async memory retrieval. If nil, prefetch is skipped.
	MemoryPrefetch *memoryutils.PrefetchConfig
}

// Runner drives the ReAct loop. Create via NewRunner.
type Runner struct {
	deps      QueryDeps
	model     string
	maxTokens int
}

func NewRunner(deps QueryDeps, model string, maxTokens int) *Runner {
	return &Runner{
		deps:      deps,
		model:     model,
		maxTokens: maxTokens,
	}
}

// Run starts the ReAct loop. It blocks until the conversation turn is done.
func (r *Runner) Run(ctx context.Context, system string, messages []types.Message, tools []types.ToolParam, events chan<- Event) []types.Message {
	defer close(events)

	// ── Async memory prefetch ──────────────────────────────────────────
	var memoryCh <-chan []types.Message
	var memoryConsumed bool
	if r.deps.MemoryPrefetch != nil {
		if q := lastUserQuery(messages); q != "" {
			memoryCh = memoryutils.Prefetch(ctx, *r.deps.MemoryPrefetch, q)
		}
	}

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

			if hardLimit > 0 && total >= hardLimit {
				events <- Event{
					Type: EventError,
					Err:  fmt.Errorf("prompt too long: ~%d tokens (limit %d)", total, hardLimit),
				}
				events <- Event{Type: EventQueryDone}
				return turnMsgs
			}

			if total >= threshold {
				// Layer 1: Tool result budget on active window
				if r.deps.CompactSystem != nil {
					boundIdx := compact.FindLastCompactBoundaryIndex(turnMsgs)
					if boundIdx < 0 {
						modified, _ := compact.ApplyToolResultBudget(turnMsgs,
							r.deps.CompactSystem.ContentReplacement,
							r.deps.CompactSystem.UnlimitedTools,
							compact.DefaultToolResultBudget)
						turnMsgs = modified
					} else {
						activeWindow := turnMsgs[boundIdx+1:]
						modified, _ := compact.ApplyToolResultBudget(activeWindow,
							r.deps.CompactSystem.ContentReplacement,
							r.deps.CompactSystem.UnlimitedTools,
							compact.DefaultToolResultBudget)
						result := make([]types.Message, 0, boundIdx+1+len(modified))
						result = append(result, turnMsgs[:boundIdx+1]...)
						result = append(result, modified...)
						turnMsgs = result
					}
				}

				// Layer 2: Microcompact (time-based)
				if r.deps.Microcompact != nil {
					mcResult := r.deps.Microcompact(turnMsgs)
					if mcResult != nil {
						turnMsgs = mcResult.Messages
					}
				}

				// Layer 3: AutoCompact if still over threshold
				if r.deps.AutoCompact != nil {
					if r.deps.TokenCount(turnMsgs) >= threshold {
						var acErr error
						turnMsgs, acErr = r.deps.AutoCompact(ctx, turnMsgs)
						if acErr != nil {
							events <- Event{Type: EventError, Err: fmt.Errorf("auto-compact: %w", acErr)}
						}
					}
				}
			}
		}

		// ── API call ───────────────────────────────────────────────────
		executor := NewStreamingToolExecutor(r.deps)
		turnResult := r.processTurn(ctx, system, turnMsgs, tools, executor, events)

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

		// ── Inject prefetched memory ─────────────────────────────────
		if !memoryConsumed && memoryCh != nil {
			select {
			case memMsgs, ok := <-memoryCh:
				if ok && len(memMsgs) > 0 {
					turnMsgs = append(turnMsgs, memMsgs...)
				}
				memoryConsumed = true
			default:
				// Not ready yet — will try again next turn
			}
		}

		// ── No tool uses → done ───────────────────────────────────────
		if len(turnResult.ToolUses) == 0 {
			if r.deps.RecordTranscript != nil {
				complete := turnMsgs
				if turnResult.AssistantMsg.Role != "" {
					complete = append(complete, turnResult.AssistantMsg)
				}
				r.deps.RecordTranscript(complete)
			}
			events <- Event{Type: EventQueryDone}
			return turnMsgs
		}

		if turnResult.AssistantMsg.Role != "" {
			turnMsgs = append(turnMsgs, turnResult.AssistantMsg)
		}

		// ── Execute tools ──────────────────────────────────────────────
		allUpdates := executor.ExecuteAll(ctx)
		for _, update := range allUpdates {
			events <- Event{
				Type:    EventToolExecResult,
				ToolID:  update.ToolUseID,
				Result:  update.Content,
				IsError: update.IsError,
			}
		}

		if err := ctx.Err(); err != nil {
			events <- Event{Type: EventTurnComplete, StopReason: "user_interrupt"}
			events <- Event{Type: EventQueryDone}
			return turnMsgs
		}

		turnMsgs = append(turnMsgs, types.NewMessage("user", UpdatesToBlocks(allUpdates)))

		if r.deps.RecordTranscript != nil {
			r.deps.RecordTranscript(turnMsgs)
		}
	}
}

// lastUserQuery extracts the text content of the most recent user message.
func lastUserQuery(messages []types.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
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
	return ""
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
	usage     types.Usage
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

type TurnResult struct {
	AssistantMsg types.Message
	ToolUses     []ToolUseInfo
	StopReason   string
	Usage        types.Usage
}

type TurnResultExt struct {
	TurnResult
	Err error
}

func (r *Runner) processTurn(ctx context.Context, system string, msgs []types.Message, tools []types.ToolParam, executor *StreamingToolExecutor, events chan<- Event) *TurnResultExt {
	t := newTurn(ctx, events)

	req := api.MarshalRequest(msgs, tools, system, r.model, r.maxTokens)
	stream, err := r.deps.CallModel(ctx, req)
	if err != nil {
		t.res.Err = fmt.Errorf("API call: %w", err)
		return t.res
	}
	defer stream.Close()

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
		case "error":
			t.handleAPIError(sseEvent.Data)
			return t.res
		case "ping":
		}

		if t.res.Err != nil {
			return t.res
		}
	}

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
