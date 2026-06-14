package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/YYYSSSRRR/codepilot/internal/tool"
	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// Session holds the runtime state of an active sub-agent session.
type Session struct {
	ID       string
	Agent    Agent
	Messages []types.Message
}

// AsyncResult holds the completed result of an asynchronous sub-agent.
type AsyncResult struct {
	SessionID string
	Content   string
	IsError   bool
}

// Manager manages sub-agent lifecycle, sessions, and async results.
type Manager struct {
	agents  []Agent
	loader  *Loader
	reg     *tool.Registry
	deps    RunnerDeps

	mu       sync.Mutex
	sessions map[string]*Session
	nextID   int

	asyncMu     sync.Mutex
	asyncResults []AsyncResult
}

// NewManager creates a Manager with the given loader and runner dependencies.
func NewManager(loader *Loader, deps RunnerDeps) *Manager {
	return &Manager{
		agents:   loader.All(),
		loader:   loader,
		reg:      deps.Registry,
		deps:     deps,
		sessions: make(map[string]*Session),
	}
}

// Find looks up an agent definition by name.
func (m *Manager) Find(name string) (Agent, bool) {
	return m.loader.Find(name)
}

// All returns all agent definitions.
func (m *Manager) All() []Agent {
	return m.agents
}

// StartSync runs a sub-agent synchronously. It creates or continues a session
// and blocks until the sub-agent completes. Returns the final assistant text.
func (m *Manager) StartSync(ctx context.Context, agent Agent, task string, agentID string) (string, string, error) {
	m.mu.Lock()
	id := agentID
	if id == "" {
		id = fmt.Sprintf("agent_%x_%d", time.Now().UnixNano(), m.nextID)
		m.nextID++
	}

	// Find or create session
	session, exists := m.sessions[id]
	if !exists {
		session = &Session{ID: id, Agent: agent}
		m.sessions[id] = session
	}
	m.mu.Unlock()

	// Append the user task message
	msg := types.NewMessage("user", []types.ContentBlock{
		{Type: types.ContentBlockText, Text: task},
	})
	session.Messages = append(session.Messages, msg)

	// Record starting messages to transcript if available
	if m.deps.RecordTranscript != nil {
		m.deps.RecordTranscript(session.Messages)
	}

	// Run sub-agent
	result, err := RunSubAgent(ctx, m.deps, agent, session.Messages)
	if err != nil {
		return id, "", err
	}
	session.Messages = result

	// Record final messages
	if m.deps.RecordTranscript != nil {
		m.deps.RecordTranscript(session.Messages)
	}

	text := ExtractLastAssistantText(result)
	return id, text, nil
}

// StartAsync starts a sub-agent in the background.
// Returns the session ID immediately. Results are collected via DrainAsync.
func (m *Manager) StartAsync(ctx context.Context, agent Agent, task string, agentID string) (string, error) {
	m.mu.Lock()
	id := agentID
	if id == "" {
		id = fmt.Sprintf("agent_%x_%d", time.Now().UnixNano(), m.nextID)
		m.nextID++
	}

	session, exists := m.sessions[id]
	if !exists {
		session = &Session{ID: id, Agent: agent}
		m.sessions[id] = session
	}

	msg := types.NewMessage("user", []types.ContentBlock{
		{Type: types.ContentBlockText, Text: task},
	})
	session.Messages = append(session.Messages, msg)
	m.mu.Unlock()

	if m.deps.RecordTranscript != nil {
		m.deps.RecordTranscript(session.Messages)
	}

	// Run in background
	go func() {
		result, err := RunSubAgent(ctx, m.deps, agent, session.Messages)

		m.mu.Lock()
		session.Messages = result
		m.mu.Unlock()

		if m.deps.RecordTranscript != nil {
			m.deps.RecordTranscript(session.Messages)
		}

		content := ExtractLastAssistantText(result)
		if err != nil {
			content = fmt.Sprintf("sub-agent %q error: %v\n%s", agent.Name, err, content)
		}

		m.asyncMu.Lock()
		m.asyncResults = append(m.asyncResults, AsyncResult{
			SessionID: id,
			Content:   content,
			IsError:   err != nil,
		})
		m.asyncMu.Unlock()
	}()

	return id, nil
}

// DrainAsync returns and clears all completed async results.
func (m *Manager) DrainAsync() []AsyncResult {
	m.asyncMu.Lock()
	defer m.asyncMu.Unlock()
	if len(m.asyncResults) == 0 {
		return nil
	}
	out := make([]AsyncResult, len(m.asyncResults))
	copy(out, m.asyncResults)
	m.asyncResults = nil
	return out
}
