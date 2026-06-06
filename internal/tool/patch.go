package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ---- multiedit ----

type multieditTool struct{ root string }

func newMultieditTool(root string) Tool { return &multieditTool{root: root} }
func (t *multieditTool) Name() string   { return "multiedit" }
func (t *multieditTool) Description() string {
	return "Apply several exact-string replacements to a single file atomically (all must apply or none are written). Edits are applied in order."
}
func (t *multieditTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"edits":{"type":"array","items":{"type":"object","properties":{"old":{"type":"string"},"new":{"type":"string"},"all":{"type":"boolean"}},"required":["old","new"]}}},"required":["path","edits"]}`)
}
func (t *multieditTool) Run(_ context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Path  string `json:"path"`
		Edits []struct {
			Old string `json:"old"`
			New string `json:"new"`
			All bool   `json:"all"`
		} `json:"edits"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	if len(a.Edits) == 0 {
		return Result{Content: "no edits provided", IsError: true}, nil
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
	total := 0
	for i, e := range a.Edits {
		count := strings.Count(content, e.Old)
		if count == 0 {
			return Result{Content: fmt.Sprintf("edit %d: old string not found (no changes written)", i+1), IsError: true}, nil
		}
		if e.All {
			content = strings.ReplaceAll(content, e.Old, e.New)
			total += count
		} else {
			if count > 1 {
				return Result{Content: fmt.Sprintf("edit %d: old string is not unique (%d matches); set all=true or add context", i+1, count), IsError: true}, nil
			}
			content = strings.Replace(content, e.Old, e.New, 1)
			total++
		}
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	return Result{Content: fmt.Sprintf("applied %d edit(s) to %s (%d replacement(s))", len(a.Edits), a.Path, total)}, nil
}

// ---- patch (unified diff) ----

type patchTool struct{ root string }

func newPatchTool(root string) Tool { return &patchTool{root: root} }
func (t *patchTool) Name() string   { return "patch" }
func (t *patchTool) Description() string {
	return "Apply a unified diff (as produced by `git diff` / `diff -u`) to one or more files in the workspace. Hunks must match the current file content."
}
func (t *patchTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"diff":{"type":"string","description":"A unified diff with --- / +++ headers and @@ hunks"}},"required":["diff"]}`)
}
func (t *patchTool) Run(_ context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Diff string `json:"diff"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(a.Diff) == "" {
		return Result{Content: "diff is required", IsError: true}, nil
	}
	files, err := parseUnifiedDiff(a.Diff)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	if len(files) == 0 {
		return Result{Content: "no file hunks found in diff", IsError: true}, nil
	}

	// Validate + compute all results first so the patch is all-or-nothing.
	type plan struct {
		path    string
		newText string
		create  bool
		remove  bool
	}
	var plans []plan
	for _, fp := range files {
		target := fp.newPath
		if target == "" || target == "/dev/null" {
			target = fp.oldPath
		}
		abs, err := confine(t.root, stripPrefix(target))
		if err != nil {
			return Result{Content: err.Error(), IsError: true}, nil
		}
		if fp.newPath == "/dev/null" { // deletion
			plans = append(plans, plan{path: abs, remove: true})
			continue
		}
		var orig string
		create := fp.oldPath == "/dev/null"
		if !create {
			b, err := os.ReadFile(abs)
			if err != nil {
				return Result{Content: fmt.Sprintf("%s: %v", target, err), IsError: true}, nil
			}
			orig = string(b)
		}
		out, err := applyHunks(orig, fp.hunks)
		if err != nil {
			return Result{Content: fmt.Sprintf("%s: %v", target, err), IsError: true}, nil
		}
		plans = append(plans, plan{path: abs, newText: out, create: create})
	}

	// Commit.
	changed := make([]string, 0, len(plans))
	for _, pl := range plans {
		switch {
		case pl.remove:
			if err := os.Remove(pl.path); err != nil && !os.IsNotExist(err) {
				return Result{Content: err.Error(), IsError: true}, nil
			}
		default:
			if err := os.WriteFile(pl.path, []byte(pl.newText), 0o644); err != nil {
				return Result{Content: err.Error(), IsError: true}, nil
			}
		}
		changed = append(changed, relTo(t.root, pl.path))
	}
	return Result{Content: fmt.Sprintf("patched %d file(s): %s", len(changed), strings.Join(changed, ", "))}, nil
}

type diffHunk struct {
	oldStart int
	lines    []string // each begins with ' ', '-' or '+'
}

type diffFile struct {
	oldPath string
	newPath string
	hunks   []diffHunk
}

