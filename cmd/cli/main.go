package main

import (
	"context"
	"fmt"
	"os"

	"github.com/YYYSSSRRR/codepilot/internal/config"
	"github.com/YYYSSSRRR/codepilot/internal/engine"
	"github.com/YYYSSSRRR/codepilot/internal/permission"
	"github.com/YYYSSSRRR/codepilot/internal/tool"
	"github.com/YYYSSSRRR/codepilot/internal/tool/tools"
	"github.com/YYYSSSRRR/codepilot/internal/transcript"
	"github.com/YYYSSSRRR/codepilot/internal/ui"
	"github.com/YYYSSSRRR/codepilot/pkg/types"
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
		&tools.GrepTool{},
		&tools.GlobTool{},
	)

	checker := permission.NewChecker(cfg.Settings, reg)
	prompter := ui.NewPermissionPrompter(cfg.SettingsPath)

	// ── Transcript / Session ──────────────────────────────────────
	project := transcript.NewProject()
	defer project.Close()

	wdHash := transcript.ProjectDirHash(cfg.WorkingDir)
	sessionID := ui.SelectSession(wdHash)

	sessionPath, err := transcript.SessionPath(wdHash, sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "transcript path: %v\n", err)
		os.Exit(1)
	}
	store := transcript.NewStore(project, sessionPath)
	defer store.Flush()

	// Load previous messages if resuming a session
	var initialMessages []types.Message
	if msgs, err := store.LoadMessages(); err == nil && len(msgs) > 0 {
		initialMessages = msgs
		fmt.Printf("Resumed session \033[33m%s\033[0m (\033[90m%d messages\033[0m)\n", sessionID, len(msgs))
	} else {
		fmt.Printf("New session \033[33m%s\033[0m\n", sessionID)
	}

	eng := engine.New(engine.Config{
		Model:        cfg.Model,
		SystemPrompt: defaultSystemPrompt(cfg.WorkingDir),
		MaxTokens:    cfg.MaxTokens,
		APIKey:       cfg.APIKey,
		BaseURL:      cfg.BaseURL,
		Tools:        reg,
		Permissions:  checker,
		Transcript:   store,
		OnPermissionAsk: prompter.Prompt,
	})
	if len(initialMessages) > 0 {
		eng.SetMessages(initialMessages)
	}

	repl := ui.NewREPL(eng)
	repl.Run(context.Background())
}

func defaultSystemPrompt(wd string) string {
	return `You are CodePilot, an AI coding assistant. You help users with software engineering tasks.
You have access to tools that let you execute bash commands, read files, write files, search code with Grep, and list files with Glob.

When using tools:
1. Prefer existing tools over suggesting manual steps
2. Show the user what you're doing by explaining before using a tool
3. If a command might be destructive, warn first

CRITICAL: Only address the most recent user message. Previous conversation turns are provided only for context — do NOT redo tasks from earlier turns, do NOT re-execute tool calls from previous questions, and do NOT repeat information already provided. Focus exclusively on the latest request.

Be concise and helpful. Your responses should be clear and actionable.

Working directory: ` + wd
}

func init() {
	// Ensure the agent, compact, context, search, and session packages
	// are imported (stubs for future implementation).
	var _ []struct{}
}
