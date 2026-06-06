package pricing

import (
	"math"
	"testing"
)

func TestLookupLongestMatch(t *testing.T) {
	p, ok := Lookup("anthropic/claude-3-5-sonnet-latest")
	if !ok || p.Input != 3.00 || p.Output != 15.00 {
		t.Fatalf("sonnet lookup = %+v ok=%v", p, ok)
	}
	if _, ok := Lookup("totally-unknown-model"); ok {
		t.Fatal("unknown model should not be found")
	}
}

func TestEstimate(t *testing.T) {
	cost, ok := Estimate("gpt-4o", 1_000_000, 1_000_000)
	if !ok {
		t.Fatal("gpt-4o should be priced")
	}
	want := 2.50 + 10.00
	if math.Abs(cost-want) > 1e-9 {
		t.Fatalf("cost = %v want %v", cost, want)
	}
}
