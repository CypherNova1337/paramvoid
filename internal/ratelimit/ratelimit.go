// Package ratelimit implements an adaptive token-bucket limiter.
//
// Unlike a fixed limiter, this one reacts to feedback from the target:
//   - Penalize() is called when the server signals it is overloaded (HTTP 429,
//     503, a Retry-After header, or repeated network errors). The limiter drops
//     its rate and installs a cooldown window that ALL workers respect.
//   - Reward() is called on sustained success and slowly ramps the rate back up
//     toward the configured maximum.
//
// This is what lets a scan absorb rate limiting and keep going instead of
// crashing: the offending request is simply retried after the cooldown at a
// lower rate.
package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"
)

// Limiter is a thread-safe adaptive token bucket.
type Limiter struct {
	mu sync.Mutex

	rate    float64 // current requests/second
	minRate float64
	maxRate float64
	adapt   bool

	tokens       float64
	last         time.Time
	backoffUntil time.Time
	successes    int

	// onAdjust, if set, is invoked (outside the lock) whenever the rate changes,
	// so the caller can log it. reason is "rate-limit", "network", or "recover".
	onAdjust func(newRate float64, reason string)
}

// New builds a limiter. maxRate is the starting and ceiling rate; minRate is the
// floor it will never drop below. If adapt is false, the rate is fixed at
// maxRate and Penalize only installs Retry-After cooldowns without lowering it.
func New(maxRate, minRate float64, adapt bool) *Limiter {
	if maxRate <= 0 {
		maxRate = 1000 // effectively "as fast as the target allows"
	}
	if minRate <= 0 {
		minRate = 1
	}
	if minRate > maxRate {
		minRate = maxRate
	}
	return &Limiter{
		rate:    maxRate,
		minRate: minRate,
		maxRate: maxRate,
		adapt:   adapt,
		tokens:  maxRate,
		last:    time.Now(),
	}
}

// OnAdjust registers a callback fired when the effective rate changes.
func (l *Limiter) OnAdjust(fn func(newRate float64, reason string)) { l.onAdjust = fn }

// Rate returns the current requests/second (for display).
func (l *Limiter) Rate() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rate
}

// Wait blocks until a request may be sent, honouring both the token bucket and
// any active cooldown window. It returns ctx.Err() if the context is cancelled.
func (l *Limiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := time.Now()

		// Refill tokens based on elapsed time.
		elapsed := now.Sub(l.last).Seconds()
		if elapsed > 0 {
			l.last = now
			l.tokens += elapsed * l.rate
			if l.tokens > l.rate {
				l.tokens = l.rate // cap burst at ~1 second's worth
			}
		}

		var wait time.Duration
		switch {
		case now.Before(l.backoffUntil):
			wait = l.backoffUntil.Sub(now)
		case l.tokens < 1:
			wait = time.Duration(((1 - l.tokens) / l.rate) * float64(time.Second))
		default:
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()

		if wait <= 0 {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Penalize records that the target is overloaded. retryAfter, if > 0, is honoured
// as the minimum cooldown (e.g. from a Retry-After header); otherwise a default
// escalating backoff is used. The rate is halved (down to minRate) when adapting.
func (l *Limiter) Penalize(retryAfter time.Duration) {
	l.mu.Lock()
	l.successes = 0
	if l.adapt {
		l.rate = math.Max(l.minRate, l.rate*0.5)
	}
	backoff := retryAfter
	if backoff <= 0 {
		backoff = 1500 * time.Millisecond
	}
	if backoff > 60*time.Second {
		backoff = 60 * time.Second // sanity cap on absurd Retry-After values
	}
	until := time.Now().Add(backoff)
	if until.After(l.backoffUntil) {
		l.backoffUntil = until
	}
	newRate := l.rate
	l.mu.Unlock()

	if l.onAdjust != nil {
		l.onAdjust(newRate, "rate-limit")
	}
}

// PenalizeNetwork applies a gentler slowdown for transient network errors
// (timeouts, resets) that are not explicit rate-limit signals.
func (l *Limiter) PenalizeNetwork() {
	l.mu.Lock()
	l.successes = 0
	if l.adapt {
		l.rate = math.Max(l.minRate, l.rate*0.8)
	}
	until := time.Now().Add(750 * time.Millisecond)
	if until.After(l.backoffUntil) {
		l.backoffUntil = until
	}
	newRate := l.rate
	l.mu.Unlock()

	if l.onAdjust != nil {
		l.onAdjust(newRate, "network")
	}
}

// Reward records a successful request and, after enough consecutive successes,
// nudges the rate back up toward the maximum.
func (l *Limiter) Reward() {
	if !l.adapt {
		return
	}
	l.mu.Lock()
	l.successes++
	var (
		bumped  bool
		newRate float64
	)
	if l.successes >= 20 && l.rate < l.maxRate {
		l.successes = 0
		l.rate = math.Min(l.maxRate, l.rate*1.25)
		bumped = true
		newRate = l.rate
	}
	l.mu.Unlock()

	if bumped && l.onAdjust != nil {
		l.onAdjust(newRate, "recover")
	}
}
