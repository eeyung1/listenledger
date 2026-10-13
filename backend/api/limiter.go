package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter implements a thread-safe sliding-window rate limiter per client key.
type RateLimiter struct {
	mu          sync.Mutex
	limit       int           // Max requests per window
	window      time.Duration // Time window duration
	visitors    map[string][]time.Time
	lastCleanup time.Time
}

// NewRateLimiter constructs a RateLimiter with the specified request count and window duration.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:       limit,
		window:      window,
		visitors:    make(map[string][]time.Time),
		lastCleanup: time.Now(),
	}
}

// Allow reports whether a request for the given key is allowed under the rate limit.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Periodic cleanup of stale visitor records every 5 minutes
	if now.Sub(rl.lastCleanup) > 5*time.Minute {
		cutoff := now.Add(-rl.window)
		for k, timestamps := range rl.visitors {
			valid := timestamps[:0]
			for _, ts := range timestamps {
				if ts.After(cutoff) {
					valid = append(valid, ts)
				}
			}
			if len(valid) == 0 {
				delete(rl.visitors, k)
			} else {
				rl.visitors[k] = valid
			}
		}
		rl.lastCleanup = now
	}

	cutoff := now.Add(-rl.window)
	timestamps := rl.visitors[key]

	// Filter timestamps within the current sliding window
	valid := timestamps[:0]
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}

	if len(valid) >= rl.limit {
		rl.visitors[key] = valid
		return false
	}

	rl.visitors[key] = append(valid, now)
	return true
}

// clientKey extracts the client identifier from the request (device token, bearer, or remote IP).
func clientKey(r *http.Request) string {
	if tok := strings.TrimSpace(r.Header.Get("X-Device-Token")); tok != "" {
		return "dev_hdr:" + tok
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(auth, "Bearer ") {
		return "bearer:" + strings.TrimPrefix(auth, "Bearer ")
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return "ip:" + host
	}
	return "ip:" + r.RemoteAddr
}

// LimitMiddleware wraps an http.Handler with rate limiting.
func (rl *RateLimiter) LimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := clientKey(r)
		if !rl.Allow(key) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded: please slow down requests")
			return
		}
		next(w, r)
	}
}
