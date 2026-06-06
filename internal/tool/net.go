package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ---- webfetch ----

type webfetchTool struct{ http *http.Client }

func newWebfetchTool() Tool {
	return &webfetchTool{http: &http.Client{Timeout: 30 * time.Second}}
}

func (t *webfetchTool) Name() string { return "webfetch" }
func (t *webfetchTool) Description() string {
	return "Fetch a URL over HTTP(S) and return its content as readable text (HTML is stripped to text). Use to read documentation or pages. Network access; gated by permissions."
}
func (t *webfetchTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"Absolute http(s) URL"},"maxBytes":{"type":"integer","description":"Max bytes to read (default 200000)"},"raw":{"type":"boolean","description":"Return raw body without HTML-to-text conversion"}},"required":["url"]}`)
}

func (t *webfetchTool) Run(ctx context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		URL      string `json:"url"`
		MaxBytes int    `json:"maxBytes"`
		Raw      bool   `json:"raw"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	if a.URL == "" {
		return Result{Content: "url is required", IsError: true}, nil
	}
	if !strings.HasPrefix(a.URL, "http://") && !strings.HasPrefix(a.URL, "https://") {
		return Result{Content: "url must start with http:// or https://", IsError: true}, nil
	}
	limit := a.MaxBytes
	if limit <= 0 {
		limit = 200000
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	req.Header.Set("User-Agent", "ties-cli/0.1 (+https://github.com/defomok-max/Ties)")
	req.Header.Set("Accept", "text/html,text/plain,application/json;q=0.9,*/*;q=0.8")
	resp, err := t.http.Do(req)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return Result{Content: err.Error(), IsError: true}, nil
	}
	truncated := false
	if len(data) > limit {
		data = data[:limit]
		truncated = true
	}
	body := string(data)
	ct := resp.Header.Get("Content-Type")
	if !a.Raw && strings.Contains(ct, "html") {
		body = htmlToText(body)
	}
	header := fmt.Sprintf("HTTP %d · %s · %d bytes", resp.StatusCode, firstWord(ct), len(data))
	if truncated {
		header += " (truncated)"
	}
	out := header + "\n\n" + body
	if resp.StatusCode >= 400 {
		return Result{Content: out, IsError: true}, nil
	}
	return Result{Content: out}, nil
}

func firstWord(s string) string {
	if i := strings.IndexAny(s, "; "); i >= 0 {
		return s[:i]
	}
	if s == "" {
		return "?"
	}
	return s
}

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style|noscript|head)[^>]*>.*?</(script|style|noscript|head)>`)
	reBlock       = regexp.MustCompile(`(?i)</(p|div|section|article|li|tr|h[1-6]|br|ul|ol|table|blockquote)\s*>`)
	reBr          = regexp.MustCompile(`(?i)<br\s*/?>`)
	reTag         = regexp.MustCompile(`(?s)<[^>]+>`)
	reBlankLines  = regexp.MustCompile(`\n[ \t]*\n[ \t]*(\n[ \t]*)+`)
	reSpaces      = regexp.MustCompile(`[ \t]{2,}`)
)

// htmlToText converts an HTML document into a plain-text approximation using
// only the standard library: scripts/styles are dropped, block-level closers
// become newlines, remaining tags are removed and common entities decoded.
func htmlToText(s string) string {
	s = reScriptStyle.ReplaceAllString(s, " ")
	s = reBr.ReplaceAllString(s, "\n")
	s = reBlock.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = decodeEntities(s)
	s = reSpaces.ReplaceAllString(s, " ")
	s = reBlankLines.ReplaceAllString(s, "\n\n")
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(strings.TrimLeft(ln, " \t"), " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

var entityReplacer = strings.NewReplacer(
	"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"",
	"&#39;", "'", "&apos;", "'", "&nbsp;", " ", "&mdash;", "—",
	"&ndash;", "–", "&hellip;", "…", "&copy;", "©", "&reg;", "®",
	"&#x27;", "'", "&rsquo;", "'", "&lsquo;", "'", "&rdquo;", "\"", "&ldquo;", "\"",
)

func decodeEntities(s string) string { return entityReplacer.Replace(s) }
