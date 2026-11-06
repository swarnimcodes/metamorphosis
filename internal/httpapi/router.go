package httpapi

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/smarniw/metamorphosis/internal/config"
	"github.com/smarniw/metamorphosis/internal/httpx"
)

// requestTimeout bounds how long a request may run: past it the request context is cancelled, so a stuck
// database call cannot hold a connection forever.
const requestTimeout = 30 * time.Second

const usernameCheck = "/auth/username-available"

// authLimit throttles sign-in, signup and refresh: they are what a password-guessing or token-guessing script hits.
func authLimit(perMinute int) func(http.Handler) http.Handler {
	return httpx.RateLimit(perMinute, func(r *http.Request) bool {
		return strings.HasPrefix(r.URL.Path, "/auth/") && r.URL.Path != usernameCheck
	})
}

// usernameCheckLimit is looser: the signup screen asks as the person types (debounced), and a hesitant user
// should not be locked out of choosing a name.
func usernameCheckLimit(perMinute int) func(http.Handler) http.Handler {
	return httpx.RateLimit(perMinute*4, func(r *http.Request) bool { return r.URL.Path == usernameCheck })
}

// maxBodyBytes is the largest request body accepted. The biggest real one is a few hundred bytes.
const maxBodyBytes = 1 << 20

// NewHandler is the whole HTTP API: middleware, the health check and every operation of the spec, behind CORS.
func NewHandler(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID, httpx.RequestIDHeader, httpx.AccessLog, httpx.Recoverer, chimw.Timeout(requestTimeout),
		httpx.NoStore, httpx.MaxBody(maxBodyBytes), authLimit(d.Cfg.AuthRateLimit), usernameCheckLimit(d.Cfg.AuthRateLimit))
	r.NotFound(httpx.NotFoundHandler)
	r.MethodFunc(http.MethodGet, "/health", httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		var one int
		if err := d.DB.QueryRow(r.Context(), "select 1").Scan(&one); err != nil {
			return httpx.FromDB(err)
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return nil
	}))
	Register(r, d)
	return cors(d.Cfg, r)
}

// cors lets browsers may only call cross-origin from the configured origins. With none
// configured, dev mode stays permissive and production stays closed.
func cors(cfg *config.Config, next http.Handler) http.Handler {
	origins := cfg.CORSOrigins
	if len(origins) == 0 && !cfg.DevLogin {
		return next
	}
	permissive := len(origins) == 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := origin != "" && (permissive || slices.Contains(origins, origin))
		hdr := w.Header()
		switch {
		case permissive:
			if allowed {
				hdr.Set("Access-Control-Allow-Origin", "*")
			}
		default:
			// Like tower-http: the response varies by Origin whether or not this origin is allowed.
			hdr.Add("Vary", "Origin")
			if allowed {
				hdr.Set("Access-Control-Allow-Origin", origin)
			}
		}
		if r.Method == http.MethodOptions {
			if permissive {
				if allowed {
					hdr.Set("Access-Control-Allow-Methods", "*")
					hdr.Set("Access-Control-Allow-Headers", "*")
				}
			} else {
				hdr.Set("Access-Control-Allow-Methods", strings.Join([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, ","))
				hdr.Set("Access-Control-Allow-Headers", "authorization,content-type,accept")
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
