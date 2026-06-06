package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// treeNoise are directory names skipped by default (unless `all` is set).
var treeNoise = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".ties": true,
	".idea": true, ".vscode": true, "dist": true, "build": true,
	"__pycache__": true, ".venv": true, "target": true,
}

const treeMaxEntries = 2000

type treeTool struct{ root string }

func newTreeTool(root string) Tool { return &treeTool{root: root} }
func (t *treeTool) Name() string   { return "tree" }
func (t *treeTool) Description() string {
	return "Print a directory tree (structure only) up to a depth, skipping noise like .git and node_modules. Use to get a quick map of a project before reading files."
}
func (t *treeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Directory to walk (default: workspace root)"},"depth":{"type":"integer","description":"Maximum depth to descend (default 3)"},"all":{"type":"boolean","description":"Include hidden files and noise dirs (.git, node_modules, …)"}},"required":[]}`)
}

func (t *treeTool) Run(_ context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Path  string `json:"path"`
		Depth int    `json:"depth"`
		All   bool   `json:"all"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	start := a.Path
	if strings.TrimSpace(start) == "" {
		start = "."
	}
	root, err := confine(t.root, start)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	info, err := os.Stat(root)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	if !info.IsDir() {
		return Result{Content: start + " is not a directory", IsError: true}, nil
	}
	depth := a.Depth
	if depth <= 0 {
		depth = 3
	}
	if depth > 12 {
		depth = 12
	}

	var b strings.Builder
	b.WriteString(filepath.Base(root))
	b.WriteString("/\n")
	count := 0
	truncated := t.walk(&b, root, "", depth, a.All, &count)
	if truncated {
		b.WriteString("… (truncated)\n")
	}
	return Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}

// walk appends the tree under dir; it returns true if the entry cap was hit.
func (t *treeTool) walk(b *strings.Builder, dir, prefix string, depth int, all bool, count *int) bool {
	if depth <= 0 {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	// Keep visible entries; dirs first, then files, each alphabetical.
	kept := entries[:0]
	for _, e := range entries {
		name := e.Name()
		if !all {
			if strings.HasPrefix(name, ".") || treeNoise[name] {
				continue
			}
		}
		kept = append(kept, e)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		di, dj := kept[i].IsDir(), kept[j].IsDir()
		if di != dj {
			return di
		}
		return kept[i].Name() < kept[j].Name()
	})

	for i, e := range kept {
		if *count >= treeMaxEntries {
			return true
		}
		*count++
		last := i == len(kept)-1
		branch, nextPrefix := "├── ", prefix+"│   "
		if last {
			branch, nextPrefix = "└── ", prefix+"    "
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(prefix + branch + name + "\n")
		if e.IsDir() {
			if t.walk(b, filepath.Join(dir, e.Name()), nextPrefix, depth-1, all, count) {
				return true
			}
		}
	}
	return false
}
