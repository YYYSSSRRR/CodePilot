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
// Run — ReAct loop
// ---------------------------------------------------------------------------

func Run(ctx context.Context, p Params, events chan<- Event) {
	defer close(events)

	for turn := 0; ; turn++ {
		// 1. Check interrupt before starting a new turn
		if err := ctx.Err(); err != nil {
			events <- Event{Type: EventTurnComplete, StopReason: "user_interrupt"}
			events <- Event{Type: EventQueryDone}
			return
		}

		// 2. Build API request
		apiMessages := extractMessages(p.Messages)
		apiReq := types.APIRequest{
			Model:     p.Model,
			MaxTokens: 8192,
			System:    p.SystemPrompt,
			Messages:  types.MessagesToAPI(apiMessages),
			Tools:     p.Tools.Definitions(),
		}

		// 3. Call API streaming
		stream, err := apiclient.StreamMessages(ctx, p.APIKey, p.BaseURL, apiReq)
		if err != nil {
			events <- Event{Type: EventError, Err: fmt.Errorf("API call: %w", err)}
			events <- Event{Type: EventQueryDone}
			return
		}

		// 4. Process stream — tool_use blocks execute tools immediately in goroutines
		turnResult := processStream(ctx, stream, p, events)
		stream.Close()

		if turnResult.Err != nil {
			events <- Event{Type: EventError, Err: turnResult.Err}
			events <- Event{Type: EventQueryDone}
			return
		}

		// 5. Check interrupt — generate synthetic results for incomplete tools
		if err := ctx.Err(); err != nil {
			turnResult = handleInterrupt(turnResult, events)
			if turnResult == nil {
				events <- Event{Type: EventTurnComplete, StopReason: "user_interrupt"}
				events <- Event{Type: EventQueryDone}
				return
			}
		}

		// 6. Emit TurnComplete
		events <- Event{Type: EventTurnComplete, StopReason: turnResult.StopReason}

		// 7. No tool uses → conversation turn done
		if len(turnResult.ToolUses) == 0 {
			events <- Event{Type: EventQueryDone}
			return
		}

		// 8. Store messages for next turn
		if turnResult.AssistantMsg.Role != "" {
			p.Messages = append(p.Messages, MessageState{
				Message:       turnResult.AssistantMsg,
				TurnCompleted: true,
			})
		}
		p.Messages = appendToolResults(p.Messages, turnResult.ToolResults)
	}
}

// ---------------------------------------------------------------------------
// processStream
// ---------------------------------------------------------------------------

func processStream(ctx context.Context, stream *apiclient.Stream, p Params, events chan<- Event) *TurnResultExt {
	res := &TurnResultExt{}
	res.ToolResults = make([]types.ContentBlock, 0)

	var accBlocks []accBlock
	var (
		toolMu      sync.Mutex
		toolWG      sync.WaitGroup
		results     []toolResultItem
		toolUses    []ToolUseInfo
	)

	for {
		sseEvent, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				res.Err = ctx.Err()
				return res
			}
			res.Err = fmt.Errorf("stream recv: %w", err)
			return res
		}
		if err := ctx.Err(); err != nil {
			res.Err = err
			return res
		}

		switch sseEvent.Type {
		case "message_start":
			handleMessageStart(sseEvent.Data, res)
			if res.Err != nil {
				return res
			}

		case "content_block_start":
			handleBlockStart(sseEvent.Data, &accBlocks, events, res)
			if res.Err != nil {
				return res
			}

		case "content_block_delta":
			handleBlockDelta(sseEvent.Data, &accBlocks, events, res)
			if res.Err != nil {
				return res
			}

		case "content_block_stop":
			tu := handleBlockStop(sseEvent.Data, &accBlocks, events, res)
			if res.Err != nil {
				return res
			}
			if tu != nil {
				toolUses = append(toolUses, *tu)
				toolWG.Add(1)
				go func(t ToolUseInfo) {
					defer toolWG.Done()
					executeAndCollect(ctx, p, t, events, &toolMu, &results)
				}(*tu)
			}

		case "message_delta":
			handleMessageDelta(sseEvent.Data, res)
			if res.Err != nil {
				return res
			}

		case "message_stop":
			toolWG.Wait()
			res.ToolResults = buildToolResults(results, toolUses)

		case "error":
			handleAPIError(sseEvent.Data, res)
			return res

		case "ping":
			// heartbeat — ignore
		}
	}

	// Build assistant message
	blocks := make([]types.ContentBlock, 0, len(accBlocks))
	for _, ab := range accBlocks {
		blocks = append(blocks, ab.block)
	}
	res.AssistantMsg = types.NewAssistantMessage(blocks)
	res.ToolUses = toolUses

	toolWG.Wait()
	if len(res.ToolResults) == 0 && len(toolUses) > 0 {
		res.ToolResults = buildToolResults(results, toolUses)
	}

	return res
}

