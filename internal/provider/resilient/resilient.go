// Package resilient wraps a provider.Provider with production-grade behavior:
// automatic retries with exponential backoff and jitter on retryable errors,
// and an ordered model-fallback chain. It depends only on the standard library
// and the vendor-neutral provider interface.
package resilient

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/defomok-max/Ties/internal/provider"
)

// retryable is implemented by provider errors that indicate a transient
// failure (e.g. HTTP 429 / 5xx). The anthropic and openai APIError types both
// satisfy it.
type retryable interface{ Retryable() bool }

// IsRetryable reports whether err (or anything it wraps) is transient.
func IsRetryable(err error) bool {
	var r retryable
	if errors.As(err, &r) {
		return r.Retryable()
	}
	return false
}

// RetryOptions configures Retrying.
type RetryOptions struct {
	// MaxRetries is the number of additional attempts after the first.
	MaxRetries int
	// Base is the initial backoff; it doubles each attempt with jitter.
	Base time.Duration
	// OnRetry, if set, is called before sleeping for a retry.
	OnRetry func(attempt int, err error, wait time.Duration)
}

type retrying struct {
	inner provider.Provider
	opt   RetryOptions
}

// Retrying wraps p so that a retryable error from Stream is retried with
// exponential backoff and jitter. Only the initial (pre-stream) connection
// error is retried, which keeps already-emitted output intact.
func Retrying(p provider.Provider, opt RetryOptions) provider.Provider {
	if opt.MaxRetries <= 0 {
		return p
	}
	if opt.Base <= 0 {
		opt.Base = 500 * time.Millisecond
	}
	return &retrying{inner: p, opt: opt}
}

func (r *retrying) Name() string { return r.inner.Name() }

func (r *retrying) Stream(ctx context.Context, req provider.Request) (<-chan provider.StreamEvent, error) {
	var lastErr error
	for attempt := 0; attempt <= r.opt.MaxRetries; attempt++ {
		ch, err := r.inner.Stream(ctx, req)
		if err == nil {
			return ch, nil
		}
		lastErr = err
		if attempt == r.opt.MaxRetries || !IsRetryable(err) {
			return nil, err
		}
		wait := backoff(r.opt.Base, attempt)
		if r.opt.OnRetry != nil {
			r.opt.OnRetry(attempt+1, err, wait)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// backoff returns base * 2^attempt plus up to base of random jitter.
func backoff(base time.Duration, attempt int) time.Duration {
	d := base << attempt
	jitter := time.Duration(rand.Int63n(int64(base) + 1)) //nolint:gosec // jitter, not security-sensitive
	return d + jitter
}

// Entry is one link in a fallback chain: a provider and the model to ask it for.
type Entry struct {
	Provider provider.Provider
	Model    string
}

type chain struct {
	entries    []Entry
	onFallback func(from, to string, err error)
}

// ChainOptions configures a fallback chain.
type ChainOptions struct {
	// OnFallback is called when one entry fails and the next is tried.
	OnFallback func(from, to string, err error)
}

// Chain returns a provider that tries each entry in order, overriding the
// request model with the entry's model, and falls through to the next entry
// when Stream returns an error. A single entry is returned unwrapped.
func Chain(entries []Entry, opt ChainOptions) provider.Provider {
	if len(entries) == 1 {
		return entries[0].Provider
	}
	return &chain{entries: entries, onFallback: opt.OnFallback}
}

func (c *chain) Name() string {
	if len(c.entries) > 0 {
		return c.entries[0].Provider.Name()
	}
	return "chain"
}

func (c *chain) Stream(ctx context.Context, req provider.Request) (<-chan provider.StreamEvent, error) {
	var lastErr error
	for i, e := range c.entries {
		r := req
		if e.Model != "" {
			r.Model = e.Model
		}
		ch, err := e.Provider.Stream(ctx, r)
		if err == nil {
			return ch, nil
		}
		lastErr = err
		if i < len(c.entries)-1 && c.onFallback != nil {
			c.onFallback(e.Model, c.entries[i+1].Model, err)
		}
	}
	return nil, lastErr
}
