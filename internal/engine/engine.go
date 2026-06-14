package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YYYSSSRRR/codepilot/internal/agent"
	"github.com/YYYSSSRRR/codepilot/internal/api"
	"github.com/YYYSSSRRR/codepilot/internal/compact"
	"github.com/YYYSSSRRR/codepilot/internal/mcp"
	"github.com/YYYSSSRRR/codepilot/internal/memory"
	"github.com/YYYSSSRRR/codepilot/internal/permission"
	"github.com/YYYSSSRRR/codepilot/internal/query"
	"github.com/YYYSSSRRR/codepilot/internal/skill"
	"github.com/YYYSSSRRR/codepilot/internal/token"
	"github.com/YYYSSSRRR/codepilot/internal/tool"
	"github.com/YYYSSSRRR/codepilot/internal/transcript"
	memoryutils "github.com/YYYSSSRRR/codepilot/internal/utils/memory"
	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// Config for the QueryEngine.
type Config struct {
	Model        string
	SmallModel   string
	SystemPrompt string
	MaxTokens    int
	APIKey       string
	BaseURL      string
	Tools        *tool.Registry
	Permissions  *permission.Checker

	ContextWindow int

	Transcript TranscriptStore

	OnPermissionAsk func(ctx context.Context, toolName string, input map[string]any, toolUseID string, reason string) permission.Decision

	// MCP manager for external tool servers (optional).
	MCPManager *mcp.Manager

	// SkillLoader for loading and listing skills (optional).
	SkillLoader *skill.Loader

	// AgentManager for sub-agent lifecycle (optional).
	AgentManager *agent.Manager
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

	memStore *memory.Store
	memOnce  sync.Once
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

func (e *QueryEngine) SubmitMessage(ctx context.Context, prompt string) (<-chan query.Event, error) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil, fmt.Errorf("a conversation turn is already in progress")
	}
	e.running = true

	e.messages = append(e.messages, types.NewMessage("user", []types.ContentBlock{
		{Type: types.ContentBlockText, Text: prompt},
	}))

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

		runner := query.NewRunner(e.makeDeps(), e.config.Model, e.config.MaxTokens)
		system := e.buildSystem(turnCtx)
		runner.Run(turnCtx, system, snapshot, e.config.Tools.Definitions(), events)

		e.mu.Lock()
		e.messages = snapshot
		e.mu.Unlock()
	}()

	return events, nil
}

func (e *QueryEngine) Interrupt() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
}

func (e *QueryEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = make([]types.Message, 0, 32)
	if e.cancel != nil {
		e.cancel()
	}
}

func (e *QueryEngine) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

func (e *QueryEngine) SetMessages(msgs []types.Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = append(make([]types.Message, 0, len(msgs)), msgs...)
}

func (e *QueryEngine) Messages() []types.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]types.Message, len(e.messages))
	copy(out, e.messages)
	return out
}

// ---------------------------------------------------------------------------
// System prompt assembly
// ---------------------------------------------------------------------------

func (e *QueryEngine) buildSystem(_ context.Context) string {
	var parts []string
	if e.config.SystemPrompt != "" {
		parts = append(parts, e.config.SystemPrompt)
	}
	if e.config.SkillLoader != nil {
		if skillsSection := e.config.SkillLoader.BuildSystemPromptSection(); skillsSection != "" {
			parts = append(parts, skillsSection)
		}
	}
	if e.config.AgentManager != nil {
		if agents := e.config.AgentManager.All(); len(agents) > 0 {
			parts = append(parts, agent.BuildSystemPromptSection(agents))
		}
	}
	if memCtx := e.loadMemoryContext(); memCtx != "" {
		parts = append(parts, memCtx)
	}
	return strings.Join(parts, "\n\n")
}

// ---------------------------------------------------------------------------
// Memory (system prompt section only; prefetch lives in utils/memory)
// ---------------------------------------------------------------------------

const memoryDirName = "memory"

func memoryDir() string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	gitRoot := strings.TrimSpace(string(output))
	if gitRoot == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	hash := transcript.ProjectDirHash(gitRoot)
	return filepath.Join(home, ".codepilot", "projects", hash, memoryDirName)
}

func (e *QueryEngine) initMemory() {
	e.memOnce.Do(func() {
		dir := memoryDir()
		if dir == "" {
			return
		}
		e.memStore = memory.NewStore(dir)
	})
}

func (e *QueryEngine) loadMemoryContext() string {
	e.initMemory()
	if e.memStore == nil {
		return ""
	}
	return memory.BuildSystemPromptSection(e.memStore)
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
		CompactSystem: &compact.ToolUseContext{
			ContentReplacement: &compact.ContentReplacementState{
				Replacements: make(map[string]string),
			},
			UnlimitedTools: make(map[string]bool),
		},
		Microcompact: func(msgs []types.Message) *compact.CompactResult {
			return compact.MaybeTimeBasedMicrocompact(msgs, compact.DefaultMicrocompactConfig())
		},
		AutoCompact: func(ctx context.Context, msgs []types.Message) ([]types.Message, error) {
			client := api.NewDeepSeek(e.config.APIKey, e.config.BaseURL)
			callLLM := func(innerCtx context.Context, prompt string) (string, error) {
				req := &api.Request{
					Model:     e.config.SmallModel,
					MaxTokens: 1024,
					Messages: []api.APIMessage{
						{Role: "user", Content: prompt},
					},
				}
				return client.CallMessages(innerCtx, req)
			}
			return compact.AutoCompact(ctx, msgs, callLLM)
		},
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
		MemoryPrefetch: &memoryutils.PrefetchConfig{
			APIKey:     e.config.APIKey,
			BaseURL:    e.config.BaseURL,
			SmallModel: e.config.SmallModel,
		},
		AsyncAgentCheck: func() []types.Message {
			if e.config.AgentManager == nil {
				return nil
			}
			results := e.config.AgentManager.DrainAsync()
			if len(results) == 0 {
				return nil
			}
			msgs := make([]types.Message, 0, len(results))
			for _, r := range results {
				msgs = append(msgs, types.NewMessage("user", []types.ContentBlock{
					{Type: types.ContentBlockText, Text: "## Async Sub-Agent Result\n\n**Agent Session ID**: " + r.SessionID + "\n\n" + r.Content},
				}))
			}
			return msgs
		},
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
