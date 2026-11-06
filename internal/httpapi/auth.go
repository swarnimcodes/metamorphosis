package httpapi

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smarniw/metamorphosis/internal/auth"
	"github.com/smarniw/metamorphosis/internal/config"
	"github.com/smarniw/metamorphosis/internal/httpx"
)

type userKey struct{}

// userFrom returns the signed-in user. Handlers of authenticated operations can rely on it: the middleware
// refuses the request before routing otherwise.
func userFrom(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(userKey{}).(uuid.UUID)
	return id
}

// publicRoutes are the only operations that need no access token ("METHOD /path"). Everything else is refused with
// 401 before the request is even parsed, so a malformed request cannot tell an anonymous caller more than a
// well-formed one can.
var publicRoutes = map[string]bool{
	// account
	"POST /auth/refresh":           true,
	"POST /auth/dev-login":         true,
	"POST /auth/google":            true,
	"POST /auth/google/signup":     true,
	"GET /auth/username-available": true,
}

// authenticate puts the signed-in user in the request context. A token for an account that no longer exists is
// refused at once, not when it expires.
func authenticate(pool *pgxpool.Pool, cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if publicRoutes[r.Method+" "+r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}
			id, err := auth.UserID(r, pool, cfg)
			if err != nil {
				httpx.RenderError(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, id)))
		})
	}
}
