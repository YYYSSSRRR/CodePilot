package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/YYYSSSRRR/codepilot/internal/permissions"
	"github.com/YYYSSSRRR/codepilot/internal/query"
	"github.com/YYYSSSRRR/codepilot/internal/tool"
	"github.com/YYYSSSRRR/codepilot/internal/types"
)

// Config for the QueryEngine.
type Config struct {
	Model        string
	SystemPrompt string
	APIKey       string
	BaseURL      string
	Tools        *tool.Registry
	Permissions  *permissions.Checker

	// OnPermissionAsk is called when the permission pipeline reaches an "ask" decision.
	// It blocks the tool execution goroutine; the implementation should prompt the user.
	// Return DecisionAllow or DecisionDeny. Returning DecisionAsk is treated as Deny.
	// If nil, tools that require permission are denied.
	OnPermissionAsk func(ctx context.Context, toolName string, input map[string]any, toolUseID string, reason string) permissions.Decision

	// OnPermissionUpdate is called when the user chooses to save a permission rule
	// (always allow / always deny). Implementations should write to settings.json.
	// If nil, the choice applies only to the current invocation.
	OnPermissionUpdate func(rule permissions.PermissionRule)
}

// QueryEngine manages multi-turn conversations with tool execution and permission checks.
type QueryEngine struct {
	config Config

	mu       sync.Mutex
	messages []query.MessageState
	cancel   context.CancelFunc
	running  bool
}

func New(cfg Config) *QueryEngine {
	return &QueryEngine{
		config:   cfg,
		messages: make([]query.MessageState, 0, 32),
	}
}

// SubmitMessage sends a user prompt and returns a channel of streaming events.
func (e *QueryEngine) SubmitMessage(ctx context.Context, prompt string) (<-chan query.Event, error) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil, fmt.Errorf("a conversation turn is already in progress")
	}

	// Append user message
	e.messages = append(e.messages, query.MessageState{
		Message: types.NewMessage("user", []types.ContentBlock{{Type: types.ContentBlockText, Text: prompt}}),
	})

	// Build params — capture a reference to the MessageState slice
	snapshot := e.messages
	e.mu.Unlock()

	turnCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancel = cancel
	e.running = true
	e.mu.Unlock()

	events := make(chan query.Event, 64)
	go func() {
		defer func() {
			e.mu.Lock()
			e.running = false
			e.mu.Unlock()
		}()

		runner := query.NewRunner(query.Params{
			Messages:     snapshot,
			Tools:        e.config.Tools,
			SystemPrompt: e.config.SystemPrompt,
			APIKey:       e.config.APIKey,
			Model:        e.config.Model,
			BaseURL:      e.config.BaseURL,
			ExecuteTool:  e.executeToolFn(),
		})
		runner.Run(turnCtx, events)

		// Capture final messages — Runner.Run modifies the slice in-place
		e.mu.Lock()
		e.messages = snapshot
		e.mu.Unlock()
	}()

	return events, nil
}

// Interrupt cancels the current turn. The active tool (if any) completes;
// remaining queued tools receive synthetic "interrupted" results.
func (e *QueryEngine) Interrupt() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
}

// Reset clears history and cancels any active turn.
func (e *QueryEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = make([]query.MessageState, 0, 32)
	if e.cancel != nil {
		e.cancel()
	}
}

// IsRunning reports whether a turn is active.
func (e *QueryEngine) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// ---------------------------------------------------------------------------
// Tool execution
// ---------------------------------------------------------------------------

func (e *QueryEngine) executeToolFn() query.ExecuteToolFunc {
	return func(ctx context.Context, toolName string, input map[string]any, toolUseID string) (string, bool, error) {
		t := e.config.Tools.FindByName(toolName)
		if t == nil {
			return fmt.Sprintf("tool %q not found", toolName), true, nil
		}

		// Run the permission pipeline
		result := e.config.Permissions.Check(ctx, toolName, input, toolUseID)

		switch result.Decision {
		case permissions.DecisionDeny:
			return result.Message, true, nil

		case permissions.DecisionAsk:
			if e.config.OnPermissionAsk != nil {
				final := e.config.OnPermissionAsk(ctx, toolName, input, toolUseID, result.Message)
				switch final {
				case permissions.DecisionDeny:
					return "Permission denied by user", true, nil
				case permissions.DecisionAllow:
					// proceed to execute
				default:
					return "Permission denied (invalid response)", true, nil
				}
			} else {
				return fmt.Sprintf("Permission required: %s", result.Message), true, nil
			}

		case permissions.DecisionAllow:
			// proceed
		}

		// Execute the tool
		resultStr, err := t.Call(ctx, input)
		if err != nil {
			return resultStr, true, err
		}
		return resultStr, false, nil
	}
}