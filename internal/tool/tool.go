package tool

import "context"

// Tool is the unified interface every tool must implement.
type Tool interface {
	// Identity
	Name() string
	Description() string
	InputSchema() map[string]any

	// Execution
	Call(ctx context.Context, input map[string]any) (string, error)

	// MaxResultSize returns the maximum number of characters in the result.
	// 0 means unlimited. Results exceeding this are truncated and saved to temp files.
	MaxResultSize() int

	// IsConcurrencySafe returns true if this tool can be executed concurrently.
	IsConcurrencySafe(input map[string]any) bool

	// IsReadOnly returns true if this tool invocation does not modify state.
	IsReadOnly(input map[string]any) bool

	// CheckPermissions returns whether the invocation is allowed, a behavior hint,
	// and a human-readable message.
	//
	// allowed=false + behavior="allow" → tool has decided to allow
	// allowed=false + behavior="deny"  → tool has decided to deny
	// allowed=false + behavior="ask"   → tool wants user confirmation
	// allowed=true                     → tool defers to the pipeline
	CheckPermissions(input map[string]any) (allowed bool, behavior string, message string, err error)

	// ValidateInput checks that the input map has all required fields with correct types.
	ValidateInput(input map[string]any) error
}
