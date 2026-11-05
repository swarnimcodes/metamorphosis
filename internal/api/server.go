// Package api is the HTTP layer: routes, handlers and the JSON shapes clients depend on.
package api

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smarniw/metamorphosis/internal/auth"
	"github.com/smarniw/metamorphosis/internal/config"
	"github.com/smarniw/metamorphosis/internal/google"
	"github.com/smarniw/metamorphosis/internal/httpx"
)

type Server struct {
	DB  *pgxpool.Pool
	Cfg *config.Config
	// Google is nil unless Google sign-in is configured.
	Google *google.Verifier
}

// Handler builds the router. A handler that needs a signed-in user takes it from authed().
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := httpx.Handle

	mux.HandleFunc("GET /health", h(s.health))
	s.authRoutes(mux)
	s.googleRoutes(mux)
	s.exercisesRoutes(mux)
	s.gymsRoutes(mux)
	s.workoutsRoutes(mux)
	s.statsRoutes(mux)
	s.bodyweightsRoutes(mux)
	if s.Cfg.DevLogin {
		mux.HandleFunc("POST /auth/dev-login", h(s.devLogin))
	}
	return s.cors(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) error {
	var one int
	if err := s.DB.QueryRow(r.Context(), "select 1").Scan(&one); err != nil {
		return httpx.FromDB(err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	return nil
}

// authed wraps a handler that needs the signed-in user.
func (s *Server) authed(f func(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := auth.UserID(r, s.DB, s.Cfg)
		if err != nil {
			return err
		}
		return f(w, r, id)
	}
}

// knownTimezone reports whether name is an IANA zone the database knows. Anything stored and later used in
// `at time zone` must pass this first, or every stats query for that user would fail.
func (s *Server) knownTimezone(ctx context.Context, name string) (bool, error) {
	var known bool
	err := s.DB.QueryRow(ctx, `select exists (select 1 from pg_timezone_names where name = $1)`, name).Scan(&known)
	return known, httpx.FromDB(err)
}

// cors follows the Rust API: browsers may only call cross-origin from the configured origins. With none
// configured, dev mode stays permissive and production stays closed.
func (s *Server) cors(next http.Handler) http.Handler {
	origins := s.Cfg.CORSOrigins
	if len(origins) == 0 && !s.Cfg.DevLogin {
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
