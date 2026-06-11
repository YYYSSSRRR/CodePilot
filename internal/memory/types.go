package memory

import (
	"fmt"
	"strings"
)

// Type enumerates the four allowed memory categories.
type Type string

const (
	TypeUser      Type = "user"
	TypeFeedback  Type = "feedback"
	TypeProject   Type = "project"
	TypeReference Type = "reference"
)

// AllTypes returns all valid memory types.
func AllTypes() []Type {
	return []Type{TypeUser, TypeFeedback, TypeProject, TypeReference}
}

// Meta is the YAML-like frontmatter embedded in every memory file.
type Meta struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Type        Type   `json:"type" yaml:"type"`
}

// Validate checks that the meta has all required fields.
func (m Meta) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("memory name is required")
	}
	if m.Description == "" {
		return fmt.Errorf("memory description is required")
	}
	valid := false
	for _, t := range AllTypes() {
		if m.Type == t {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid memory type %q; must be one of: %s", m.Type, strings.Join(typesToStrings(), ", "))
	}
	return nil
}

func typesToStrings() []string {
	out := make([]string, len(AllTypes()))
	for i, t := range AllTypes() {
		out[i] = string(t)
	}
	return out
}

// File represents one persisted memory.
type File struct {
	Meta    Meta
	Content string // body after frontmatter
}

// IndexEntry is a single line in MEMORY.md.
type IndexEntry struct {
	Name        string
	Filename    string // e.g. "user_role.md"
	Description string
}

// IndexLine formats the entry as a MEMORY.md line: `- [Name](file.md) — description`
func (e IndexEntry) IndexLine() string {
	return fmt.Sprintf("- [%s](%s) — %s", e.Name, e.Filename, e.Description)
}

// ParseIndexLine parses a single MEMORY.md line into an IndexEntry.
func ParseIndexLine(line string) (IndexEntry, bool) {
	// Format: - [Name](file.md) — description
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "- [") {
		return IndexEntry{}, false
	}
	closeBracket := strings.Index(line, "](")
	if closeBracket < 0 {
		return IndexEntry{}, false
	}
	name := line[3:closeBracket]
	rest := line[closeBracket+2:]
	closeParen := strings.Index(rest, ")")
	if closeParen < 0 {
		return IndexEntry{}, false
	}
	filename := rest[:closeParen]
	desc := strings.TrimPrefix(rest[closeParen+1:], " —")
	desc = strings.TrimSpace(desc)
	return IndexEntry{
		Name:        name,
		Filename:    filename,
		Description: desc,
	}, true
}

// RenderFile serializes a memory File to markdown with frontmatter.
func RenderFile(f File) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", f.Meta.Name)
	fmt.Fprintf(&b, "description: %s\n", f.Meta.Description)
	fmt.Fprintf(&b, "type: %s\n", f.Meta.Type)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(f.Content))
	b.WriteString("\n")
	return b.String()
}