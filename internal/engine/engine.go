package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YYYSSSRRR/codepilot/internal/api"
	"github.com/YYYSSSRRR/codepilot/internal/permission"
	"github.com/YYYSSSRRR/codepilot/internal/query"
	"github.com/YYYSSSRRR/codepilot/internal/token"
	"github.com/YYYSSSRRR/codepilot/internal/tool"
	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// Config for the QueryEngine.
type Config struct {
	Model        string
	SystemPrompt string
	MaxTokens    int
	APIKey       string
	BaseURL      string
	Tools        *tool.Registry
	Permissions  *permission.Checker

	// ContextWindow sets the model's total context window in tokens.
	// If zero, defaults to 128000.
	ContextWindow int

	// Transcript persists conversation history. If nil, persistence is skipped.
	Transcript TranscriptStore

	// OnPermissionAsk is called when the permission pipeline reaches an "ask" decision.
	OnPermissionAsk func(ctx context.Context, toolName string, input map[string]any, toolUseID string, reason string) permission.Decision
}

// TranscriptStore is the interface for persisting conversation transcripts.
type TranscriptStore interface {
	RecordTranscript(messages []types.Message) (int, error)
	Flush()
}

// QueryEngine manages multi-turn conversations with tool execution and permission checks.
type QueryEngine struct {
	config  Config
	counter *token.Counter

	mu       sync.Mutex
	messages []types.Message
	cancel   context.CancelFunc
	running  bool
}

func New(cfg Config) *QueryEngine {
	ctxWindow := cfg.ContextWindow
	if ctxWindow <= 0 {
		ctxWindow = 128000
	}
	return &QueryEngine{
		config:   cfg,
		counter:  token.NewCounter(cfg.Model, ctxWindow, cfg.MaxTokens),
		messages: make([]types.Message, 0, 32),
	}
}

// SubmitMessage sends a user prompt and returns a channel of streaming events.
func (e *QueryEngine) SubmitMessage(ctx context.Context, prompt string) (<-chan query.Event, error) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil, fmt.Errorf("a conversation turn is already in progress")
	}
	e.running = true

	// Append user message
	e.messages = append(e.messages, types.NewMessage("user", []types.ContentBlock{
		{Type: types.ContentBlockText, Text: prompt},
	}))

	// Persist user message before the API call (synchronous)
	if e.config.Transcript != nil {
		if _, err := e.config.Transcript.RecordTranscript(e.messages); err != nil {
			fmt.Fprintf(os.Stderr, "transcript: %v\n", err)
		}
	}

	snapshot := e.messages
	e.mu.Unlock()

	turnCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()

	events := make(chan query.Event, 64)
	go func() {
		defer func() {
			e.mu.Lock()
			e.running = false
			e.mu.Unlock()
		}()

		runner := query.NewRunner(e.makeDeps(), e.config.SystemPrompt, e.config.Model, e.config.MaxTokens)
		runner.Run(turnCtx, snapshot, e.config.Tools.Definitions(), events)

		// Capture the updated messages from the runner
		e.mu.Lock()
		e.messages = snapshot
		e.mu.Unlock()
	}()

	return events, nil
}

// Interrupt cancels the current turn.
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
	e.messages = make([]types.Message, 0, 32)
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

// SetMessages replaces the in-memory message list (e.g. loaded from transcript).
func (e *QueryEngine) SetMessages(msgs []types.Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = append(make([]types.Message, 0, len(msgs)), msgs...)
}

// Messages returns a copy of the current message list.
func (e *QueryEngine) Messages() []types.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]types.Message, len(e.messages))
	copy(out, e.messages)
	return out
}

// ---------------------------------------------------------------------------
// QueryDeps factory
// ---------------------------------------------------------------------------

func (e *QueryEngine) makeDeps() query.QueryDeps {
	return query.QueryDeps{
		CallModel: e.callModel,
		CanUseTool: func(ctx context.Context, toolName string, input map[string]any, toolUseID string) (bool, string) {
			result := e.config.Permissions.Check(ctx, toolName, input, toolUseID)

			switch result.Decision {
			case permission.DecisionDeny:
				return false, result.Message
			case permission.DecisionAsk:
				if e.config.OnPermissionAsk != nil {
					final := e.config.OnPermissionAsk(ctx, toolName, input, toolUseID, result.Message)
					switch final {
					case permission.DecisionDeny:
						return false, "Permission denied by user"
					case permission.DecisionAllow:
						return true, ""
					default:
						return false, "Permission denied (invalid response)"
					}
				}
				return false, fmt.Sprintf("Permission required: %s", result.Message)
			case permission.DecisionAllow:
				return true, ""
			default:
				return false, "Unknown permission decision"
			}
		},
		ExecuteTool: func(ctx context.Context, toolName string, input map[string]any) (string, bool, error) {
			t := e.config.Tools.FindByName(toolName)
			if t == nil {
				return fmt.Sprintf("tool %q not found", toolName), true, nil
			}
			if err := t.ValidateInput(input); err != nil {
				return fmt.Sprintf("invalid input: %v", err), true, nil
			}
			result, err := t.Call(ctx, input)
			if err != nil {
				return result, true, err
			}
			result = truncateResult(toolName, result, t.MaxResultSize())
			return result, false, nil
		},
		Compact: nil, //TODO
		RecordTranscript: func(messages []types.Message) {
			if e.config.Transcript != nil {
				if _, err := e.config.Transcript.RecordTranscript(messages); err != nil {
					fmt.Fprintf(os.Stderr, "transcript: %v\n", err)
				}
			}
		},
		TokenCount:           e.counter.TokenCount,
		AutoCompactThreshold: e.counter.AutoCompactThreshold,
		EffectiveWindow:      e.counter.EffectiveWindow,
	}
}


func truncateResult(toolName, result string, maxSize int) string {
	if maxSize <= 0 || len(result) <= maxSize {
		return result
	}

	tmpDir := filepath.Join(os.TempDir(), "codepilot-truncated")
	os.MkdirAll(tmpDir, 0755)

	filename := fmt.Sprintf("%s-%s.txt", toolName, strconv.FormatInt(time.Now().UnixNano(), 36))
	path := filepath.Join(tmpDir, filename)
	os.WriteFile(path, []byte(result), 0644)

	previewLines := 20
	lines := 0
	preview := ""
	for _, line := range strings.Split(result, "\n") {
		if lines >= previewLines {
			break
		}
		preview += line + "\n"
		lines++
	}
	if len(preview) > 2000 {
		preview = preview[:2000] + "..."
	}

	return fmt.Sprintf("Results truncated (%d chars, limit %d). Full output saved to: %s\n\nPreview:\n%s", len(result), maxSize, path, preview)
}

func (e *QueryEngine) callModel(ctx context.Context, req *api.Request) (api.Streamer, error) {
	client := api.NewDeepSeek(e.config.APIKey, e.config.BaseURL)
	return client.StreamMessages(ctx, req)
}
