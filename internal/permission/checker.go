package permission

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

	// Step 1a: Blanket deny
	if c.hasRule(toolName, "*", BehaviorDeny) {
		return Result{Decision: DecisionDeny, Message: "denied by blanket rule"}
	}

	// Step 1b: Blanket allow
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

	// Step 2: Tool's own CheckPermissions
	if t := c.registry.FindByName(toolName); t != nil {
		allowed, behavior, msg, checkErr := t.CheckPermissions(input)
		if checkErr == nil && !allowed {
			return Result{
				Decision: c.behaviorToDecision(behavior),
				Message:  msg,
			}
		}
	}

	// Step 3: Pre-tool hooks (reserved)

	// Step 4: Ask rules
	if c.hasRule(toolName, content, BehaviorAsk) {
		return Result{Decision: DecisionAsk}
	}

	// Step 5: Default behaviour based on permissionMode
	return c.defaultResult(toolName, input)
}

func (c *Checker) defaultResult(toolName string, input map[string]any) Result {
	isReadOnly := c.isReadOnlyTool(toolName, input)

	switch c.settings.PermissionMode {
	case ModeBypass:
		return Result{Decision: DecisionAllow}
	case ModePlan:
		if !isReadOnly {
			return Result{Decision: DecisionDeny, Message: "write operations denied in plan mode"}
		}
		return Result{Decision: DecisionAllow}
	default:
		if !isReadOnly {
			return Result{Decision: DecisionAsk}
		}
		return Result{Decision: DecisionAllow}
	}
}

func (c *Checker) isReadOnlyTool(name string, input map[string]any) bool {
	t := c.registry.FindByName(name)
	if t == nil {
		return false
	}
	return t.IsReadOnly(input)
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
		return strings.HasPrefix(str, prefix)
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

func (c *Checker) invocationContent(toolName string, input map[string]any) string {
	switch toolName {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			return cmd
		}
	case "Read":
		if path, ok := input["path"].(string); ok {
			return path
		}
	case "Write":
		if path, ok := input["path"].(string); ok {
			return filepath.ToSlash(path)
		}
	}
	return ""
}
