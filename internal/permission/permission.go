// Package permission decides whether a tool invocation may proceed. Rules are
// keyed by "tool" or "tool:pattern" and map to allow | ask | deny. Deny always
// wins; otherwise the most specific matching rule applies.
package permission

import "strings"

// Decision is the result of evaluating a permission request.
type Decision string

// Possible decisions.
const (
	Allow Decision = "allow"
	Ask   Decision = "ask"
	Deny  Decision = "deny"
)

// Engine evaluates permission rules.
type Engine struct {
	rules map[string]Decision
}

// New builds an Engine from a raw rule map (values "allow"/"ask"/"deny").
// Unknown values are treated as "ask".
func New(rules map[string]string) *Engine {
	e := &Engine{rules: map[string]Decision{}}
	for k, v := range rules {
		e.rules[k] = normalize(v)
	}
	return e
}

func normalize(v string) Decision {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "allow":
		return Allow
	case "deny":
		return Deny
	default:
		return Ask
	}
}

// Evaluate returns the decision for invoking tool with the given target detail
// (a path or command, may be empty). Deny wins; otherwise the highest
// specificity match is used, with Ask preferred over Allow on ties.
func (e *Engine) Evaluate(tool, target string) Decision {
	bestSpec := -1
	best := Ask
	matchedAny := false

	consider := func(spec int, d Decision) {
		matchedAny = true
		if d == Deny {
			// Mark deny at max specificity so it always wins.
			if bestSpec <= 1000 {
				bestSpec = 1000
				best = Deny
			}
			return
		}
		if best == Deny {
			return
		}
		if spec > bestSpec || (spec == bestSpec && d == Ask) {
			bestSpec = spec
			best = d
		}
	}

	for key, dec := range e.rules {
		t, pat, hasPat := strings.Cut(key, ":")
		switch {
		case key == "*":
			consider(0, dec)
		case !hasPat && t == tool:
			consider(1, dec)
		case hasPat && t == tool && wildcard(pat, target):
			consider(2, dec)
		case hasPat && t == "*" && wildcard(pat, target):
			consider(1, dec)
		}
	}

	if !matchedAny {
		return Ask
	}
	return best
}

// wildcard reports whether pattern matches s. '*' matches any run of
// characters (including path separators). A leading "**/" segment matches zero
// or more path segments, so "**/*.go" matches both "main.go" and "a/b/x.go".
func wildcard(pattern, s string) bool {
	// Variant 1: collapse "**" to "*".
	if matchStar(strings.ReplaceAll(pattern, "**", "*"), s) {
		return true
	}
	// Variant 2: let "**/" match zero segments.
	collapsed := strings.ReplaceAll(pattern, "**/", "")
	collapsed = strings.ReplaceAll(collapsed, "**", "*")
	return matchStar(collapsed, s)
}

func matchStar(p, s string) bool {
	for len(p) > 0 {
		if p[0] == '*' {
			// Collapse consecutive stars.
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if len(p) == 0 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if matchStar(p, s[i:]) {
					return true
				}
			}
			return false
		}
		if len(s) == 0 || p[0] != s[0] {
			return false
		}
		p = p[1:]
		s = s[1:]
	}
	return len(s) == 0
}
