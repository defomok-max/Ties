package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// confine resolves p relative to root and guarantees the result stays inside
// root, defeating path-traversal via "..".
func confine(root, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := p
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRoot, target)
	}
	target = filepath.Clean(target)
	rel, err := filepath.Rel(absRoot, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the workspace root", p)
	}
	return target, nil
}

// ---- read ----

type readTool struct{ root string }

func newReadTool(root string) Tool { return &readTool{root: root} }
func (t *readTool) Name() string   { return "read" }
func (t *readTool) Description() string {
	return "Read a UTF-8 text file from the workspace and return its contents. Use before editing."
}
func (t *readTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path relative to the workspace root"}},"required":["path"]}`)
}
func (t *readTool) Run(_ context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	p, err := confine(t.root, a.Path)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	return Result{Content: string(data)}, nil
}

// ---- write ----

type writeTool struct{ root string }

func newWriteTool(root string) Tool { return &writeTool{root: root} }
func (t *writeTool) Name() string   { return "write" }
func (t *writeTool) Description() string {
	return "Create or overwrite a file with the given content. Parent directories are created as needed."
}
func (t *writeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)
}
func (t *writeTool) Run(_ context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	p, err := confine(t.root, a.Path)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	return Result{Content: fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path)}, nil
}

// ---- edit ----

type editTool struct{ root string }

func newEditTool(root string) Tool { return &editTool{root: root} }
func (t *editTool) Name() string   { return "edit" }
func (t *editTool) Description() string {
	return "Replace the first (or all) occurrence(s) of an exact string in a file. The old string must match exactly."
}
func (t *editTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old":{"type":"string","description":"Exact text to find"},"new":{"type":"string","description":"Replacement text"},"all":{"type":"boolean","description":"Replace all occurrences (default false)"}},"required":["path","old","new"]}`)
}
func (t *editTool) Run(_ context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
		All  bool   `json:"all"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	p, err := confine(t.root, a.Path)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	content := string(data)
	count := strings.Count(content, a.Old)
	if count == 0 {
		return Result{Content: "old string not found", IsError: true}, nil
	}
	var updated string
	if a.All {
		updated = strings.ReplaceAll(content, a.Old, a.New)
	} else {
		if count > 1 {
			return Result{Content: fmt.Sprintf("old string is not unique (%d matches); pass all=true or add more context", count), IsError: true}, nil
		}
		updated = strings.Replace(content, a.Old, a.New, 1)
	}
	if err := os.WriteFile(p, []byte(updated), 0o644); err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	return Result{Content: fmt.Sprintf("edited %s (%d replacement(s))", a.Path, count)}, nil
}

// ---- list ----

type listTool struct{ root string }

func newListTool(root string) Tool      { return &listTool{root: root} }
func (t *listTool) Name() string        { return "list" }
func (t *listTool) Description() string { return "List the entries of a directory in the workspace." }
func (t *listTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Directory path (default: workspace root)"}}}`)
}
func (t *listTool) Run(_ context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	if a.Path == "" {
		a.Path = "."
	}
	p, err := confine(t.root, a.Path)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name)
		b.WriteByte('\n')
	}
	return Result{Content: b.String()}, nil
}

// ---- glob ----

type globTool struct{ root string }

func newGlobTool(root string) Tool      { return &globTool{root: root} }
func (t *globTool) Name() string        { return "glob" }
func (t *globTool) Description() string { return "Find files matching a glob pattern (e.g. **/*.go)." }
func (t *globTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern; ** matches across directories"}},"required":["pattern"]}`)
}
func (t *globTool) Run(_ context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Pattern string `json:"pattern"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	if a.Pattern == "" {
		return Result{Content: "pattern is required", IsError: true}, nil
	}
	absRoot, _ := filepath.Abs(t.root)
	var matches []string
	err := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(absRoot, path)
		rel = filepath.ToSlash(rel)
		if matchGlob(a.Pattern, rel) {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return Result{Content: "(no matches)"}, nil
	}
	return Result{Content: strings.Join(matches, "\n")}, nil
}

// matchGlob supports ** (any path segments) in addition to standard glob.
func matchGlob(pattern, name string) bool {
	if !strings.Contains(pattern, "**") {
		ok, _ := filepath.Match(pattern, name)
		if ok {
			return true
		}
		// Also try matching just the base name for convenience.
		ok, _ = filepath.Match(pattern, filepath.Base(name))
		return ok
	}
	// Translate ** into a regexp-free segment walk.
	return globStar(pattern, name)
}

func globStar(pattern, name string) bool {
	pParts := strings.Split(pattern, "/")
	nParts := strings.Split(name, "/")
	return matchParts(pParts, nParts)
}

func matchParts(p, n []string) bool {
	if len(p) == 0 {
		return len(n) == 0
	}
	if p[0] == "**" {
		// ** matches zero or more segments.
		for i := 0; i <= len(n); i++ {
			if matchParts(p[1:], n[i:]) {
				return true
			}
		}
		return false
	}
	if len(n) == 0 {
		return false
	}
	if ok, _ := filepath.Match(p[0], n[0]); !ok {
		return false
	}
	return matchParts(p[1:], n[1:])
}

// ---- grep ----

type grepTool struct{ root string }

func newGrepTool(root string) Tool { return &grepTool{root: root} }
func (t *grepTool) Name() string   { return "grep" }
func (t *grepTool) Description() string {
	return "Search file contents for a substring and return matching lines with file:line prefixes."
}
func (t *grepTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"glob":{"type":"string","description":"Optional glob to restrict files"}},"required":["query"]}`)
}
func (t *grepTool) Run(_ context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Query string `json:"query"`
		Glob  string `json:"glob"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	if a.Query == "" {
		return Result{Content: "query is required", IsError: true}, nil
	}
	absRoot, _ := filepath.Abs(t.root)
	var out []string
	const maxHits = 200
	err := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(absRoot, path)
		rel = filepath.ToSlash(rel)
		if a.Glob != "" && !matchGlob(a.Glob, rel) {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, a.Query) {
				out = append(out, fmt.Sprintf("%s:%d:%s", rel, i+1, line))
				if len(out) >= maxHits {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	if len(out) == 0 {
		return Result{Content: "(no matches)"}, nil
	}
	return Result{Content: strings.Join(out, "\n")}, nil
}
