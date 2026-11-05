package httpx

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// RequestIDHeader echoes the request id (set by chi's RequestID) so a client can quote it when reporting a problem.
func RequestIDHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := chimw.GetReqID(r.Context()); id != "" {
			w.Header().Set("X-Request-Id", id)
		}
		next.ServeHTTP(w, r)
	})
}

// Recoverer turns a panic in a handler into the API's usual 500, and logs it with its stack.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler { //nolint:errorlint // the sentinel is compared by identity on purpose
					panic(rec)
				}
				slog.Error("panic in handler", "panic", fmt.Sprint(rec), "stack", string(debug.Stack()),
					"request_id", chimw.GetReqID(r.Context()))
				writeError(w, Internal(fmt.Errorf("panic: %v", rec)))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// AccessLog logs one line per request at debug level (off in production, where the level is info).
// It records the route pattern, never the URL: query strings can hold usernames, and bodies and tokens never
// reach a log.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slog.Default().Enabled(r.Context(), slog.LevelDebug) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		slog.Debug("request", "method", r.Method, "route", route, "status", ww.Status(),
			"bytes", ww.BytesWritten(), "duration", time.Since(start), "request_id", chimw.GetReqID(r.Context()))
	})
}

// NotFoundHandler answers an unknown path the way net/http's mux always has.
func NotFoundHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNotFound)
	_, _ = fmt.Fprintln(w, "404 page not found")
}
