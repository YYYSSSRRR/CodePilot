package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Loader scans and caches skills from the skills directory.
type Loader struct {
	root  string
	infos []Info
}

// NewLoader scans ~/.codepilot/skills/ for skill directories and caches metadata.
func NewLoader() (*Loader, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	root := filepath.Join(home, ".codepilot", "skills")

	l := &Loader{root: root}
	if err := l.scan(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Loader) scan() error {
	entries, err := os.ReadDir(l.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read skills dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(l.root, entry.Name())
		skillPath := filepath.Join(dir, "SKILL.md")
		if _, err := os.Stat(skillPath); os.IsNotExist(err) {
			continue
		}
		info, err := parseInfo(skillPath, dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skill: skip %s: %v\n", entry.Name(), err)
			continue
		}
		l.infos = append(l.infos, info)
	}

	// Sort by name for deterministic ordering
	sort.Slice(l.infos, func(i, j int) bool {
		return l.infos[i].Name < l.infos[j].Name
	})
	return nil
}

// All returns all discovered skill infos.
func (l *Loader) All() []Info {
	out := make([]Info, len(l.infos))
	copy(out, l.infos)
	return out
}

// Find looks up a skill by name.
func (l *Loader) Find(name string) (Info, bool) {
	for _, info := range l.infos {
		if info.Name == name {
			return info, true
		}
	}
	return Info{}, false
}

// Root returns the skills root directory.
func (l *Loader) Root() string { return l.root }

// BuildSystemPromptSection returns a system prompt fragment listing available skills.
func (l *Loader) BuildSystemPromptSection() string {
	if len(l.infos) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n## Available Skills\n\n")
	b.WriteString("You have access to the following skills. Use the SkillTool to invoke one.\n\n")
	for _, info := range l.infos {
		b.WriteString(fmt.Sprintf("- **%s**: %s\n", info.Name, info.Description))
		if info.WhenToUse != "" {
			b.WriteString(fmt.Sprintf("  When to use: %s\n", info.WhenToUse))
		}
	}
	b.WriteString(fmt.Sprintf(`
To invoke a skill, call the SkillTool with:
{
  "name": "<skill-name>",
  "arguments": "<any arguments for the skill>"
}

The skill will expand into detailed instructions. Follow them precisely.
`))
	return b.String()
}
