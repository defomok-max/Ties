package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	content := "---\nname: demo\ndescription: A demo skill\n---\n# Body\nhello world"
	name, desc, body := parse(content)
	if name != "demo" {
		t.Errorf("name = %q", name)
	}
	if desc != "A demo skill" {
		t.Errorf("desc = %q", desc)
	}
	if body != "# Body\nhello world" {
		t.Errorf("body = %q", body)
	}
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	sd := filepath.Join(dir, "skills", "alpha")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"),
		[]byte("---\nname: alpha\ndescription: first\n---\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	skills := Discover([]string{filepath.Join(dir, "skills")})
	if len(skills) != 1 || skills[0].Name != "alpha" {
		t.Fatalf("discover = %+v", skills)
	}
	if skills[0].Description != "first" {
		t.Errorf("desc = %q", skills[0].Description)
	}
}