// ---------------------------------------------------------------------------
// SSE event handlers
// ---------------------------------------------------------------------------

func handleMessageStart(data json.RawMessage, res *TurnResultExt) {
	var msgData struct {
		Message struct {
			Usage struct {
				InputTokens int `json:"input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &msgData); err != nil {
		res.Err = fmt.Errorf("parse message_start: %w", err)
		return
	}
	res.Usage.InputTokens = msgData.Message.Usage.InputTokens
}

func handleBlockStart(data json.RawMessage, accBlocks *[]accBlock, events chan<- Event, res *TurnResultExt) {
	var startData struct {
		Index        int             `json:"index"`
		ContentBlock json.RawMessage `json:"content_block"`
	}
	if err := json.Unmarshal(data, &startData); err != nil {
		res.Err = fmt.Errorf("parse content_block_start: %w", err)
		return
	}

	block, err := types.ParseStartContentBlock(startData.ContentBlock)
	if err != nil {
		res.Err = fmt.Errorf("parse block: %w", err)
		return
	}

	for len(*accBlocks) <= startData.Index {
		*accBlocks = append(*accBlocks, accBlock{})
	}
	(*accBlocks)[startData.Index] = accBlock{block: block}

	if block.Type == types.ContentBlockToolUse {
		events <- Event{
			Type:     EventToolUseStart,
			ToolID:   block.ToolUseID,
			ToolName: block.Name,
		}
	}
}

func handleBlockDelta(data json.RawMessage, accBlocks *[]accBlock, events chan<- Event, res *TurnResultExt) {
	var deltaData struct {
		Index int `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &deltaData); err != nil {
		res.Err = fmt.Errorf("parse delta: %w", err)
		return
	}

	idx := deltaData.Index
	for len(*accBlocks) <= idx {
		*accBlocks = append(*accBlocks, accBlock{})
	}

	switch deltaData.Delta.Type {
	case "text_delta":
		(*accBlocks)[idx].block.Type = types.ContentBlockText
		(*accBlocks)[idx].block.Text += deltaData.Delta.Text
		events <- Event{Type: EventTextChunk, Text: deltaData.Delta.Text}


		case "thinking_delta":
			(*accBlocks)[idx].block.Type = types.ContentBlockThinking
			(*accBlocks)[idx].block.Thinking += deltaData.Delta.Text
	case "input_json_delta":
		(*accBlocks)[idx].jsonSB.WriteString(deltaData.Delta.PartialJSON)
		events <- Event{
			Type:       EventToolUseInput,
			ToolID:     (*accBlocks)[idx].block.ToolUseID,
			InputDelta: deltaData.Delta.PartialJSON,
		}
	}
}

func handleBlockStop(data json.RawMessage, accBlocks *[]accBlock, events chan<- Event, res *TurnResultExt) *ToolUseInfo {
	var stopData struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal(data, &stopData); err != nil {
		res.Err = fmt.Errorf("parse content_block_stop: %w", err)
		return nil
	}

	idx := stopData.Index
	if idx >= len(*accBlocks) {
		return nil
	}

	ab := &(*accBlocks)[idx]

	// Finalize tool_use input from accumulated JSON deltas
	if ab.block.Type == types.ContentBlockToolUse && ab.jsonSB.Len() > 0 {
		var input map[string]any
		if err := json.Unmarshal([]byte(ab.jsonSB.String()), &input); err == nil {
			ab.block.Input = input
		}
	}

	if ab.block.Type != types.ContentBlockToolUse {
		return nil
	}

	tu := &ToolUseInfo{
		Index: idx,
		ID:    ab.block.ToolUseID,
		Name:  ab.block.Name,
		Input: ab.block.Input,
	}

	events <- Event{
		Type:     EventToolUseDone,
		ToolID:   tu.ID,
		ToolName: tu.Name,
		Input:    tu.Input,
	}

	return tu
}

func handleMessageDelta(data json.RawMessage, res *TurnResultExt) {
	var deltaMsg struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &deltaMsg); err != nil {
		res.Err = fmt.Errorf("parse message_delta: %w", err)
		return
	}
	res.StopReason = deltaMsg.Delta.StopReason
	res.Usage.OutputTokens = deltaMsg.Usage.OutputTokens
}

func handleAPIError(data json.RawMessage, res *TurnResultExt) {
	var errData struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &errData); err == nil {
		res.Err = fmt.Errorf("API error [%s]: %s", errData.Error.Type, errData.Error.Message)
		return
	}
	res.Err = fmt.Errorf("API error: %s", string(data))
}

