package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type BashTool struct{}

func (t *BashTool) Name() string        { return "Bash" }
func (t *BashTool) Description() string { return "Execute a bash command in the shell." }

func (t *BashTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The bash command to execute.",
			},
		},
		"required": []string{"command"},
	}
}

func (t *BashTool) Call(ctx context.Context, input map[string]any) (string, error) {
	cmdStr, _ := input["command"].(string)
	if cmdStr == "" {
		return "", fmt.Errorf("command is required")
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	}

	cmd.Stdin = os.Stdin
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}

func (t *BashTool) MaxResultSize() int { return 0 }

func (t *BashTool) IsConcurrencySafe(input map[string]any) bool { return false }

func (t *BashTool) IsReadOnly(input map[string]any) bool {
	cmdStr, _ := input["command"].(string)
	readOnlyPrefixes := []string{
		"ls", "cat", "head", "tail", "echo", "pwd", "which",
		"git status", "git diff", "git log", "git branch",
		"npm list", "npm outdated",
	}
	for _, prefix := range readOnlyPrefixes {
		if strings.HasPrefix(strings.TrimSpace(cmdStr), prefix) {
			return true
		}
	}
	return false
}

func (t *BashTool) CheckPermissions(input map[string]any) (bool, string, string, error) {
	cmdStr, _ := input["command"].(string)
	if cmdStr == "" {
		return false, "deny", "command is required", nil
	}

	// Danger commands that should always be denied
	dangerPrefixes := []string{
		"rm -rf /", "dd ", ":(){ :|:& };:",
	}
	for _, prefix := range dangerPrefixes {
		if strings.HasPrefix(cmdStr, prefix) {
			return false, "deny", "command appears dangerous", nil
		}
	}

	// Read-only commands — allow directly
	if t.IsReadOnly(input) {
		return false, "allow", "", nil
	}

	// Write operations — ask via pipeline
	return false, "ask", "this command may modify the system", nil
}

func (t *BashTool) ValidateInput(input map[string]any) error {
	cmdStr, _ := input["command"].(string)
	if cmdStr == "" {
		return fmt.Errorf("command is required")
	}
	return nil
}
