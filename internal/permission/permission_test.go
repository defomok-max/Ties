package permission

import "testing"

func TestEvaluate(t *testing.T) {
	e := New(map[string]string{
		"*":             "ask",
		"read":          "allow",
		"bash":          "ask",
		"bash:rm *":     "deny",
		"write:**/*.go": "allow",
	})
	cases := []struct {
		tool, target string
		want         Decision
	}{
		{"read", "", Allow},
		{"bash", "ls -la", Ask},
		{"bash", "rm -rf /", Deny},
		{"write", "main.go", Allow},
		{"write", "notes.txt", Ask}, // falls back to "*"
		{"unknown", "", Ask},        // "*" applies
	}
	for _, c := range cases {
		if got := e.Evaluate(c.tool, c.target); got != c.want {
			t.Errorf("Evaluate(%q,%q)=%q want %q", c.tool, c.target, got, c.want)
		}
	}
}

func TestDenyWins(t *testing.T) {
	e := New(map[string]string{
		"bash":           "allow",
		"bash:git push*": "deny",
	})
	if got := e.Evaluate("bash", "git push origin main"); got != Deny {
		t.Fatalf("expected deny, got %q", got)
	}
	if got := e.Evaluate("bash", "git status"); got != Allow {
		t.Fatalf("expected allow, got %q", got)
	}
}

func TestWildcard(t *testing.T) {
	if !wildcard("rm *", "rm -rf /") {
		t.Error("rm * should match rm -rf /")
	}
	if wildcard("git *", "ls") {
		t.Error("git * should not match ls")
	}
	if !wildcard("**/*.go", "internal/cli/run.go") {
		t.Error("**/*.go should match nested go file")
	}
}
