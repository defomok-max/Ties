// Package memory discovers project-context documents — the AGENTS.md /
// CLAUDE.md / TIES.md files that Claude Code, OpenCode and Codex use to carry
// persistent, repo-specific instructions into every run. The collected text is
// injected into the system prompt so the agent always sees a project's
// conventions, build commands and house rules.
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// names are the recognised context filenames, checked in this order within a
// directory. The first existing one per directory wins (so a repo can ship a
// single AGENTS.md without it being duplicated by a CLAUDE.md symlink).
var names = []string{"AGENTS.md", "CLAUDE.md", "TIES.md"}

// maxBytes caps how much of a single document is injected, so one giant file
// can't blow the model's context window.
const maxBytes = 16000

// Doc is a discovered context document.
type Doc struct {
	// Path is the absolute path the document was read from.
	Path string
	// Content is its (possibly truncated) trimmed body.
	Content string
}

// Collect gathers context documents for a run. Precedence runs from lowest to
// highest: an optional global document (globalDir, e.g. ~/.config/ties) first,
// then every ancestor directory from the filesystem root down to dir, so the
// nearest file is appended last and therefore "wins" when the model reads them
// top to bottom. Each physical file is included at most once.
func Collect(dir, globalDir string) []Doc {
	var docs []Doc
	seen := map[string]bool{}

	add := func(candidate string) {
		abs, err := filepath.Abs(candidate)
		if err != nil || seen[abs] {
			return
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return
		}
		body := strings.TrimSpace(string(data))
		if body == "" {
			return
		}
		if len(body) > maxBytes {
			body = body[:maxBytes] + "\n…(truncated)"
		}
		seen[abs] = true
		docs = append(docs, Doc{Path: abs, Content: body})
	}

	addDir := func(d string) {
		for _, n := range names {
			before := len(docs)
			add(filepath.Join(d, n))
			if len(docs) > before {
				return // first match in this dir wins
			}
		}
	}

	if globalDir != "" {
		addDir(globalDir)
	}

	// Walk up collecting ancestor dirs, then add them outermost-first.
	var dirs []string
	if cur, err := filepath.Abs(dir); err == nil {
		for {
			dirs = append(dirs, cur)
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		addDir(dirs[i])
	}
	return docs
}

// Render formats discovered docs into a single block for the system prompt.
func Render(docs []Doc) string {
	if len(docs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, d := range docs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "----- %s -----\n%s", d.Path, d.Content)
	}
	return b.String()
}
