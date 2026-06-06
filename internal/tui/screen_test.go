package tui

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/defomok-max/Ties/internal/ui"
)

// syncBuf is a tiny concurrency-safe writer so the spinner goroutine and the
// test goroutine can both write without tripping the race detector.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestScreenLifecycle(t *testing.T) {
	t.Setenv("COLUMNS", "50")
	t.Setenv("LINES", "14")
	out := &syncBuf{}
	s := NewScreen(out, nil, ui.ResolveTheme("dark"), true)
	s.Model().SetMeta("test-model", "sess-abc")

	s.Start()
	s.Update(func(m *Model) {
		m.AddUser("hello")
		m.AppendAssistant("world")
		m.EndAssistant()
	})
	s.Stop()

	got := out.String()
	if !strings.Contains(got, altScreenOn) {
		t.Error("missing alt-screen enable sequence")
	}
	if !strings.Contains(got, altScreenOff) {
		t.Error("missing alt-screen disable sequence")
	}
	if !strings.Contains(got, hideCursor) || !strings.Contains(got, showCursor) {
		t.Error("cursor was not hidden/restored")
	}
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Error("transcript content not rendered")
	}
	if !strings.Contains(got, "test-model") {
		t.Error("header model not rendered")
	}
}

func TestScreenStartIsIdempotent(t *testing.T) {
	out := &syncBuf{}
	s := NewScreen(out, nil, ui.ResolveTheme("mono"), false)
	s.Start()
	s.Start() // second call must be a no-op, not a panic or double goroutine
	s.Stop()
	s.Stop() // double stop is safe
}
