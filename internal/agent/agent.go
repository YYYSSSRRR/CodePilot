package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Agent holds the parsed definition of a sub-agent.
type Agent struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Tools        []string `json:"tools"`
	Model        string   `json:"model"`
	SystemPrompt string   `json:"system_prompt"`
	Source       string   `json:"source"` // file path
}

// ParseFile parses an agent definition markdown file.
// Expected format:
//
//	---
//	name: code-reviewer
//	description: Reviews code
//	tools: Read, Glob, Grep
//	model: sonnet
//	---
//
//	System prompt body...
func ParseFile(path string) (Agent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Agent{}, fmt.Errorf("read %s: %w", path, err)
	}

	a := Agent{Source: path}
	content := string(data)

	// Parse frontmatter
	if strings.HasPrefix(content, "---") {
		rest := content[3:]
		endIdx := strings.Index(rest, "\n---")
		if endIdx >= 0 {
			fm := rest[:endIdx]
			a.SystemPrompt = strings.TrimSpace(rest[endIdx+5:])

			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				switch {
				case strings.HasPrefix(line, "name:"):
					a.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
				case strings.HasPrefix(line, "description:"):
					a.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
				case strings.HasPrefix(line, "tools:"):
					raw := strings.TrimSpace(strings.TrimPrefix(line, "tools:"))
					for _, t := range strings.Split(raw, ",") {
						t = strings.TrimSpace(t)
						if t != "" {
							a.Tools = append(a.Tools, t)
						}
					}
				case strings.HasPrefix(line, "model:"):
					a.Model = strings.TrimSpace(strings.TrimPrefix(line, "model:"))
				}
			}
		}
	}

	if a.SystemPrompt == "" && !strings.HasPrefix(content, "---") {
		a.SystemPrompt = strings.TrimSpace(content)
	}
	if a.Name == "" {
		a.Name = strings.TrimSuffix(filepath.Base(path), ".md")
	}

	return a, nil
}

// BuildSystemPromptSection returns a system prompt section listing available sub-agents.
func BuildSystemPromptSection(agents []Agent) string {
	if len(agents) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n## Available Sub-Agents\n\n")
	b.WriteString("You can delegate tasks to specialized sub-agents using the AgentTool.\n\n")
	for _, a := range agents {
		b.WriteString(fmt.Sprintf("- **%s**: %s\n", a.Name, a.Description))
		if len(a.Tools) > 0 {
			b.WriteString(fmt.Sprintf("  Tools: %s\n", strings.Join(a.Tools, ", ")))
		}
	}
	b.WriteString(`
To invoke a sub-agent, call the AgentTool with:
{
  "name": "<agent-name>",
  "task": "detailed task description",
  "mode": "sync" (default, waits for result) or "async" (background execution)
}

To continue a previous agent session, include:
{
  "agentID": "<agent-id-from-previous-call>",
  "task": "follow-up task"
}
`)
	return b.String()
}
