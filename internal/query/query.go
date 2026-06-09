package query

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/YYYSSSRRR/codepilot/internal/apiclient"
	"github.com/YYYSSSRRR/codepilot/internal/tool"
	"github.com/YYYSSSRRR/codepilot/internal/types"
)

// ToolUseInfo records one tool_use from the assistant.
type ToolUseInfo struct {
	Index int
	ID    string
	Name  string
	Input map[string]any
}

// ExecuteToolFunc is provided by the engine to execute a tool with permission checks.
type ExecuteToolFunc func(ctx context.Context, toolName string, input map[string]any, toolUseID string) (string, bool, error)

// Params for the ReAct loop.
type Params struct {
	Messages     []MessageState
	Tools        *tool.Registry
	SystemPrompt string
	APIKey       string
	Model        string
	BaseURL      string
	ExecuteTool  ExecuteToolFunc
}

// MessageState wraps a message with additional turn metadata.
type MessageState struct {
	Message       types.Message
	TurnCompleted bool
}

// TurnResult holds the assistant message and tool usage info from one API turn.
type TurnResult struct {
	AssistantMsg types.Message
	ToolUses     []ToolUseInfo
	StopReason   string
	Usage        struct {
		InputTokens  int
		OutputTokens int
	}
}

// TurnResultExt extends TurnResult with execution results and errors.
type TurnResultExt struct {
	TurnResult
	ToolResults []types.ContentBlock
	Err         error
}

// toolResultItem pairs a result with its SSE block index for ordering.
type toolResultItem struct {
	Index int
	Block types.ContentBlock
}

// accBlock accumulates one assistant content block from SSE events.
type accBlock struct {
	block  types.ContentBlock
	jsonSB strings.Builder
}

// ---------------------------------------------------------------------------
// Runner — ReAct loop
// ---------------------------------------------------------------------------

// Runner drives the ReAct loop. Create via NewRunner.
type Runner struct {
	params Params
	client *apiclient.Client
}

func NewRunner(params Params) *Runner {
	return &Runner{
		params: params,
		client: apiclient.NewClient(params.APIKey, params.BaseURL),
	}
}

// Run starts the ReAct loop. It blocks until the conversation turn is done.
func (r *Runner) Run(ctx context.Context, events chan<- Event) {
	defer close(events)

	for turn := 0; ; turn++ {
		if err := ctx.Err(); err != nil {
			events <- Event{Type: EventTurnComplete, StopReason: "user_interrupt"}
			events <- Event{Type: EventQueryDone}
			return
		}

		apiMessages := extractMessages(r.params.Messages)
		apiReq := types.APIRequest{
			Model:     r.params.Model,
			MaxTokens: 8192,
			System:    r.params.SystemPrompt,
			Messages:  types.Messages(apiMessages).ToAPI(),
			Tools:     r.params.Tools.Definitions(),
		}

		stream, err := r.client.StreamMessages(ctx, apiReq)
		if err != nil {
			events <- Event{Type: EventError, Err: fmt.Errorf("API call: %w", err)}
			events <- Event{Type: EventQueryDone}
			return
		}

		turnResult := r.processStream(ctx, stream, events)
		stream.Close()

		if turnResult.Err != nil {
			events <- Event{Type: EventError, Err: turnResult.Err}
			events <- Event{Type: EventQueryDone}
			return
		}

		if err := ctx.Err(); err != nil {
			turnResult = r.handleInterrupt(turnResult, events)
			if turnResult == nil {
				events <- Event{Type: EventTurnComplete, StopReason: "user_interrupt"}
				events <- Event{Type: EventQueryDone}
				return
			}
		}

		events <- Event{Type: EventTurnComplete, StopReason: turnResult.StopReason}

		if len(turnResult.ToolUses) == 0 {
			events <- Event{Type: EventQueryDone}
			return
		}

		if turnResult.AssistantMsg.Role != "" {
			r.params.Messages = append(r.params.Messages, MessageState{
				Message:       turnResult.AssistantMsg,
				TurnCompleted: true,
			})
		}
		r.params.Messages = appendToolResults(r.params.Messages, turnResult.ToolResults)
	}
}

