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

// CheckPermissions implements tool.PermissionedTool.
func (t *BashTool) CheckPermissions(input map[string]any) (bool, string, string, error) {
	cmdStr, _ := input["command"].(string)

	// Read-only commands that are always safe
	readOnlyPrefixes := []string{
		"ls", "cat", "head", "tail", "echo", "pwd", "which",
		"git status", "git diff", "git log", "git branch",
		"npm list", "npm outdated",
	}
	for _, prefix := range readOnlyPrefixes {
		if strings.HasPrefix(strings.TrimSpace(cmdStr), prefix) {
			return true, "allow", "", nil
		}
	}

	// Danger commands that should always be denied (unless explicitly allowed by rules)
	dangerPrefixes := []string{
		"rm -rf /", "dd ", ":(){ :|:& };:",
	}
	for _, prefix := range dangerPrefixes {
		if strings.HasPrefix(cmdStr, prefix) {
			return false, "deny", "command appears dangerous", nil
		}
	}

	// Write operations — return "ask" so the permission pipeline can check rules
	return false, "ask", "this command may modify the system", nil
}

func (t *BashTool) IsWriteOperation(input map[string]any) bool {
	cmdStr, _ := input["command"].(string)
	readOnlyPrefixes := []string{"ls", "cat", "head", "tail", "echo", "pwd", "which", "git status", "git diff", "git log", "npm list"}
	for _, prefix := range readOnlyPrefixes {
		if strings.HasPrefix(strings.TrimSpace(cmdStr), prefix) {
			return false
		}
	}
	return true
}