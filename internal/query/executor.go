package query

import (
	"context"
	"fmt"

	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// ToolUpdate carries the result of one completed tool execution.
type ToolUpdate struct {
	ToolUseID string
	Content   string
	IsError   bool
}

// StreamingToolExecutor queues tool_use blocks during streaming and
// executes them synchronously (one at a time) after streaming completes.
type StreamingToolExecutor struct {
	deps QueryDeps

	queued  []ToolUseInfo
	results []ToolUpdate
}

func NewStreamingToolExecutor(deps QueryDeps) *StreamingToolExecutor {
	return &StreamingToolExecutor{
		deps:    deps,
		queued:  make([]ToolUseInfo, 0, 8),
		results: make([]ToolUpdate, 0, 8),
	}
}

// AddTool queues a tool_use for later execution.
func (e *StreamingToolExecutor) AddTool(tu ToolUseInfo) {
	e.queued = append(e.queued, tu)
}

// Queued returns the number of queued tools.
func (e *StreamingToolExecutor) Queued() int {
	return len(e.queued)
}

// ExecuteAll runs all queued tools synchronously (one at a time) and
// returns the collected results. If the context is cancelled partway through,
// remaining tools get an "Interrupted by user" error result.
func (e *StreamingToolExecutor) ExecuteAll(ctx context.Context) []ToolUpdate {
	for _, tu := range e.queued {
		if ctx.Err() != nil {
			e.results = append(e.results, ToolUpdate{
				ToolUseID: tu.ID,
				Content:   "Interrupted by user",
				IsError:   true,
			})
			continue
		}

		allowed, reason := e.deps.CanUseTool(ctx, tu.Name, tu.Input, tu.ID)
		if !allowed {
			e.results = append(e.results, ToolUpdate{
				ToolUseID: tu.ID,
				Content:   reason,
				IsError:   true,
			})
			continue
		}

		result, isError, err := e.deps.ExecuteTool(ctx, tu.Name, tu.Input)
		if err != nil {
			e.results = append(e.results, ToolUpdate{
				ToolUseID: tu.ID,
				Content:   fmt.Sprintf("execution error: %v", err),
				IsError:   true,
			})
			continue
		}

		e.results = append(e.results, ToolUpdate{
			ToolUseID: tu.ID,
			Content:   result,
			IsError:   isError,
		})
	}
	return e.results
}

// Results returns all tool execution results collected so far.
func (e *StreamingToolExecutor) Results() []ToolUpdate {
	return e.results
}

// UpdatesToBlocks converts a ToolUpdate slice to ContentBlock slices.
func UpdatesToBlocks(updates []ToolUpdate) []types.ContentBlock {
	blocks := make([]types.ContentBlock, 0, len(updates))
	for _, u := range updates {
		blocks = append(blocks, types.ContentBlock{
			Type:      types.ContentBlockToolResult,
			ToolUseID: u.ToolUseID,
			Content:   u.Content,
			IsError:   u.IsError,
		})
	}
	return blocks
}
