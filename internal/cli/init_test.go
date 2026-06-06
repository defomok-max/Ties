package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuessCommands(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x"), 0o644); err != nil {
		t.Fatal(err)
	}
	build, test := guessCommands(dir)
	if build != "go build ./..." || test != "go test ./..." {
		t.Fatalf("go detection wrong: %q / %q", build, test)
	}

	empty := t.TempDir()
	b2, _ := guessCommands(empty)
	if !strings.Contains(b2, "_add") {
		t.Fatalf("unknown stack should use placeholder, got %q", b2)
	}
}

func TestScaffoldAgentsContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := scaffoldAgents(dir)
	if !strings.Contains(out, "# "+filepath.Base(dir)) {
		t.Errorf("missing project heading:\n%s", out)
	}
	if !strings.Contains(out, "npm test") {
		t.Errorf("should reflect node test command:\n%s", out)
	}
	for _, want := range []string{"## Overview", "## Commands", "## Conventions"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing section %q", want)
		}
	}
}
