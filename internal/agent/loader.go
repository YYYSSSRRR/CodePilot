package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Loader scans ~/.codepilot/agents/ for agent definition markdown files.
type Loader struct {
	agents []Agent
}

// NewLoader creates a Loader and scans the agents directory.
func NewLoader() (*Loader, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	dir := filepath.Join(home, ".codepilot", "agents")

	l := &Loader{}
	if err := l.scan(dir); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Loader) scan(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read agents dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".md") && !strings.HasSuffix(entry.Name(), ".MD")) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		a, err := ParseFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent: skip %s: %v\n", entry.Name(), err)
			continue
		}
		l.agents = append(l.agents, a)
	}

	sort.Slice(l.agents, func(i, j int) bool {
		return l.agents[i].Name < l.agents[j].Name
	})
	return nil
}

// All returns all loaded agents.
func (l *Loader) All() []Agent {
	out := make([]Agent, len(l.agents))
	copy(out, l.agents)
	return out
}

// Find looks up an agent by name.
func (l *Loader) Find(name string) (Agent, bool) {
	for _, a := range l.agents {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}
