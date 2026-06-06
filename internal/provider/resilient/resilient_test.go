package resilient

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/defomok-max/Ties/internal/provider"
)

type retryErr struct{ retry bool }

func (e retryErr) Error() string   { return "boom" }
func (e retryErr) Retryable() bool { return e.retry }

// fakeProvider fails the first failN Stream calls with err, then succeeds.
type fakeProvider struct {
	name   string
	failN  int
	err    error
	calls  int
	models []string
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.StreamEvent, error) {
	f.calls++
	f.models = append(f.models, req.Model)
	if f.calls <= f.failN {
		return nil, f.err
	}
	ch := make(chan provider.StreamEvent)
	go func() {
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "ok"}
		ch <- provider.StreamEvent{Type: provider.EventDone}
		close(ch)
	}()
	return ch, nil
}

func TestIsRetryable(t *testing.T) {
	if !IsRetryable(retryErr{retry: true}) {
		t.Error("should be retryable")
	}
	if IsRetryable(retryErr{retry: false}) {
		t.Error("should not be retryable")
	}
	if IsRetryable(errors.New("plain")) {
		t.Error("plain error not retryable")
	}
}

func TestRetryingSucceedsAfterTransient(t *testing.T) {
	f := &fakeProvider{name: "x", failN: 2, err: retryErr{retry: true}}
	p := Retrying(f, RetryOptions{MaxRetries: 3, Base: time.Millisecond})
	ch, err := p.Stream(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	<-ch
	if f.calls != 3 {
		t.Errorf("calls = %d want 3", f.calls)
	}
}

func TestRetryingGivesUpOnNonRetryable(t *testing.T) {
	f := &fakeProvider{name: "x", failN: 5, err: retryErr{retry: false}}
	p := Retrying(f, RetryOptions{MaxRetries: 3, Base: time.Millisecond})
	if _, err := p.Stream(context.Background(), provider.Request{Model: "m"}); err == nil {
		t.Fatal("expected error")
	}
	if f.calls != 1 {
		t.Errorf("calls = %d want 1 (no retry on non-retryable)", f.calls)
	}
}

func TestChainFallsBack(t *testing.T) {
	bad := &fakeProvider{name: "bad", failN: 1, err: retryErr{retry: false}}
	good := &fakeProvider{name: "good", failN: 0}
	var fellBack bool
	c := Chain([]Entry{{Provider: bad, Model: "m1"}, {Provider: good, Model: "m2"}},
		ChainOptions{OnFallback: func(_, _ string, _ error) { fellBack = true }})
	ch, err := c.Stream(context.Background(), provider.Request{Model: "ignored"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	<-ch
	if !fellBack {
		t.Error("expected fallback callback")
	}
	if len(good.models) != 1 || good.models[0] != "m2" {
		t.Errorf("good provider got models %v, want [m2]", good.models)
	}
}

func TestChainSingleEntryUnwrapped(t *testing.T) {
	only := &fakeProvider{name: "only"}
	c := Chain([]Entry{{Provider: only, Model: "m"}}, ChainOptions{})
	if c != provider.Provider(only) {
		t.Error("single-entry chain should return the provider unwrapped")
	}
}
