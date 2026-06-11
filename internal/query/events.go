package query

import "github.com/YYYSSSRRR/codepilot/pkg/types"

// EventType enumerates all possible streaming events emitted by the ReAct loop.
type EventType int

const (
	EventTextChunk EventType = iota
	EventToolUseStart
	EventToolUseInput
	EventToolUseDone
	EventToolExecStart
	EventToolExecResult
	EventToolPermissionDenied
	EventToolPermissionAsk
	EventTurnComplete
	EventQueryDone
	EventError
)

// Event is a single streaming event. Fields set depend on Type.
type Event struct {
	Type EventType

	// Text chunks
	Text string

	// Tool identification
	ToolID   string
	ToolName string

	// Tool input delta (partial JSON)
	InputDelta string

	// Tool input (complete at tool_use done)
	Input map[string]any

	// Tool execution result
	Result  string
	IsError bool

	// Turn complete
	StopReason string

	// Token usage (set on EventTurnComplete)
	Usage *types.Usage

	// Error
	Err error
}