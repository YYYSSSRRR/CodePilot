package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/YYYSSSRRR/codepilot/internal/permission"
)

// PermissionPrompter handles interactive permission prompts.
type PermissionPrompter struct {
	settingsPath string
}

func NewPermissionPrompter(settingsPath string) *PermissionPrompter {
	return &PermissionPrompter{settingsPath: settingsPath}
}

// Prompt asks the user for a permission decision.
func (p *PermissionPrompter) Prompt(ctx context.Context, toolName string, input map[string]any, toolUseID string, reason string) permission.Decision {
	fmt.Printf("\n  \033[33m🔑 Permission\033[0m tool=%s  %s\n", toolName, reason)
	fmt.Printf("  Input: %s\n", formatInput(input))

	for {
		fmt.Print("  ─────────────────────────────────────\n")
		fmt.Print("  \033[32m[A]\033[0m Allow once    \033[32m[AA]\033[0m Always allow\n")
		fmt.Print("  \033[31m[D]\033[0m Deny once     \033[31m[DD]\033[0m Always deny\n")
		fmt.Print("  Choice [A/D/AA/DD]: ")

		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			return permission.DecisionDeny
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return permission.DecisionDeny
		}

		switch strings.ToUpper(line) {
		case "A":
			return permission.DecisionAllow
		case "D":
			return permission.DecisionDeny
		case "AA":
			p.writeRule(permission.PermissionRule{
				Source:       permission.SourceProject,
				RuleBehavior: permission.BehaviorAllow,
				RuleValue: permission.RuleValue{
					ToolName:    toolName,
					RuleContent: inputContentPattern(toolName, input),
				},
			})
			fmt.Printf("  \033[32m✓ Rule saved: allow %s\033[0m\n", inputContentPattern(toolName, input))
			return permission.DecisionAllow
		case "DD":
			p.writeRule(permission.PermissionRule{
				Source:       permission.SourceProject,
				RuleBehavior: permission.BehaviorDeny,
				RuleValue: permission.RuleValue{
					ToolName:    toolName,
					RuleContent: inputContentPattern(toolName, input),
				},
			})
			fmt.Printf("  \033[31m✓ Rule saved: deny %s\033[0m\n", inputContentPattern(toolName, input))
			return permission.DecisionDeny
		default:
			fmt.Println("  Invalid choice. Enter A, D, AA, or DD.")
		}
	}
}

func formatInput(input map[string]any) string {
	b, err := json.Marshal(input)
	if err != nil {
		return fmt.Sprintf("%v", input)
	}
	return string(b)
}

func inputContentPattern(toolName string, input map[string]any) string {
	switch toolName {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			if len(cmd) > 80 {
				return cmd[:80] + "*"
			}
			return cmd
		}
	}
	return "*"
}

type settingsJSON struct {
	Rules []permission.PermissionRule `json:"permissionRules"`
}

func (p *PermissionPrompter) writeRule(rule permission.PermissionRule) {
	data, err := os.ReadFile(p.settingsPath)
	if err != nil {
		data = []byte("{}")
	}

	var cfg settingsJSON
	json.Unmarshal(data, &cfg)
	cfg.Rules = append(cfg.Rules, rule)

	updated, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(p.settingsPath, updated, 0644)
}