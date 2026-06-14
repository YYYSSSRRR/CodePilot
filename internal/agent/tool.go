package agent

import (
	"context"
	"fmt"
)

// Tool is the AgentTool the model calls to delegate tasks to sub-agents.
type Tool struct {
	manager *Manager
}

// NewTool creates a new AgentTool.
func NewTool(mgr *Manager) *Tool {
	return &Tool{manager: mgr}
}

func (t *Tool) Name() string { return "AgentTool" }

func (t *Tool) Description() string {
	return `Delegate a task to a specialized sub-agent. Sub-agents have their own system prompt and restricted tool access. Use this when a task requires focused expertise, a second opinion, or can be parallelized.

**sync mode** (default): The main agent pauses and waits for the sub-agent's result. Use for tasks that need to complete before continuing.

**async mode**: The sub-agent runs in the background. The main agent continues immediately. Results are delivered when ready. Use for tasks that don't block the main flow.

To continue a previous sub-agent conversation, provide the agentID returned from a prior call.`
}

func (t *Tool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name of the sub-agent to invoke",
			},
			"task": map[string]any{
				"type":        "string",
				"description": "The task description for the sub-agent. Be specific and include all necessary context.",
			},
			"mode": map[string]any{
				"type":        "string",
				"description": "Execution mode: 'sync' (wait for result) or 'async' (background)",
				"enum":        []any{"sync", "async"},
			},
			"agentID": map[string]any{
				"type":        "string",
				"description": "Existing agent session ID to continue. Use to resume a previous sub-agent conversation.",
			},
		},
		"required": []any{"name", "task"},
	}
}

func (t *Tool) MaxResultSize() int { return 0 }

func (t *Tool) IsConcurrencySafe(map[string]any) bool { return false }

func (t *Tool) IsReadOnly(map[string]any) bool { return true }

func (t *Tool) CheckPermissions(map[string]any) (bool, string, string, error) {
	return true, "", "", nil
}

func (t *Tool) ValidateInput(input map[string]any) error {
	name, _ := input["name"].(string)
	if name == "" {
		return fmt.Errorf("missing required field: name")
	}
	if _, ok := input["task"].(string); !ok {
		return fmt.Errorf("missing required field: task")
	}
	return nil
}

func (t *Tool) Call(ctx context.Context, input map[string]any) (string, error) {
	name, _ := input["name"].(string)
	task, _ := input["task"].(string)
	mode, _ := input["mode"].(string)
	agentID, _ := input["agentID"].(string)

	if task == "" {
		return "", fmt.Errorf("task is empty")
	}

	agent, ok := t.manager.Find(name)
	if !ok {
		return "", fmt.Errorf("sub-agent %q not found", name)
	}

	if mode == "async" {
		id, err := t.manager.StartAsync(ctx, agent, task, agentID)
		if err != nil {
			return "", fmt.Errorf("start async agent: %w", err)
		}
		return fmt.Sprintf("✅ Task submitted to sub-agent **%s** for background execution.\n\nAgent session ID: `%s`\n\nYou will receive the result when it completes.", agent.Name, id), nil
	}

	// Default: sync
	id, result, err := t.manager.StartSync(ctx, agent, task, agentID)
	if err != nil {
		return fmt.Sprintf("Sub-agent %q returned an error: %v\n\nPartial result:\n%s", agent.Name, err, result), nil
	}

	// Build a structured result with agent info for the model
	return fmt.Sprintf(`## Sub-Agent Result

**Agent**: %s
**Agent Session ID**: %s

%s

---
*You can continue this agent session by calling AgentTool with agentID="%s".`, agent.Name, id, result, id), nil

}

