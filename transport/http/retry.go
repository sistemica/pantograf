package http

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Backoff is a small exponential delay helper used by polling triggers
// and rate-limited callers. Each Next() returns the current delay then
// doubles it, capped at max. Reset() returns to initial.
//
// Not goroutine-safe — each goroutine should hold its own.
type Backoff struct {
	cur, initial, max time.Duration
}

// NewBackoff returns a Backoff with the given initial and max durations.
// Typical usage: 1s → 30s for polling-error recovery; 600ms → 30s for
// rate-limit retries.
func NewBackoff(initial, max time.Duration) *Backoff {
	return &Backoff{cur: initial, initial: initial, max: max}
}

// Next returns the current delay and advances. After the cap is hit,
// every call returns max.
func (b *Backoff) Next() time.Duration {
	d := b.cur
	b.cur *= 2
	if b.cur > b.max {
		b.cur = b.max
	}
	return d
}

// Reset returns the cursor to the initial delay.
func (b *Backoff) Reset() { b.cur = b.initial }

// RetryOn wraps fn and retries up to maxAttempts when fn returns an
// *APIError with a status code in codes. Backoff doubles 600ms → 30s
// per retry. ctx cancellation breaks out immediately; non-matching
// errors return without retry.
//
// Caller picks which codes to retry on:
//   RetryOn(ctx, fn, http.StatusTooManyRequests)
//   RetryOn(ctx, fn, 429, 503)
//
// Returns the last seen error wrapped with retry context after the
// final attempt fails.
func RetryOn(ctx context.Context, fn func() error, codes ...int) error {
	const maxAttempts = 5
	bo := NewBackoff(600*time.Millisecond, 30*time.Second)
	matches := func(err error) bool {
		if err == nil {
			return false
		}
		var ae *APIError
		if !errors.As(err, &ae) {
			return false
		}
		for _, code := range codes {
			if ae.Status == code {
				return true
			}
		}
		return false
	}
	var last error
	for i := 0; i < maxAttempts; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		if !matches(err) {
			return err
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bo.Next()):
		}
	}
	return fmt.Errorf("retried %d times: %w", maxAttempts, last)
}
