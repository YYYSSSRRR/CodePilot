package permissions

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/YYYSSSRRR/codepilot/internal/tool"
)

// globMatch reports whether str matches the glob pattern.
func globMatch(pattern, str string) bool {
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
	content := invocationContent(toolName, input)

	// Step 1a: Blanket deny — tool name + content="*" (deny all invocations)
	if hasRule(c.settings.PermissionRules, toolName, "*", BehaviorDeny) {
		return Result{Decision: DecisionDeny, Message: "denied by blanket rule"}
	}

	// Step 1b: Blanket allow — tool name + content="*" (allow all invocations)
	if hasRule(c.settings.PermissionRules, toolName, "*", BehaviorAllow) {
		return Result{Decision: DecisionAllow}
	}

	// Step 1c: Content-specific deny
	if content != "" && hasRule(c.settings.PermissionRules, toolName, content, BehaviorDeny) {
		return Result{Decision: DecisionDeny, Message: "denied by rule"}
	}

	// Step 1d: Content-specific allow
	if content != "" && hasRule(c.settings.PermissionRules, toolName, content, BehaviorAllow) {
		return Result{Decision: DecisionAllow}
	}

	// Step 2: Tool's own checkPermissions
	if pt, err := c.registry.FindPermissioned(toolName); err == nil && pt != nil {
		allowed, behavior, msg, checkErr := pt.CheckPermissions(input)
		if checkErr == nil && !allowed {
			return Result{
				Decision: behaviorToDecision(behavior),
				Message:  msg,
			}
		}
	}

	// Step 3: Pre-tool hooks (reserved)

	// Step 4: Ask rules — content pattern match
	if hasRule(c.settings.PermissionRules, toolName, content, BehaviorAsk) {
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

// invocationContent extracts a string representation of the tool's input for pattern matching.
func invocationContent(toolName string, input map[string]any) string {
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

// hasRule checks if any rule matches the given tool, content, and behavior.
// For blanket matching, pass content="*". For pattern matching, pass the actual content string.
func hasRule(rules []PermissionRule, toolName, content string, behavior RuleBehavior) bool {
	for _, r := range rules {
		if r.RuleBehavior != behavior || r.RuleValue.ToolName != toolName {
			continue
		}
		// Blanket rule
		if r.RuleValue.RuleContent == "*" {
			return true
		}
		// Empty content can only match blanket rules
		if content == "" {
			continue
		}
		// Pattern match
		if globMatch(r.RuleValue.RuleContent, content) {
			return true
		}
	}
	return false
}

func behaviorToDecision(b string) Decision {
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