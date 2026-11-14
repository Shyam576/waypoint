// Package ratelimit implements a minimal in-memory, per-client-IP fixed
// window rate limiter, for the two endpoints that accept credentials
// before any authentication exists to key on: POST /auth/login and
// POST /bootstrap/admin. See docs/architecture-audit.md Finding 8.
//
// This is deliberately simple (standard library only, no external
// dependency) and intentionally scoped to a single-process MVP: state is
// an in-memory map, so it does not coordinate across multiple API
// replicas, and the map is never evicted (acceptable for an internal
// tool with a small, fairly stable set of client IPs behind a reverse
// proxy; a high-cardinality or internet-facing deployment would need an
// eviction policy or a shared store). It blocks brute-force guessing
// attempts; it is not a substitute for network-level protections.
package ratelimit

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Shyam576/waypoint/internal/httpx"
)

type window struct {
	count      int
	windowEnds time.Time
}

// PerIP returns middleware allowing at most maxPerWindow requests per
// window per client IP, identified by RemoteAddr (expected to already be
// the real client IP — mount this after chi's middleware.RealIP).
// Requests beyond the limit get 429 Too Many Requests.
func PerIP(maxPerWindow int, windowLen time.Duration) func(http.Handler) http.Handler {
	var mu sync.Mutex
	windows := map[string]*window{}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := clientIP(r)
			now := time.Now()

			mu.Lock()
			win, ok := windows[key]
			if !ok || now.After(win.windowEnds) {
				win = &window{count: 0, windowEnds: now.Add(windowLen)}
				windows[key] = win
			}
			win.count++
			exceeded := win.count > maxPerWindow
			mu.Unlock()

			if exceeded {
				httpx.Error(w, r, httpx.NewAPIError(http.StatusTooManyRequests, "rate_limited", "too many attempts; please wait before trying again"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
