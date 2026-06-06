// Package pricing provides a best-effort cost estimate for a completion based
// on a small, embedded table of public per-million-token prices. It is
// intentionally approximate: unknown models simply report ok=false.
package pricing

import "strings"

// Price is the USD cost per 1,000,000 tokens.
type Price struct {
	Input  float64
	Output float64
}

// table maps a lowercase model-name substring to its price. The longest
// matching key wins, so more specific names override family defaults.
var table = map[string]Price{
	// Anthropic
	"claude-3-5-haiku":  {Input: 0.80, Output: 4.00},
	"claude-3-5-sonnet": {Input: 3.00, Output: 15.00},
	"claude-3-7-sonnet": {Input: 3.00, Output: 15.00},
	"claude-3-opus":     {Input: 15.00, Output: 75.00},
	"claude-3-haiku":    {Input: 0.25, Output: 1.25},
	"claude-sonnet-4":   {Input: 3.00, Output: 15.00},
	"claude-opus-4":     {Input: 15.00, Output: 75.00},
	// OpenAI
	"gpt-4o-mini":  {Input: 0.15, Output: 0.60},
	"gpt-4o":       {Input: 2.50, Output: 10.00},
	"gpt-4.1-mini": {Input: 0.40, Output: 1.60},
	"gpt-4.1":      {Input: 2.00, Output: 8.00},
	"o3-mini":      {Input: 1.10, Output: 4.40},
	"o1-mini":      {Input: 1.10, Output: 4.40},
	"o1":           {Input: 15.00, Output: 60.00},
	// Google Gemini
	"gemini-1.5-flash": {Input: 0.075, Output: 0.30},
	"gemini-1.5-pro":   {Input: 1.25, Output: 5.00},
	"gemini-2.0-flash": {Input: 0.10, Output: 0.40},
	"gemini-2.5-flash": {Input: 0.30, Output: 2.50},
	"gemini-2.5-pro":   {Input: 1.25, Output: 10.00},
	// Common open models served by compatible providers (often free/local)
	"llama":   {Input: 0.00, Output: 0.00},
	"qwen":    {Input: 0.00, Output: 0.00},
	"mistral": {Input: 0.00, Output: 0.00},
}

// Lookup returns the price for a model name (matched case-insensitively by the
// longest table key contained in the name).
func Lookup(model string) (Price, bool) {
	m := strings.ToLower(model)
	var (
		best    Price
		bestLen int
		found   bool
	)
	for key, price := range table {
		if strings.Contains(m, key) && len(key) > bestLen {
			best, bestLen, found = price, len(key), true
		}
	}
	return best, found
}

// Estimate returns the USD cost of inputTokens/outputTokens for model and
// whether the model was found in the table.
func Estimate(model string, inputTokens, outputTokens int) (float64, bool) {
	p, ok := Lookup(model)
	if !ok {
		return 0, false
	}
	cost := float64(inputTokens)/1e6*p.Input + float64(outputTokens)/1e6*p.Output
	return cost, true
}