// ---------------------------------------------------------------------------
// Turn — per-stream state
// ---------------------------------------------------------------------------

type turn struct {
	ctx       context.Context
	runner    *Runner
	events    chan<- Event
	res       *TurnResultExt
	accBlocks []accBlock
	toolMu    sync.Mutex
	toolWG    sync.WaitGroup
	results   []toolResultItem
	toolUses  []ToolUseInfo
}

func newTurn(ctx context.Context, r *Runner, events chan<- Event) *turn {
	return &turn{
		ctx:    ctx,
		runner: r,
		events: events,
		res: &TurnResultExt{
			ToolResults: make([]types.ContentBlock, 0),
		},
	}
}

func (r *Runner) processStream(ctx context.Context, stream *apiclient.Stream, events chan<- Event) *TurnResultExt {
	t := newTurn(ctx, r, events)

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
			if t.res.Err != nil {
				return t.res
			}

		case "content_block_start":
			t.handleBlockStart(sseEvent.Data)
			if t.res.Err != nil {
				return t.res
			}

		case "content_block_delta":
			t.handleBlockDelta(sseEvent.Data)
			if t.res.Err != nil {
				return t.res
			}

		case "content_block_stop":
			t.handleBlockStop(sseEvent.Data)
			if t.res.Err != nil {
				return t.res
			}

		case "message_delta":
			t.handleMessageDelta(sseEvent.Data)
			if t.res.Err != nil {
				return t.res
			}

		case "message_stop":
			t.toolWG.Wait()
			t.res.ToolResults = t.buildToolResults()

		case "error":
			t.handleAPIError(sseEvent.Data)
			return t.res

		case "ping":
			// heartbeat — ignore
		}
	}

	// Build assistant message
	blocks := make([]types.ContentBlock, 0, len(t.accBlocks))
	for _, ab := range t.accBlocks {
		blocks = append(blocks, ab.block)
	}
	t.res.AssistantMsg = types.NewMessage("assistant", blocks)
	t.res.ToolUses = t.toolUses

	t.toolWG.Wait()
	if len(t.res.ToolResults) == 0 && len(t.toolUses) > 0 {
		t.res.ToolResults = t.buildToolResults()
	}

	return t.res
}

// ---------------------------------------------------------------------------
// SSE event handlers
// ---------------------------------------------------------------------------

