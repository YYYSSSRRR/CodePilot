package query

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

	// EventTextChunk
	Text string

	// EventToolUseStart / EventToolUseDone / EventToolExecStart / EventToolExecResult / EventToolPermissionDenied / EventToolPermissionAsk
	ToolID   string
	ToolName string

	// EventToolUseInput
	InputDelta string

	// EventToolUseDone
	Input map[string]any

	// EventToolExecResult / EventToolPermissionDenied
	Result  string
	IsError bool

	// EventTurnComplete
	StopReason string

	// EventError
	Err error
}