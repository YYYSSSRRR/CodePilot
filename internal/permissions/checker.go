package permissions

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/YYYSSSRRR/codepilot/internal/tool"
)

// Checker implements the permission pipeline.
type Checker struct {
	settings *Settings
	registry *tool.Registry
}

func NewChecker(settings *Settings, registry *tool.Registry) *Checker {
	return &Checker{settings: settings, registry: registry}
}

// Check runs the full permission pipeline for a tool invocation.
func (c *Checker) Check(ctx context.Context, toolName string, input map[string]any, toolUseID string) Result {
	content := c.invocationContent(toolName, input)

	// Step 1a: Blanket deny — tool name + content="*" (deny all invocations)
	if c.hasRule(toolName, "*", BehaviorDeny) {
		return Result{Decision: DecisionDeny, Message: "denied by blanket rule"}
	}

	// Step 1b: Blanket allow — tool name + content="*" (allow all invocations)
	if c.hasRule(toolName, "*", BehaviorAllow) {
		return Result{Decision: DecisionAllow}
	}

	// Step 1c: Content-specific deny
	if content != "" && c.hasRule(toolName, content, BehaviorDeny) {
		return Result{Decision: DecisionDeny, Message: "denied by rule"}
	}

	// Step 1d: Content-specific allow
	if content != "" && c.hasRule(toolName, content, BehaviorAllow) {
		return Result{Decision: DecisionAllow}
	}

	// Step 2: Tool's own checkPermissions
	if pt, err := c.registry.FindPermissioned(toolName); err == nil && pt != nil {
		allowed, behavior, msg, checkErr := pt.CheckPermissions(input)
		if checkErr == nil && !allowed {
			return Result{
				Decision: c.behaviorToDecision(behavior),
				Message:  msg,
			}
		}
	}

	// Step 3: Pre-tool hooks (reserved)

	// Step 4: Ask rules — content pattern match
	if c.hasRule(toolName, content, BehaviorAsk) {
		return Result{Decision: DecisionAsk}
	}

	// Step 5: Default behaviour based on permissionMode
	return c.defaultResult(toolName)
}

func (c *Checker) defaultResult(toolName string) Result {
	switch c.settings.PermissionMode {
	case ModeBypass:
		return Result{Decision: DecisionAllow}
	case ModePlan:
		if c.isWriteTool(toolName) {
			return Result{Decision: DecisionDeny, Message: "write operations denied in plan mode"}
		}
		return Result{Decision: DecisionAllow}
	default: // ModeDefault
		if c.isWriteTool(toolName) {
			return Result{Decision: DecisionAsk}
		}
		return Result{Decision: DecisionAllow}
	}
}

func (c *Checker) isWriteTool(name string) bool {
	writeTools := map[string]bool{
		"Bash":     true,
		"Write":    true,
		"Edit":     true,
		"FileEdit": true,
		"create":   true,
		"edit":     true,
		"write":    true,
		"function": true,
	}
	return writeTools[name]
}

func (c *Checker) hasRule(toolName, content string, behavior RuleBehavior) bool {
	for _, r := range c.settings.PermissionRules {
		if r.RuleBehavior != behavior || r.RuleValue.ToolName != toolName {
			continue
		}
		if r.RuleValue.RuleContent == "*" {
			return true
		}
		if content == "" {
			continue
		}
		if c.globMatch(r.RuleValue.RuleContent, content) {
			return true
		}
	}
	return false
}

func (c *Checker) globMatch(pattern, str string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if strings.HasSuffix(pattern, "/**") {
			dirPrefix := strings.TrimSuffix(pattern, "/**")
			return str == dirPrefix || strings.HasPrefix(str, dirPrefix+"/")
		}
		if strings.HasPrefix(str, prefix) {
			return true
		}
		return false
	}
	if strings.Contains(pattern, "*") {
		return false
	}
	return str == pattern
}

func (c *Checker) behaviorToDecision(b string) Decision {
	switch b {
	case "allow":
		return DecisionAllow
	case "deny":
		return DecisionDeny
	case "ask":
		return DecisionAsk
	default:
		return DecisionAsk
	}
}

// invocationContent extracts a string representation of the tool's input for pattern matching.
func (c *Checker) invocationContent(toolName string, input map[string]any) string {
	switch toolName {
	case "Bash", "execute_bash", "bash":
		if cmd, ok := input["command"].(string); ok {
			return cmd
		}
	case "Read", "read", "file_read":
		if path, ok := input["path"].(string); ok {
			return path
		}
	case "Write", "write", "file_write":
		if path, ok := input["path"].(string); ok {
			return filepath.ToSlash(path)
		}
	}
	return ""
}