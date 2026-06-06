package cli

import "testing"

func TestParseAgentFlagsLoopAndTDD(t *testing.T) {
	f, err := parseAgentFlags([]string{"--tdd", "--loop", "--max-loops", "5", "build", "the", "thing"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.tdd || !f.loop || f.maxLoops != 5 {
		t.Fatalf("flags = %+v", f)
	}
	if got := joinRest(f); got != "build the thing" {
		t.Fatalf("rest = %q", got)
	}
}

func TestParseAgentFlagsUntilImpliesLoop(t *testing.T) {
	f, err := parseAgentFlags([]string{"--until", "all green", "go"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.loop || f.until != "all green" {
		t.Fatalf("--until should enable loop: %+v", f)
	}
}

func TestParseAgentFlagsBadMaxLoops(t *testing.T) {
	if _, err := parseAgentFlags([]string{"--max-loops", "abc"}); err == nil {
		t.Fatal("expected parse error")
	}
}

func joinRest(f agentFlags) string {
	out := ""
	for i, s := range f.rest {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
