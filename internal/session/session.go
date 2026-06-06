// Package session persists conversation transcripts as append-only JSONL
// files, supporting create, resume, list and show.
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/defomok-max/Ties/internal/provider"
)

// Meta describes a session without its full transcript.
type Meta struct {
	ID      string    `json:"id"`
	Model   string    `json:"model"`
	Title   string    `json:"title,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// record is one JSONL line.
type record struct {
	Type    string            `json:"type"` // "meta" | "message"
	Time    time.Time         `json:"time"`
	Meta    *Meta             `json:"meta,omitempty"`
	Message *provider.Message `json:"message,omitempty"`
}

// Session is an open transcript that can be appended to.
type Session struct {
	Meta     Meta
	Messages []provider.Message
	path     string
	f        *os.File
	w        *bufio.Writer
}

// Store manages session files under a directory.
type Store struct{ dir string }

// NewStore returns a Store rooted at dir, creating it if needed.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) pathFor(id string) string { return filepath.Join(s.dir, id+".jsonl") }

// NewID generates a time-sortable session id.
func NewID() string { return time.Now().UTC().Format("20060102-150405") }

// Create starts a new session for the given model.
func (s *Store) Create(model string) (*Session, error) {
	id := NewID()
	now := time.Now().UTC()
	sess := &Session{
		Meta: Meta{ID: id, Model: model, Created: now, Updated: now},
		path: s.pathFor(id),
	}
	f, err := os.OpenFile(sess.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	sess.f = f
	sess.w = bufio.NewWriter(f)
	if err := sess.writeRecord(record{Type: "meta", Time: now, Meta: &sess.Meta}); err != nil {
		_ = f.Close()
		return nil, err
	}
	return sess, nil
}

// Open resumes an existing session by id, loading its transcript.
func (s *Store) Open(id string) (*Session, error) {
	path := s.pathFor(id)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sess := &Session{path: path, Meta: Meta{ID: id}}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		switch rec.Type {
		case "meta":
			if rec.Meta != nil {
				sess.Meta = *rec.Meta
			}
		case "message":
			if rec.Message != nil {
				sess.Messages = append(sess.Messages, *rec.Message)
			}
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	sess.f = f
	sess.w = bufio.NewWriter(f)
	return sess, nil
}

// List returns metadata for all sessions, newest first.
func (s *Store) List() ([]Meta, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var metas []Meta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		if m, err := s.readMeta(id); err == nil {
			metas = append(metas, m)
		}
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Created.After(metas[j].Created) })
	return metas, nil
}

func (s *Store) readMeta(id string) (Meta, error) {
	f, err := os.Open(s.pathFor(id))
	if err != nil {
		return Meta{}, err
	}
	defer func() { _ = f.Close() }()
	meta := Meta{ID: id}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var rec record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.Type == "meta" && rec.Meta != nil {
			meta = *rec.Meta
		}
		if rec.Type == "message" {
			meta.Updated = rec.Time
		}
	}
	return meta, nil
}

// Append records a message both in memory and on disk.
func (s *Session) Append(m provider.Message) error {
	s.Messages = append(s.Messages, m)
	s.Meta.Updated = time.Now().UTC()
	return s.writeRecord(record{Type: "message", Time: s.Meta.Updated, Message: &m})
}

func (s *Session) writeRecord(rec record) error {
	if s.w == nil {
		return fmt.Errorf("session is not open for writing")
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := s.w.Write(append(data, '\n')); err != nil {
		return err
	}
	return s.w.Flush()
}

// Close flushes and closes the underlying file.
func (s *Session) Close() error {
	if s.w != nil {
		_ = s.w.Flush()
	}
	if s.f != nil {
		return s.f.Close()
	}
	return nil
}

// Render returns a human-readable transcript for `ties session show`.
func (s *Session) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Session %s (model %s)\n", s.Meta.ID, s.Meta.Model)
	for _, m := range s.Messages {
		switch m.Role {
		case provider.RoleUser:
			fmt.Fprintf(&b, "\n> %s\n", m.Content)
		case provider.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "%s\n", m.Content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "  · %s(%s)\n", tc.Name, string(tc.Arguments))
			}
		case provider.RoleTool:
			fmt.Fprintf(&b, "  ← %s\n", truncate(m.Content, 200))
		}
	}
	return b.String()
}

// Export renders the full transcript in a shareable format: "md" (Markdown,
// the default) or "html" (a standalone, styled page).
func (s *Session) Export(format string) (string, error) {
	switch format {
	case "", "md", "markdown":
		return s.exportMarkdown(), nil
	case "html":
		return s.exportHTML(), nil
	default:
		return "", fmt.Errorf("unknown export format %q (want md or html)", format)
	}
}

func (s *Session) exportMarkdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Ties session `%s`\n\n", s.Meta.ID)
	fmt.Fprintf(&b, "- **Model:** %s\n- **Created:** %s\n\n---\n\n",
		s.Meta.Model, s.Meta.Created.Format("2006-01-02 15:04 MST"))
	for _, m := range s.Messages {
		switch m.Role {
		case provider.RoleUser:
			fmt.Fprintf(&b, "### 🧑 User\n\n%s\n\n", m.Content)
		case provider.RoleAssistant:
			b.WriteString("### 🤖 Assistant\n\n")
			if strings.TrimSpace(m.Content) != "" {
				fmt.Fprintf(&b, "%s\n\n", m.Content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "```tool\n%s(%s)\n```\n\n", tc.Name, string(tc.Arguments))
			}
		case provider.RoleTool:
			tag := "result"
			if m.IsError {
				tag = "error"
			}
			fmt.Fprintf(&b, "<details><summary>↩︎ tool %s</summary>\n\n```\n%s\n```\n\n</details>\n\n", tag, m.Content)
		}
	}
	return b.String()
}

func (s *Session) exportHTML() string {
	var b strings.Builder
	b.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\">")
	fmt.Fprintf(&b, "<title>Ties session %s</title>", htmlEscape(s.Meta.ID))
	b.WriteString("<style>" + exportCSS + "</style></head><body><main>")
	fmt.Fprintf(&b, "<h1>Ties session <code>%s</code></h1>", htmlEscape(s.Meta.ID))
	fmt.Fprintf(&b, "<p class=\"meta\">Model <b>%s</b> · %s</p>",
		htmlEscape(s.Meta.Model), htmlEscape(s.Meta.Created.Format("2006-01-02 15:04 MST")))
	for _, m := range s.Messages {
		switch m.Role {
		case provider.RoleUser:
			fmt.Fprintf(&b, "<div class=\"turn user\"><div class=\"who\">🧑 User</div><pre>%s</pre></div>", htmlEscape(m.Content))
		case provider.RoleAssistant:
			b.WriteString("<div class=\"turn assistant\"><div class=\"who\">🤖 Assistant</div>")
			if strings.TrimSpace(m.Content) != "" {
				fmt.Fprintf(&b, "<pre>%s</pre>", htmlEscape(m.Content))
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "<pre class=\"tool\">%s(%s)</pre>",
					htmlEscape(tc.Name), htmlEscape(string(tc.Arguments)))
			}
			b.WriteString("</div>")
		case provider.RoleTool:
			cls := "toolresult"
			if m.IsError {
				cls += " error"
			}
			fmt.Fprintf(&b, "<div class=\"turn %s\"><pre>%s</pre></div>", cls, htmlEscape(m.Content))
		}
	}
	b.WriteString("</main></body></html>")
	return b.String()
}

const exportCSS = `body{background:#0d1117;color:#c9d1d9;font:15px/1.6 -apple-system,Segoe UI,Roboto,sans-serif;margin:0}` +
	`main{max-width:820px;margin:0 auto;padding:32px 20px}` +
	`h1{font-size:20px}code{background:#161b22;padding:1px 5px;border-radius:5px}` +
	`.meta{color:#8b949e;margin-bottom:24px}` +
	`.turn{border:1px solid #21262d;border-radius:10px;padding:12px 16px;margin:14px 0;background:#0f141a}` +
	`.who{font-weight:600;margin-bottom:6px}` +
	`.turn.user{background:#10171f}` +
	`pre{white-space:pre-wrap;word-wrap:break-word;margin:6px 0;font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace}` +
	`pre.tool{color:#79c0ff;background:#161b22;padding:8px 10px;border-radius:6px}` +
	`.toolresult pre{color:#8b949e}.toolresult.error pre{color:#ff7b72}`

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
