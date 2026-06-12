package skill

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Tool is the SkillTool the model can call to invoke a skill.
type Tool struct {
	loader *Loader
}

// NewTool creates a new SkillTool.
func NewTool(loader *Loader) *Tool {
	return &Tool{loader: loader}
}

func (t *Tool) Name() string { return "SkillTool" }

func (t *Tool) Description() string {
	return "Invoke a skill by name. Skills provide specialized capabilities and instructions. Call this tool with the skill name and any arguments to expand the skill into detailed instructions."
}

func (t *Tool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name of the skill to invoke",
			},
			"arguments": map[string]any{
				"type":        "string",
				"description": "Arguments to pass to the skill (substituted into the skill prompt)",
			},
		},
		"required": []any{"name"},
	}
}

func (t *Tool) MaxResultSize() int { return 0 }

func (t *Tool) IsConcurrencySafe(map[string]any) bool { return false }

func (t *Tool) IsReadOnly(map[string]any) bool { return true }

func (t *Tool) CheckPermissions(map[string]any) (bool, string, string, error) {
	return true, "", "", nil
}

func (t *Tool) ValidateInput(input map[string]any) error {
	name, ok := input["name"].(string)
	if !ok || name == "" {
		return fmt.Errorf("missing required field: name")
	}
	return nil
}

// Call expands a skill: loads the full SKILL.md, substitutes arguments,
// replaces ${CLAUDE_SKILL_DIR}, and executes !`command` inline commands.
func (t *Tool) Call(ctx context.Context, input map[string]any) (string, error) {
	name, _ := input["name"].(string)
	args, _ := input["arguments"].(string)

	info, ok := t.loader.Find(name)
	if !ok {
		return "", fmt.Errorf("skill %q not found", name)
	}

	loaded, err := Load(info.Dir)
	if err != nil {
		return "", fmt.Errorf("load skill %q: %w", name, err)
	}

	content := loaded.Content

	// Substitute ${ARGUMENTS} with the provided arguments string
	if args != "" {
		content = strings.ReplaceAll(content, "${ARGUMENTS}", args)
		// Also substitute {{arguments}} for compatibility
		content = strings.ReplaceAll(content, "{{arguments}}", args)
	}

	// Replace ${CLAUDE_SKILL_DIR} with the skill directory path
	content = strings.ReplaceAll(content, "${CLAUDE_SKILL_DIR}", info.Dir)

	// Execute !`command` inline commands
	content = executeShellCommands(content)

	return content, nil
}

// executeShellCommands finds all !`command` patterns and replaces each
// with the stdout output of executing the command via bash -c.
func executeShellCommands(content string) string {
	re := regexp.MustCompile("!`([^`]+)`")
	matches := re.FindAllStringSubmatchIndex(content, -1)
	if matches == nil {
		return content
	}

	var out bytes.Buffer
	lastEnd := 0

	for _, match := range matches {
		// match[0]:start of full match, match[1]:end of full match
		// match[2]:start of capture group, match[3]:end of capture group
		cmd := content[match[2]:match[3]]

		// Write text before this match
		out.WriteString(content[lastEnd:match[0]])

		// Execute the command
		result, err := exec.Command("bash", "-c", cmd).Output()
		if err != nil {
			out.WriteString(fmt.Sprintf("\n[skill: command error: %v]\n", err))
			lastEnd = match[1]
			continue
		}
		out.Write(bytes.TrimSpace(result))
		lastEnd = match[1]
	}

	out.WriteString(content[lastEnd:])
	return out.String()
}
