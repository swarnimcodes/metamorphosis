package httpx

import (
	"net/http"
	"time"

	"github.com/go-chi/httprate"
)

// NoStore marks every response as not cacheable: it is one person's data, and no proxy or browser should keep it.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// MaxBody refuses a request body larger than limit bytes: reading past it fails, and the handler answers 413.
func MaxBody(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return http.MaxBytesHandler(next, limit) }
}

// RateLimit limits how often one client may make the requests that match: perMinute per client address, answered
// with 429 and Retry-After beyond that. perMinute <= 0 turns it off. Each call has its own counters, which live in
// this process (right for a single instance).
func RateLimit(perMinute int, match func(*http.Request) bool) func(http.Handler) http.Handler {
	if perMinute <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	limit := httprate.LimitBy(perMinute, time.Minute,
		func(r *http.Request) (string, error) { return ClientIP(r), nil },
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "60")
			writeError(w, &Error{Status: http.StatusTooManyRequests, Message: "too many requests, try again in a minute"})
		}))
	return func(next http.Handler) http.Handler {
		limited := limit(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if match(r) {
				limited.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
