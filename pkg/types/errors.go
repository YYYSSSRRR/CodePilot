package types

import "fmt"

// AbortError is returned when a query is aborted by the user.
type AbortError struct {
	Reason string
}

func (e *AbortError) Error() string {
	return fmt.Sprintf("aborted: %s", e.Reason)
}

// ModelError wraps an API model error.
type ModelError struct {
	Code    string
	Message string
}

func (e *ModelError) Error() string {
	return fmt.Sprintf("model error [%s]: %s", e.Code, e.Message)
}

// PermissionDeniedError is returned when a tool invocation is denied.
type PermissionDeniedError struct {
	ToolName string
	Reason   string
}

func (e *PermissionDeniedError) Error() string {
	return fmt.Sprintf("permission denied for %s: %s", e.ToolName, e.Reason)
}