// ---------------------------------------------------------------------------
// Tool execution helpers
// ---------------------------------------------------------------------------

func executeAndCollect(
	ctx context.Context,
	p Params,
	tu ToolUseInfo,
	events chan<- Event,
	toolMu *sync.Mutex,
	results *[]toolResultItem,
) {
	events <- Event{
		Type:     EventToolExecStart,
		ToolID:   tu.ID,
		ToolName: tu.Name,
	}

	result, isError, err := p.ExecuteTool(ctx, tu.Name, tu.Input, tu.ID)
	if err != nil {
		events <- Event{
			Type:    EventToolExecResult,
			ToolID:  tu.ID,
			IsError: true,
			Result:  fmt.Sprintf("execution error: %v", err),
		}
		toolMu.Lock()
		*results = append(*results, toolResultItem{
			Index: tu.Index,
			Block: types.ContentBlock{
				Type:      types.ContentBlockToolResult,
				ToolUseID: tu.ID,
				Content:   fmt.Sprintf("execution error: %v", err),
				IsError:   true,
			},
		})
		toolMu.Unlock()
		return
	}

	events <- Event{
		Type:    EventToolExecResult,
		ToolID:  tu.ID,
		Result:  result,
		IsError: isError,
	}

	toolMu.Lock()
	*results = append(*results, toolResultItem{
		Index: tu.Index,
		Block: types.ContentBlock{
			Type:      types.ContentBlockToolResult,
			ToolUseID: tu.ID,
			Content:   result,
			IsError:   isError,
		},
	})
	toolMu.Unlock()
}

func buildToolResults(items []toolResultItem, toolUses []ToolUseInfo) []types.ContentBlock {
	// Map completed results by their SSE content block index.
	resultByIndex := make(map[int]types.ContentBlock, len(items))
	for _, item := range items {
		resultByIndex[item.Index] = item.Block
	}

	out := make([]types.ContentBlock, len(toolUses))
	for i, tu := range toolUses {
		if b, ok := resultByIndex[tu.Index]; ok {
			out[i] = b
		} else {
			// 有的工具调用没有返回结果，这里必须要补齐id，否则api会报错
			out[i] = types.ContentBlock{
				Type:      types.ContentBlockToolResult,
				ToolUseID: tu.ID,
				Content:   "no result available",
				IsError:   true,
			}
		}
	}

	fmt.Printf("[DEBUG REQUEST] buildToolResults: toolUses=%d results=%d out=%+v\n", len(toolUses), len(items), out)
	return out
}

func handleInterrupt(res *TurnResultExt, events chan<- Event) *TurnResultExt {
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
		Message: types.NewToolResultMessage(results),
	})
}