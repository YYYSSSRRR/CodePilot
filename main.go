package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/YYYSSSRRR/codepilot/internal/config"
	"github.com/YYYSSSRRR/codepilot/internal/engine"
	"github.com/YYYSSSRRR/codepilot/internal/permissions"
	"github.com/YYYSSSRRR/codepilot/internal/query"
	"github.com/YYYSSSRRR/codepilot/internal/tool"
	"github.com/YYYSSSRRR/codepilot/internal/tools"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Permission mode: %s (%d rules)\n", cfg.Settings.PermissionMode, len(cfg.Settings.PermissionRules))

	reg := tool.NewRegistry(
		&tools.BashTool{},
		&tools.ReadTool{},
		&tools.WriteTool{},
	)

	eng := engine.New(engine.Config{
		Model:        cfg.Model,
		SystemPrompt: defaultSystemPrompt(cfg.WorkingDir),
		APIKey:       cfg.APIKey,
		BaseURL:      cfg.BaseURL,
		Tools:        reg,
		Permissions:  permissions.NewChecker(cfg.Settings, reg),
		OnPermissionAsk: func(ctx context.Context, toolName string, input map[string]any, _ string, reason string) permissions.Decision {
			return promptPermission(toolName, input, reason, cfg.SettingsPath)
		},
	})

	app := &App{
		engine: eng,
		cfg:    cfg,
	}
	app.Run()
}

// App is the CLI application.
type App struct {
	engine *engine.QueryEngine
	cfg    *config.Config
}

// Run starts the CLI event loop.
func (app *App) Run() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		if app.engine.IsRunning() {
			fmt.Println("\n⏳ Interrupting... (waiting for current tool to complete)")
			app.engine.Interrupt()
		} else {
			cancel()
		}
	}()

	printBanner()
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch {
		case input == "/quit" || input == "/exit":
			fmt.Println("Goodbye!")
			return
		case input == "/reset":
			app.engine.Reset()
			fmt.Println("Conversation reset.")
			continue
		case input == "/help":
			printHelp()
			continue
		}

		eventCh, err := app.engine.SubmitMessage(ctx, input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}

		renderEvents(ctx, eventCh)
	}
}

// ---------------------------------------------------------------------------
// Event rendering
// ---------------------------------------------------------------------------

func renderEvents(ctx context.Context, eventCh <-chan query.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}

			switch event.Type {
			case query.EventTextChunk:
				fmt.Print(event.Text)

			case query.EventToolUseStart:
				fmt.Printf("\n\033[36m[Tool] %s\033[0m ", event.ToolName)

			case query.EventToolUseDone:
				fmt.Println()

			case query.EventToolExecStart:
				fmt.Println()

			case query.EventToolExecResult:
				if event.IsError {
					fmt.Printf("  \033[31m✗ Error:\033[0m %s\n", event.Result)
				} else if event.Result != "" {
					lines := strings.Split(event.Result, "\n")
					for _, line := range lines {
						if len(line) > 300 {
							line = line[:300] + "..."
						}
						fmt.Printf("  \033[90m│ %s\033[0m\n", line)
					}
				}
				fmt.Println()

			case query.EventTurnComplete:
			case query.EventQueryDone:
			case query.EventError:
				fmt.Fprintf(os.Stderr, "\n\033[31mError: %v\033[0m\n", event.Err)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Interactive permission prompt
// ---------------------------------------------------------------------------

func promptPermission(toolName string, input map[string]any, reason string, settingsPath string) permissions.Decision {
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
			return permissions.DecisionDeny
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return permissions.DecisionDeny
		}

		switch strings.ToUpper(line) {
		case "A":
			return permissions.DecisionAllow
		case "D":
			return permissions.DecisionDeny
		case "AA":
			writeRuleToFile(settingsPath, permissions.PermissionRule{
				Source:       permissions.SourceProject,
				RuleBehavior: permissions.BehaviorAllow,
				RuleValue: permissions.RuleValue{
					ToolName:    toolName,
					RuleContent: inputContentPattern(toolName, input),
				},
			})
			fmt.Printf("  \033[32m✓ Rule saved: allow %s\033[0m\n", inputContentPattern(toolName, input))
			return permissions.DecisionAllow
		case "DD":
			writeRuleToFile(settingsPath, permissions.PermissionRule{
				Source:       permissions.SourceProject,
				RuleBehavior: permissions.BehaviorDeny,
				RuleValue: permissions.RuleValue{
					ToolName:    toolName,
					RuleContent: inputContentPattern(toolName, input),
				},
			})
			fmt.Printf("  \033[31m✓ Rule saved: deny %s\033[0m\n", inputContentPattern(toolName, input))
			return permissions.DecisionDeny
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

func writeRuleToFile(path string, rule permissions.PermissionRule) {
	data, err := os.ReadFile(path)
	if err != nil {
		data = []byte("{}")
	}

	var cfg struct {
		Rules []permissions.PermissionRule `json:"permissionRules"`
	}
	json.Unmarshal(data, &cfg)
	cfg.Rules = append(cfg.Rules, rule)

	updated, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(path, updated, 0644)
}

// ---------------------------------------------------------------------------
// Banner & help
// ---------------------------------------------------------------------------

func printBanner() {
	fmt.Println("\033[36m╭──────────────────────────────────────────╮")
	fmt.Println("│          CodePilot v0.1.0                   │")
	fmt.Println("│   A code agent powered by Claude            │")
	fmt.Println("╰──────────────────────────────────────────╯\033[0m")
	fmt.Println("Type \033[33m/help\033[0m for commands, Ctrl+C to interrupt.")
}

func printHelp() {
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  /help       Show this help")
	fmt.Println("  /reset      Reset conversation")
	fmt.Println("  /quit       Exit")
	fmt.Println("  /exit       Exit")
	fmt.Println()
	fmt.Println("Press Ctrl+C to interrupt the current response.")
}

func defaultSystemPrompt(wd string) string {
	return `You are CodePilot, an AI coding assistant. You help users with software engineering tasks.
You have access to tools that let you execute bash commands, read files, and write files.

When using tools:
1. Prefer existing tools over suggesting manual steps
2. Show the user what you're doing by explaining before using a tool
3. If a command might be destructive, warn first

Be concise and helpful. Your responses should be clear and actionable.

Working directory: ` + wd
}