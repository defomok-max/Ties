// Package skill discovers and loads SKILL.md files. Skills are reusable units
// of knowledge: a YAML frontmatter (name + description) plus a markdown body.
// The agent is shown the name+description of every skill and can load a full
// body on demand (progressive disclosure).
package skill

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is a single loaded SKILL.md.
type Skill struct {
	Name        string
	Description string
	Path        string
	Body        string
}

// Load reads and parses a single SKILL.md file.
func Load(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name, desc, body := parse(string(data))
	if name == "" {
		// Fall back to the parent directory name.
		name = filepath.Base(filepath.Dir(path))
	}
	return &Skill{Name: name, Description: desc, Path: path, Body: body}, nil
}

// parse splits frontmatter (between leading --- fences) from the body and
// extracts name/description keys.
func parse(content string) (name, desc, body string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", "", content
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", content
	}
	front := rest[:end]
	body = strings.TrimPrefix(rest[end+len("\n---"):], "\n")
	for _, line := range strings.Split(front, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		switch strings.ToLower(key) {
		case "name":
			name = val
		case "description":
			desc = val
		}
	}
	return name, desc, strings.TrimSpace(body)
}

// Discover scans the given directories for skills. It looks for both
// "<dir>/<name>/SKILL.md" and "<dir>/SKILL.md" layouts. Results are sorted by
// name and de-duplicated (first occurrence wins).
func Discover(dirs []string) []*Skill {
	seen := map[string]bool{}
	var skills []*Skill
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.EqualFold(d.Name(), "SKILL.md") {
				return nil
			}
			s, lerr := Load(path)
			if lerr != nil || seen[s.Name] {
				return nil
			}
			seen[s.Name] = true
			skills = append(skills, s)
			return nil
		})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills
}

// DefaultDirs returns the standard skill search paths for a workspace root.
func DefaultDirs(root string, extra []string) []string {
	dirs := []string{
		filepath.Join(root, ".ties", "skills"),
		filepath.Join(root, "skills"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "ties", "skills"))
	}
	return append(dirs, extra...)
}

// Catalog renders a compact "name: description" list for the system prompt.
func Catalog(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range skills {
		b.WriteString("- ")
		b.WriteString(s.Name)
		if s.Description != "" {
			b.WriteString(": ")
			b.WriteString(s.Description)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
