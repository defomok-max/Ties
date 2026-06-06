package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// mentionRe matches @path tokens: an @ at the start of input or after
// whitespace, followed by a non-whitespace path. Trailing punctuation is
// trimmed separately so "see @main.go." resolves to main.go.
var mentionRe = regexp.MustCompile(`(^|\s)@([^\s]+)`)

// maxMentionBytes caps how much of one referenced file is inlined.
const maxMentionBytes = 50000

// expandMentions scans input for @path references (a convenience borrowed from
// Claude Code / OpenCode) and appends the contents of each readable file that
// resolves inside root. The original text — including the @token — is left
// intact so the model still sees the reference; the file bodies are added as a
// trailing context block. Tokens that don't resolve to a file are ignored.
func expandMentions(root, input string) string {
	matches := mentionRe.FindAllStringSubmatch(input, -1)
	if len(matches) == 0 {
		return input
	}
	var blocks []string
	seen := map[string]bool{}
	for _, m := range matches {
		raw := strings.TrimRight(m[2], ".,;:!?)]}")
		if raw == "" || seen[raw] {
			continue
		}
		abs, err := confineRoot(root, raw)
		if err != nil {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		seen[raw] = true
		body := string(data)
		if len(body) > maxMentionBytes {
			body = body[:maxMentionBytes] + "\n…(truncated)"
		}
		blocks = append(blocks, fmt.Sprintf("----- %s -----\n%s", raw, body))
	}
	if len(blocks) == 0 {
		return input
	}
	return input + "\n\nReferenced files:\n" + strings.Join(blocks, "\n\n")
}

// confineRoot resolves p under root and rejects paths that escape it. It is a
// thin local copy of the tool package's confinement so the CLI need not import
// internals; symmetry with tool.confine is intentional.
func confineRoot(root, p string) (string, error) {
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