func (t *turn) handleMessageStart(data json.RawMessage) {
	var msgData struct {
		Message struct {
			Usage struct {
				InputTokens int `json:"input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &msgData); err != nil {
		t.res.Err = fmt.Errorf("parse message_start: %w", err)
		return
	}
	t.res.Usage.InputTokens = msgData.Message.Usage.InputTokens
}

func (t *turn) handleBlockStart(data json.RawMessage) {
	var startData struct {
		Index        int             `json:"index"`
		ContentBlock json.RawMessage `json:"content_block"`
	}
	if err := json.Unmarshal(data, &startData); err != nil {
		t.res.Err = fmt.Errorf("parse content_block_start: %w", err)
		return
	}

	var block types.ContentBlock
	if err := block.ParseStart(startData.ContentBlock); err != nil {
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

func (t *turn) handleBlockDelta(data json.RawMessage) {
	var deltaData struct {
		Index int `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &deltaData); err != nil {
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

func (t *turn) handleBlockStop(data json.RawMessage) {
	var stopData struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal(data, &stopData); err != nil {
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

	// Launch tool execution immediately
	t.toolUses = append(t.toolUses, tu)
	t.toolWG.Add(1)
	go func(tu2 ToolUseInfo) {
		defer t.toolWG.Done()
		t.executeAndCollect(tu2)
	}(tu)
}

func (t *turn) handleMessageDelta(data json.RawMessage) {
	var deltaMsg struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &deltaMsg); err != nil {
		t.res.Err = fmt.Errorf("parse message_delta: %w", err)
		return
	}
	t.res.StopReason = deltaMsg.Delta.StopReason
	t.res.Usage.OutputTokens = deltaMsg.Usage.OutputTokens
}

func (t *turn) handleAPIError(data json.RawMessage) {
	var errData struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &errData); err == nil {
		t.res.Err = fmt.Errorf("API error [%s]: %s", errData.Error.Type, errData.Error.Message)
		return
	}
	t.res.Err = fmt.Errorf("API error: %s", string(data))
}

// ---------------------------------------------------------------------------
// Tool execution helpers
// ---------------------------------------------------------------------------

func (t *turn) executeAndCollect(tu ToolUseInfo) {
	t.events <- Event{
		Type:     EventToolExecStart,
		ToolID:   tu.ID,
		ToolName: tu.Name,
	}

	result, isError, err := t.runner.params.ExecuteTool(t.ctx, tu.Name, tu.Input, tu.ID)
	if err != nil {
		t.events <- Event{
			Type:    EventToolExecResult,
			ToolID:  tu.ID,
			IsError: true,
			Result:  fmt.Sprintf("execution error: %v", err),
		}
		t.toolMu.Lock()
		t.results = append(t.results, toolResultItem{
			Index: tu.Index,
			Block: types.ContentBlock{
				Type:      types.ContentBlockToolResult,
				ToolUseID: tu.ID,
				Content:   fmt.Sprintf("execution error: %v", err),
				IsError:   true,
			},
		})
		t.toolMu.Unlock()
		return
	}

	t.events <- Event{
		Type:    EventToolExecResult,
		ToolID:  tu.ID,
		Result:  result,
		IsError: isError,
	}

	t.toolMu.Lock()
	t.results = append(t.results, toolResultItem{
		Index: tu.Index,
		Block: types.ContentBlock{
			Type:      types.ContentBlockToolResult,
			ToolUseID: tu.ID,
			Content:   result,
			IsError:   isError,
		},
	})
	t.toolMu.Unlock()
}

func (t *turn) buildToolResults() []types.ContentBlock {
	resultByIndex := make(map[int]types.ContentBlock, len(t.results))
	for _, item := range t.results {
		resultByIndex[item.Index] = item.Block
	}

	out := make([]types.ContentBlock, len(t.toolUses))
	for i, tu := range t.toolUses {
		if b, ok := resultByIndex[tu.Index]; ok {
			out[i] = b
		} else {
			out[i] = types.ContentBlock{
				Type:      types.ContentBlockToolResult,
				ToolUseID: tu.ID,
				Content:   "no result available",
				IsError:   true,
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Interrupt handling
// ---------------------------------------------------------------------------

func (r *Runner) handleInterrupt(res *TurnResultExt, events chan<- Event) *TurnResultExt {
	if res != nil && len(res.ToolUses) > 0 && len(res.ToolResults) < len(res.ToolUses) {
		for i := len(res.ToolResults); i < len(res.ToolUses); i++ {
			tu := res.ToolUses[i]
			events <- Event{
				Type:    EventToolExecResult,
				ToolID:  tu.ID,
				IsError: true,
				Result:  "Interrupted by user",
			}
			res.ToolResults = append(res.ToolResults, types.ContentBlock{
				Type:      types.ContentBlockToolResult,
				ToolUseID: tu.ID,
				Content:   "Interrupted by user",
				IsError:   true,
			})
		}
		res.StopReason = "user_interrupt"
		return res
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func extractMessages(states []MessageState) []types.Message {
	out := make([]types.Message, 0, len(states))
	for _, s := range states {
		out = append(out, s.Message)
	}
	return out
}

func appendToolResults(states []MessageState, results []types.ContentBlock) []MessageState {
	if len(results) == 0 {
		return states
	}
	return append(states, MessageState{
		Message: types.NewMessage("user", results),
	})
}