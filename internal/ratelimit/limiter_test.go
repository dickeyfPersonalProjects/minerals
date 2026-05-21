package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a controllable monotonic clock for deterministic tests.
// No time.Sleep anywhere — tests advance time explicitly.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// drain spends n tokens and asserts they were all admitted.
func drain(t *testing.T, l *Limiter, key string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if ok, _ := l.Allow(key); !ok {
			t.Fatalf("token %d/%d unexpectedly rejected", i+1, n)
		}
	}
}

func TestBucketMechanics_BurstThenReject(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	// 5 requests / 1s.
	l := NewLimiter(5, time.Second, time.Minute, clk.now)

	// First 5 (the burst) pass; the 6th is rejected without advancing.
	drain(t, l, "k", 5)
	ok, retry := l.Allow("k")
	if ok {
		t.Fatal("6th request should be rejected at full burst")
	}
	if retry <= 0 {
		t.Fatalf("reject must return a positive Retry-After, got %v", retry)
	}
}

func TestBucketMechanics_FullRefillAfterWindow(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	l := NewLimiter(5, time.Second, time.Minute, clk.now)

	drain(t, l, "k", 5)
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("expected rejection before refill")
	}

	// Advance a full window — bucket refills to the full burst.
	clk.advance(time.Second)
	drain(t, l, "k", 5)
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("expected rejection after re-draining the refilled bucket")
	}
}

func TestBucketMechanics_PartialRefill(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	// 10 requests / 1s => 10 tokens/sec.
	l := NewLimiter(10, time.Second, time.Minute, clk.now)

	drain(t, l, "k", 10)
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("expected rejection at empty bucket")
	}

	// Half a window refills ~5 tokens.
	clk.advance(500 * time.Millisecond)
	drain(t, l, "k", 5)
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("partial refill should grant exactly ~5 tokens, not more")
	}
}

func TestBucketMechanics_SteadyState(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	// 1 request / 100ms.
	l := NewLimiter(1, 100*time.Millisecond, time.Minute, clk.now)

	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("first request should pass")
	}
	// Each subsequent request is allowed exactly once per 100ms.
	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("k"); ok {
			t.Fatalf("iteration %d: expected reject before refill", i)
		}
		clk.advance(100 * time.Millisecond)
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("iteration %d: expected allow after 100ms refill", i)
		}
	}
}

func TestKeyIsolation(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	l := NewLimiter(2, time.Second, time.Minute, clk.now)

	// Drain key A; key B is independent and still full.
	drain(t, l, "a", 2)
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("key a should be exhausted")
	}
	drain(t, l, "b", 2)
	if ok, _ := l.Allow("b"); ok {
		t.Fatal("key b should be exhausted independently")
	}
}

func TestRetryAfterIsSane(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	// 1 request / 2s => after spending the token, the next is ~2s away.
	l := NewLimiter(1, 2*time.Second, time.Minute, clk.now)

	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("first request should pass")
	}
	ok, retry := l.Allow("k")
	if ok {
		t.Fatal("second request should be rejected")
	}
	// One token at 0.5 tok/sec is 2s. Allow a little float slack.
	if retry < 1900*time.Millisecond || retry > 2100*time.Millisecond {
		t.Fatalf("Retry-After should be ~2s, got %v", retry)
	}
}

func TestDisabledTierFailsClosed(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	// A non-positive budget disables the tier — every call is rejected.
	for _, l := range []*Limiter{
		NewLimiter(0, time.Second, time.Minute, clk.now),
		NewLimiter(5, 0, time.Minute, clk.now),
	} {
		if ok, retry := l.Allow("k"); ok || retry <= 0 {
			t.Fatalf("disabled tier must reject with positive retry, got ok=%v retry=%v", ok, retry)
		}
	}
}

func TestEviction_IdleBucketsRemoved(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	l := NewLimiter(5, time.Second, 5*time.Minute, clk.now)

	// Create a bucket for "old".
	if ok, _ := l.Allow("old"); !ok {
		t.Fatal("first request should pass")
	}
	if l.len() != 1 {
		t.Fatalf("expected 1 bucket, got %d", l.len())
	}

	// Advance past the idle TTL and touch a different key. The sweep
	// fires on Allow and evicts the now-idle "old" bucket; only "new"
	// remains.
	clk.advance(6 * time.Minute)
	if ok, _ := l.Allow("new"); !ok {
		t.Fatal("request for new key should pass")
	}
	if got := l.len(); got != 1 {
		t.Fatalf("expected map to shrink to 1 (old evicted), got %d", got)
	}
}

func TestConcurrency_ExactlyBurstAllowed(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	const n = 50
	l := NewLimiter(n, time.Second, time.Minute, clk.now)

	var allowed atomic.Int32
	var wg sync.WaitGroup
	// 4n goroutines race on the same key at a fixed clock; exactly n
	// (the burst) must be admitted, the rest rejected, with no race
	// (run with -race).
	for i := 0; i < 4*n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow("shared"); ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := allowed.Load(); got != n {
		t.Fatalf("expected exactly %d admitted, got %d", n, got)
	}
}