func parseUnifiedDiff(diff string) ([]diffFile, error) {
	lines := strings.Split(diff, "\n")
	var files []diffFile
	var cur *diffFile
	var hunk *diffHunk
	flush := func() {
		if cur != nil && hunk != nil {
			cur.hunks = append(cur.hunks, *hunk)
			hunk = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		switch {
		case strings.HasPrefix(ln, "diff "):
			flush()
			if cur != nil {
				files = append(files, *cur)
			}
			cur = &diffFile{}
		case strings.HasPrefix(ln, "--- "):
			if cur == nil {
				cur = &diffFile{}
			} else if len(cur.hunks) > 0 || hunk != nil {
				flush()
				files = append(files, *cur)
				cur = &diffFile{}
			}
			cur.oldPath = strings.TrimSpace(strings.TrimPrefix(ln, "--- "))
		case strings.HasPrefix(ln, "+++ "):
			if cur == nil {
				cur = &diffFile{}
			}
			cur.newPath = strings.TrimSpace(strings.TrimPrefix(ln, "+++ "))
		case strings.HasPrefix(ln, "@@"):
			flush()
			h, err := parseHunkHeader(ln)
			if err != nil {
				return nil, err
			}
			hunk = &h
		case hunk != nil && (strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "-") || strings.HasPrefix(ln, "+")):
			// A blank context line is encoded as a single space (" "); a truly
			// empty string is the trailing-newline artifact and is ignored.
			hunk.lines = append(hunk.lines, ln)
		}
		// Any other line (e.g. "\ No newline at end of file", index/mode
		// headers) is ignored.
	}
	flush()
	if cur != nil {
		files = append(files, *cur)
	}
	// Drop files without hunks (pure rename/mode headers we don't model).
	out := files[:0]
	for _, f := range files {
		if len(f.hunks) > 0 {
			out = append(out, f)
		}
	}
	return out, nil
}

func parseHunkHeader(ln string) (diffHunk, error) {
	// Format: @@ -l,s +l,s @@ optional
	body := strings.TrimPrefix(ln, "@@")
	if i := strings.Index(body, "@@"); i >= 0 {
		body = body[:i]
	}
	body = strings.TrimSpace(body)
	var oldStart int
	for _, part := range strings.Fields(body) {
		if strings.HasPrefix(part, "-") {
			seg := strings.TrimPrefix(part, "-")
			if c := strings.IndexByte(seg, ','); c >= 0 {
				seg = seg[:c]
			}
			n := 0
			_, err := fmt.Sscanf(seg, "%d", &n)
			if err != nil {
				return diffHunk{}, fmt.Errorf("bad hunk header %q", ln)
			}
			oldStart = n
		}
	}
	return diffHunk{oldStart: oldStart}, nil
}

// applyHunks applies hunks to orig. It locates each hunk by matching its
// context+deletion lines, allowing small line-number drift.
func applyHunks(orig string, hunks []diffHunk) (string, error) {
	hadTrailingNL := strings.HasSuffix(orig, "\n")
	var src []string
	if orig == "" {
		src = []string{}
	} else {
		src = strings.Split(strings.TrimSuffix(orig, "\n"), "\n")
	}

	for _, h := range hunks {
		var want []string // context + removed lines, in order
		for _, l := range h.lines {
			if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "-") {
				want = append(want, l[1:])
			}
		}
		pos := findBlock(src, want, h.oldStart-1)
		if pos < 0 {
			return "", fmt.Errorf("hunk does not apply (context not found near line %d)", h.oldStart)
		}
		// Build replacement: context + added lines.
		var repl []string
		for _, l := range h.lines {
			switch {
			case strings.HasPrefix(l, " "):
				repl = append(repl, l[1:])
			case strings.HasPrefix(l, "+"):
				repl = append(repl, l[1:])
			}
		}
		next := append([]string{}, src[:pos]...)
		next = append(next, repl...)
		next = append(next, src[pos+len(want):]...)
		src = next
	}
	out := strings.Join(src, "\n")
	if hadTrailingNL || orig == "" {
		out += "\n"
	}
	return out, nil
}

// findBlock returns the index in src where want matches, preferring near hint.
func findBlock(src, want []string, hint int) int {
	if len(want) == 0 {
		if hint < 0 {
			return 0
		}
		if hint > len(src) {
			return len(src)
		}
		return hint
	}
	match := func(at int) bool {
		if at < 0 || at+len(want) > len(src) {
			return false
		}
		for i, w := range want {
			if src[at+i] != w {
				return false
			}
		}
		return true
	}
	if match(hint) {
		return hint
	}
	for d := 1; d < len(src); d++ {
		if match(hint - d) {
			return hint - d
		}
		if match(hint + d) {
			return hint + d
		}
	}
	for i := range src {
		if match(i) {
			return i
		}
	}
	return -1
}

func stripPrefix(p string) string {
	for _, pre := range []string{"a/", "b/", "./"} {
		if strings.HasPrefix(p, pre) {
			return p[len(pre):]
		}
	}
	return p
}

func relTo(root, abs string) string {
	if absRoot, err := filepath.Abs(root); err == nil {
		if r, err := filepath.Rel(absRoot, abs); err == nil {
			return r
		}
	}
	return abs
}
