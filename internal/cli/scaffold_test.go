package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defomok-max/Ties/internal/config"
)

func TestMCPAddStdioAndRemove(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := mcpAdd([]string{"fs", "--", "npx", "-y", "server-filesystem", "/tmp"}); err != nil {
		t.Fatalf("mcpAdd: %v", err)
	}
	cfg, err := loadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	srv, ok := cfg.MCP["fs"]
	if !ok {
		t.Fatal("server not saved")
	}
	if srv.Command != "npx" || len(srv.Args) != 3 || srv.Args[0] != "-y" {
		t.Fatalf("bad command/args: %+v", srv)
	}
	if srv.IsHTTP() {
		t.Fatal("stdio server should not be HTTP")
	}

	if err := mcpRemove([]string{"fs"}); err != nil {
		t.Fatalf("mcpRemove: %v", err)
	}
	cfg2, _ := loadGlobal()
	if _, ok := cfg2.MCP["fs"]; ok {
		t.Fatal("server not removed")
	}
	if err := mcpRemove([]string{"nope"}); err == nil {
		t.Fatal("removing unknown server should error")
	}
}

func TestMCPAddHTTP(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := mcpAdd([]string{"remote", "--url", "https://mcp.example.com/rpc", "--header", "Authorization:Bearer xyz"}); err != nil {
		t.Fatalf("mcpAdd http: %v", err)
	}
	cfg, _ := loadGlobal()
	srv := cfg.MCP["remote"]
	if !srv.IsHTTP() || srv.URL != "https://mcp.example.com/rpc" {
		t.Fatalf("bad http server: %+v", srv)
	}
	if srv.Headers["Authorization"] != "Bearer xyz" {
		t.Fatalf("header not saved: %+v", srv.Headers)
	}
}

func TestMCPAddValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := mcpAdd([]string{"x"}); err == nil {
		t.Fatal("expected error when neither url nor command given")
	}
}

func TestSkillAdd(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)

	if err := skillAdd([]string{"my-skill"}); err != nil {
		t.Fatalf("skillAdd: %v", err)
	}
	path := filepath.Join(dir, "skills", "my-skill", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("skill file not created: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "name: my-skill") || !strings.HasPrefix(body, "---\n") {
		t.Fatalf("bad frontmatter:\n%s", body)
	}

	// Refuses overwrite without --force.
	if err := skillAdd([]string{"my-skill"}); err == nil {
		t.Fatal("expected overwrite guard error")
	}
	if err := skillAdd([]string{"my-skill", "--force"}); err != nil {
		t.Fatalf("force overwrite: %v", err)
	}
	// Rejects bad names.
	if err := skillAdd([]string{"Bad Name"}); err == nil {
		t.Fatal("expected invalid name error")
	}
}

var _ = config.MCPServer{}

// withWorkdir chdirs into dir for the duration of the test.
func withWorkdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}
