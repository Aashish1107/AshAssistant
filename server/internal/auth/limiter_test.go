package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestLimiterBlocksAfterMaxFailures(t *testing.T) {
	limiter := newLoginLimiter(3, 10*time.Minute)
	now := time.Unix(1_000_000, 0)

	for i := range 3 {
		if wait := limiter.retryAfter("a", now); wait != 0 {
			t.Fatalf("blocked after %d failures", i)
		}
		limiter.recordFailure("a", now.Add(time.Duration(i)*time.Minute))
	}
	// Failures at +0, +1, +2 minutes. At +2 the oldest ages out at +10.
	if wait := limiter.retryAfter("a", now.Add(2*time.Minute)); wait != 8*time.Minute {
		t.Errorf("wait = %v, want 8m", wait)
	}
	if wait := limiter.retryAfter("a", now.Add(10*time.Minute)); wait != 0 {
		t.Errorf("still blocked once the oldest failure aged out (wait %v)", wait)
	}
	if wait := limiter.retryAfter("b", now); wait != 0 {
		t.Error("another client was blocked")
	}
}

func TestLimiterReset(t *testing.T) {
	limiter := newLoginLimiter(1, time.Minute)
	now := time.Unix(1_000_000, 0)
	limiter.recordFailure("a", now)
	limiter.reset("a")
	if wait := limiter.retryAfter("a", now); wait != 0 {
		t.Fatal("still blocked after reset")
	}
}

func TestLimiterForgetsOldClients(t *testing.T) {
	limiter := newLoginLimiter(5, time.Minute)
	now := time.Unix(1_000_000, 0)
	for i := range 1500 {
		limiter.recordFailure(fmt.Sprint(i), now)
	}
	limiter.recordFailure("new", now.Add(2*time.Minute))
	if n := len(limiter.failures); n != 1 {
		t.Fatalf("limiter still tracks %d clients, want only the recent one", n)
	}
}
