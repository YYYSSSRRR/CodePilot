package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Info holds parsed metadata for a skill.
type Info struct {
	Name        string
	Description string
	WhenToUse   string
	Dir         string
	Path        string
}

// Loaded holds the full loaded skill content with metadata.
type Loaded struct {
	Info
	Content string
}

// ParseInfo extracts name, description, and when_to_use from the SKILL.md frontmatter.
// Frontmatter is a YAML-like block between "---" delimiters at the start of the file.
func parseInfo(path, dir string) (Info, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}, fmt.Errorf("read %s: %w", path, err)
	}

	info := Info{
		Dir:  dir,
		Path: path,
	}

	content := string(data)
	if !strings.HasPrefix(content, "---") {
		// No frontmatter — use filename as name
		info.Name = nameFromDir(dir)
		info.Description = strings.TrimSpace(content)
		if len(info.Description) > 200 {
			info.Description = info.Description[:200]
		}
		return info, nil
	}

	// Find closing ---
	rest := content[3:]
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		info.Name = nameFromDir(dir)
		return info, nil
	}

	fm := rest[:endIdx]
	lines := strings.Split(fm, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "name:"):
			info.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		case strings.HasPrefix(line, "description:"):
			info.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		case strings.HasPrefix(line, "when_to_use:"):
			info.WhenToUse = strings.TrimSpace(strings.TrimPrefix(line, "when_to_use:"))
		}
	}

	if info.Name == "" {
		info.Name = nameFromDir(dir)
	}
	return info, nil
}

func nameFromDir(dir string) string {
	return strings.TrimSuffix(filepath.Base(dir), ".skill")
}

// FindDir finds the skill directory from the skills root by name.
func FindDir(root, name string) string {
	// Direct match: root/<name>/SKILL.md
	direct := filepath.Join(root, name)
	if info, err := os.Stat(filepath.Join(direct, "SKILL.md")); err == nil && !info.IsDir() {
		return direct
	}

	// Try root/<name>.skill/SKILL.md
	withSuffix := filepath.Join(root, name+".skill")
	if info, err := os.Stat(filepath.Join(withSuffix, "SKILL.md")); err == nil && !info.IsDir() {
		return withSuffix
	}

	// Search all subdirectories for a matching name
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == name || entry.Name() == name+".skill" {
			return filepath.Join(root, entry.Name())
		}
		// Check SKILL.md inside this dir
		skillPath := filepath.Join(root, entry.Name(), "SKILL.md")
		if info, err := os.Stat(skillPath); err == nil && !info.IsDir() {
			parsed, err := parseInfo(skillPath, filepath.Join(root, entry.Name()))
			if err == nil && parsed.Name == name {
				return filepath.Join(root, entry.Name())
			}
		}
	}
	return ""
}

// Load reads the full SKILL.md content from a skill directory.
func Load(dir string) (Loaded, error) {
	path := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return Loaded{}, fmt.Errorf("read skill: %w", err)
	}

	content := string(data)

	// Strip frontmatter
	if strings.HasPrefix(content, "---") {
		rest := content[3:]
		if endIdx := strings.Index(rest, "\n---"); endIdx >= 0 {
			content = rest[endIdx+5:] // skip past ---\n
		}
	}

	info, err := parseInfo(path, dir)
	if err != nil {
		return Loaded{}, err
	}

	return Loaded{
		Info:    info,
		Content: strings.TrimSpace(content),
	}, nil
}
