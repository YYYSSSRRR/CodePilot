package memory

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	EntrypointName      = "MEMORY.md"
	MaxEntrypointLines  = 200
	MaxEntrypointBytes  = 25_000
)

// Store manages the file-based memory system.
// All memories live in a single directory as .md files with frontmatter.
// The index (MEMORY.md) tracks all files with one-line entries.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore creates a Store rooted at dir (e.g. ~/.codepilot/projects/<hash>/memory/).
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Dir returns the memory directory path.
func (s *Store) Dir() string { return s.dir }

// EnsureDir creates the memory directory (and parents) if it doesn't exist.
func (s *Store) EnsureDir() error {
	return os.MkdirAll(s.dir, 0755)
}

// ---------------------------------------------------------------------------
// Index (MEMORY.md)
// ---------------------------------------------------------------------------

// ReadIndex returns all entries from MEMORY.md.
func (s *Store) ReadIndex() ([]IndexEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(filepath.Join(s.dir, EntrypointName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read index: %w", err)
	}
	defer f.Close()

	var entries []IndexEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if entry, ok := ParseIndexLine(line); ok {
			entries = append(entries, entry)
		}
	}
	return entries, scanner.Err()
}

// ReadIndexContent returns the raw MEMORY.md text, truncated to limits.
func (s *Store) ReadIndexContent() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(filepath.Join(s.dir, EntrypointName))
	if err != nil {
		return ""
	}
	return truncateIndex(string(data))
}

// AppendToIndex appends a new entry line to MEMORY.md.
func (s *Store) AppendToIndex(entry IndexEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(filepath.Join(s.dir, EntrypointName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("append index: %w", err)
	}
	defer f.Close()

	line := entry.IndexLine() + "\n"
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	return nil
}

// RemoveFromIndex removes entries matching name from MEMORY.md.
func (s *Store) RemoveFromIndex(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dir, EntrypointName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var out []string
	removed := false
	for _, line := range strings.Split(string(data), "\n") {
		if entry, ok := ParseIndexLine(line); ok && entry.Name == name {
			removed = true
			continue
		}
		out = append(out, line)
	}
	if !removed {
		return nil
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0644)
}

// ---------------------------------------------------------------------------
// Memory files
// ---------------------------------------------------------------------------

// ReadMemory reads and parses a single memory file.
func (s *Store) ReadMemory(name string) (*File, error) {
	path := filepath.Join(s.dir, name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read memory %s: %w", name, err)
	}
	return parseFile(data), nil
}

// WriteMemory writes a memory file and updates the index.
// If the file already exists, it updates both the file and the index entry.
func (s *Store) WriteMemory(f File) error {
	if err := f.Meta.Validate(); err != nil {
		return err
	}
	if err := s.EnsureDir(); err != nil {
		return err
	}

	// Write the file
	path := filepath.Join(s.dir, f.Meta.Name+".md")
	content := RenderFile(f)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write memory: %w", err)
	}

	// Update index
	s.RemoveFromIndex(f.Meta.Name)
	s.AppendToIndex(IndexEntry{
		Name:        f.Meta.Name,
		Filename:    f.Meta.Name + ".md",
		Description: f.Meta.Description,
	})
	return nil
}

// DeleteMemory removes a memory file and its index entry.
func (s *Store) DeleteMemory(name string) error {
	path := filepath.Join(s.dir, name+".md")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete memory: %w", err)
	}
	return s.RemoveFromIndex(name)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseFile parses a markdown file with frontmatter.
func parseFile(data []byte) *File {
	text := string(data)

	// Parse frontmatter between --- markers
	var meta Meta
	body := text

	if strings.HasPrefix(text, "---\n") {
		end := strings.Index(text[4:], "\n---")
		if end >= 0 {
			fm := text[4 : 4+end]
			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				switch {
				case strings.HasPrefix(line, "name:"):
					meta.Name = strings.TrimSpace(line[5:])
				case strings.HasPrefix(line, "description:"):
					meta.Description = strings.TrimSpace(line[12:])
				case strings.HasPrefix(line, "type:"):
					meta.Type = Type(strings.TrimSpace(line[5:]))
				}
			}
			body = strings.TrimSpace(text[4+end+5:])
		}
	}

	return &File{
		Meta:    meta,
		Content: body,
	}
}

// truncateIndex truncates MEMORY.md to line and byte limits.
func truncateIndex(raw string) string {
	trimmed := strings.TrimSpace(raw)
	lines := strings.Split(trimmed, "\n")

	if len(lines) > MaxEntrypointLines {
		lines = lines[:MaxEntrypointLines]
	}

	result := strings.Join(lines, "\n")
	if len(result) > MaxEntrypointBytes {
		cut := strings.LastIndex(result[:MaxEntrypointBytes], "\n")
		if cut > 0 {
			result = result[:cut]
		} else {
			result = result[:MaxEntrypointBytes]
		}
	}
	return result
}
