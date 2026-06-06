package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTMLToText(t *testing.T) {
	in := `<html><head><title>t</title><style>.a{}</style></head><body>` +
		`<h1>Hi</h1><p>Hello&nbsp;<b>world</b> &amp; friends</p>` +
		`<script>ignore()</script><ul><li>one</li><li>two</li></ul></body></html>`
	got := htmlToText(in)
	for _, want := range []string{"Hi", "Hello world & friends", "one", "two"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ignore()") || strings.Contains(got, ".a{}") {
		t.Fatalf("script/style leaked: %s", got)
	}
}

func TestWebfetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<p>Plain <b>text</b> here</p>"))
	}))
	defer srv.Close()

	tl := newWebfetchTool()
	raw, _ := json.Marshal(map[string]any{"url": srv.URL})
	res, err := tl.Run(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Plain text here") {
		t.Fatalf("converted text missing: %s", res.Content)
	}
}

func TestWebfetchRejectsNonHTTP(t *testing.T) {
	tl := newWebfetchTool()
	raw, _ := json.Marshal(map[string]any{"url": "file:///etc/passwd"})
	res, _ := tl.Run(context.Background(), raw)
	if !res.IsError {
		t.Fatal("expected non-http scheme to be rejected")
	}
}

func TestWebfetchHTTPErrorIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()
	tl := newWebfetchTool()
	raw, _ := json.Marshal(map[string]any{"url": srv.URL})
	res, _ := tl.Run(context.Background(), raw)
	if !res.IsError {
		t.Fatal("expected 404 to be marked as error")
	}
}
