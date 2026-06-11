package ui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/YYYSSSRRR/codepilot/internal/query"
	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// Renderer handles rendering streaming events to the terminal.
type Renderer struct{}

func NewRenderer() *Renderer {
	return &Renderer{}
}

// RenderEvents reads from the event channel and renders until it closes.
func (r *Renderer) RenderEvents(ctx context.Context, eventCh <-chan query.Event) {
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
				if event.Usage != nil {
					r.printUsage(event.Usage)
				}

			case query.EventQueryDone:
			case query.EventError:
				fmt.Fprintf(os.Stderr, "\n\033[31mError: %v\033[0m\n", event.Err)
			}
		}
	}
}

func (r *Renderer) printUsage(u *types.Usage) {
	parts := make([]string, 0, 2)
	if u.InputTokens > 0 {
		parts = append(parts, fmt.Sprintf("in: %d", u.InputTokens))
	}
	if u.OutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("out: %d", u.OutputTokens))
	}
	if len(parts) > 0 {
		fmt.Printf("\n\033[90m%s tokens\033[0m\n", strings.Join(parts, " | "))
	}
}
