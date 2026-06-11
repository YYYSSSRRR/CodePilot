package ui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/YYYSSSRRR/codepilot/internal/engine"
	"github.com/YYYSSSRRR/codepilot/internal/transcript"
)

// REPL is the read-eval-print loop for the CLI.
type REPL struct {
	engine *engine.QueryEngine
}

func NewREPL(eng *engine.QueryEngine) *REPL {
	return &REPL{engine: eng}
}

// Run starts the CLI event loop.
func (r *REPL) Run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		if r.engine.IsRunning() {
			fmt.Println("\n⏳ Interrupting... (waiting for current tool to complete)")
			r.engine.Interrupt()
		} else {
			cancel()
		}
	}()

	printBanner()
	renderer := NewRenderer()
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
			r.engine.Reset()
			fmt.Println("Conversation reset.")
			continue
		case input == "/help":
			printHelp()
			continue
		}

		eventCh, err := r.engine.SubmitMessage(ctx, input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}

		renderer.RenderEvents(ctx, eventCh)
	}
}

// SelectSession prompts the user to pick a session or start a new one.
// Returns the chosen session ID.
func SelectSession(wdHash string) string {
	sessions, err := transcript.ListSessions(wdHash)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not list sessions: %v\n", err)
	}

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Println("\n\033[36m── Session ────────────────────────────────────\033[0m")
		if len(sessions) == 0 {
			fmt.Println("  No saved sessions found.")
		} else {
			fmt.Println("  Existing sessions:")
			for i, s := range sessions {
				label := s.ID
				if len(label) > 30 {
					label = label[:27] + "..."
				}
				fmt.Printf("  \033[33m[%d]\033[0m  %s  (\033[90m%d msgs\033[0m)\n", i+1, label, s.MsgCount)
			}
		}
		fmt.Println("  \033[33m[n]\033[0m  Start a new session")
		fmt.Print("\nChoose session \033[33m[1-n]\033[0m: ")

		if !scanner.Scan() {
			return newSessionID()
		}
		input := strings.TrimSpace(scanner.Text())

		// New session
		if input == "n" || input == "N" || input == "new" {
			return newSessionID()
		}

		// Pick by number
		var idx int
		if _, err := fmt.Sscanf(input, "%d", &idx); err == nil && idx >= 1 && idx <= len(sessions) {
			return sessions[idx-1].ID
		}

		fmt.Println("\033[31mInvalid choice, try again.\033[0m")
	}
}

func newSessionID() string {
	return time.Now().Format("2006-01-02_150405")
}

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
