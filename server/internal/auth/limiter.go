package auth

import (
	"sync"
	"time"
)

// loginLimiter remembers recent failed logins per client address and blocks
// a client after too many. It lives in memory: a server restart forgets it.
type loginLimiter struct {
	maxFailures int
	window      time.Duration

	mu       sync.Mutex
	failures map[string][]time.Time // client -> failure times, oldest first
}

func newLoginLimiter(maxFailures int, window time.Duration) *loginLimiter {
	return &loginLimiter{maxFailures: maxFailures, window: window, failures: map[string][]time.Time{}}
}

// retryAfter returns how long client must wait before trying again (0 = allowed).
func (l *loginLimiter) retryAfter(client string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.prune(client, now)
	if len(recent) < l.maxFailures {
		return 0
	}
	return recent[len(recent)-l.maxFailures].Add(l.window).Sub(now)
}

func (l *loginLimiter) recordFailure(client string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[client] = append(l.prune(client, now), now)

	// Forget clients whose failures have all aged out, so the map can't grow
	// without limit.
	if len(l.failures) > 1000 {
		for other := range l.failures {
			l.prune(other, now)
		}
	}
}

func (l *loginLimiter) reset(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, client)
}

// prune drops client's failures older than the window. Callers hold l.mu.
func (l *loginLimiter) prune(client string, now time.Time) []time.Time {
	times := l.failures[client]
	cutoff := now.Add(-l.window)
	kept := 0
	for kept < len(times) && !times[kept].After(cutoff) {
		kept++
	}
	times = times[kept:]
	if len(times) == 0 {
		delete(l.failures, client)
		return nil
	}
	l.failures[client] = times
	return times
}
