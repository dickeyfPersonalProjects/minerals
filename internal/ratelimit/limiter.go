// Package ratelimit implements a clock-injectable, in-memory
// token-bucket limiter keyed by an arbitrary string (a user id or a
// client IP, decided by the caller). It has no HTTP knowledge — the
// API middleware in internal/api maps requests to tiers + keys and
// renders the §10 429 envelope; this package only answers "may this
// key spend one token right now, and if not, how long until it can?".
//
// Design notes (mi-tnru):
//   - The clock is injected (NewLimiter takes a func() time.Time).
//     The limiter NEVER calls time.Now directly, so tests advance time
//     explicitly and never sleep.
//   - One bucket per key. A bucket refills continuously at Rate
//     tokens/sec up to Burst. A request spends one token.
//   - Idle buckets are swept lazily (clock-driven) so the map cannot
//     grow without bound under a churn of distinct keys (e.g. an IP
//     scan). No background goroutine — eviction rides on Allow.
//   - Storage is per-process/in-memory. Prod runs 2 replicas, so a
//     caller hitting both can draw up to 2× a tier's quota. That
//     per-replica approximation is an accepted ops tradeoff (operator
//     decision, mi-tnru) — exact global limits would need a shared
//     store (Redis). Tests run single-instance and assert exact limits.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// Clock returns the current time. Injected so tests drive the limiter
// deterministically. Production passes time.Now.
type Clock func() time.Time

// bucket is the per-key token-bucket state. last is the timestamp the
// token count was last brought current; tokens is the (fractional)
// balance as of last.
type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is a keyed token-bucket limiter. The zero value is not
// usable — construct via NewLimiter. Safe for concurrent use.
type Limiter struct {
	rate    float64 // tokens added per second
	burst   float64 // maximum token balance (== requests-per-window)
	idleTTL time.Duration
	now     Clock

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

// NewLimiter builds a limiter that admits up to requests calls per
// window per key, refilling continuously (burst == requests, rate ==
// requests/window). Buckets untouched for longer than idleTTL are
// evicted lazily. now must be non-nil.
//
// requests <= 0 or window <= 0 yields a limiter that rejects every
// call (a misconfiguration should fail closed, not open).
func NewLimiter(requests int, window, idleTTL time.Duration, now Clock) *Limiter {
	var rate, burst float64
	if requests > 0 && window > 0 {
		burst = float64(requests)
		rate = float64(requests) / window.Seconds()
	}
	return &Limiter{
		rate:      rate,
		burst:     burst,
		idleTTL:   idleTTL,
		now:       now,
		buckets:   make(map[string]*bucket),
		lastSweep: now(),
	}
}

// Allow reports whether key may spend one token now. When it may not,
// retryAfter is the time until the next token becomes available
// (always > 0 on a reject), suitable for a Retry-After header.
func (l *Limiter) Allow(key string) (allowed bool, retryAfter time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweepLocked(now)

	// A non-positive rate means the limiter is misconfigured/disabled
	// for this tier — fail closed.
	if l.rate <= 0 {
		return false, l.fullWindow()
	}

	b := l.buckets[key]
	if b == nil {
		// First sight of this key starts with a full bucket.
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		l.refillLocked(b, now)
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	// Tokens needed to reach 1, divided by the refill rate, is the
	// wait. Round up to whole seconds at the call site (Retry-After is
	// an integer); here we return the precise duration.
	deficit := 1 - b.tokens
	wait := time.Duration(deficit / l.rate * float64(time.Second))
	if wait <= 0 {
		wait = time.Nanosecond
	}
	return false, wait
}

// refillLocked brings b current as of now, capped at burst.
func (l *Limiter) refillLocked(b *bucket, now time.Time) {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
}

// fullWindow is the duration of one full window (burst/rate seconds),
// used as the Retry-After when the tier is disabled. Guards against a
// zero rate.
func (l *Limiter) fullWindow() time.Duration {
	if l.rate <= 0 {
		return time.Second
	}
	return time.Duration(l.burst / l.rate * float64(time.Second))
}

// sweepLocked evicts buckets idle longer than idleTTL. It runs at most
// once per idleTTL of clock advance to keep Allow O(1) amortized. A
// bucket that has had no activity for idleTTL has refilled to full, so
// dropping it is equivalent to keeping it — the next request rebuilds
// a full bucket.
func (l *Limiter) sweepLocked(now time.Time) {
	if l.idleTTL <= 0 {
		return
	}
	if now.Sub(l.lastSweep) < l.idleTTL {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.last) >= l.idleTTL {
			delete(l.buckets, k)
		}
	}
}

// len reports the number of live buckets. Test-only (same package).
func (l *Limiter) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
