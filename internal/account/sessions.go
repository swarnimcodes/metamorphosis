package account

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smarniw/metamorphosis/internal/auth"
	"github.com/smarniw/metamorphosis/internal/config"
	"github.com/smarniw/metamorphosis/internal/google"
)

// AuthSessions is Sessions backed by internal/auth: JWT access tokens and refresh tokens stored hashed.
type AuthSessions struct {
	Pool *pgxpool.Pool
	Cfg  *config.Config
}

func (a AuthSessions) Issue(ctx context.Context, user uuid.UUID, userAgent *string) (auth.Session, error) {
	return auth.IssueSession(ctx, a.Pool, a.Cfg, user, nil, userAgent)
}

func (a AuthSessions) Rotate(ctx context.Context, refreshToken string, userAgent *string) (uuid.UUID, auth.Session, error) {
	return auth.RotateSession(ctx, a.Pool, a.Cfg, refreshToken, userAgent)
}

func (a AuthSessions) IssueSignupToken(id google.Identity) (string, error) {
	return auth.IssueSignupToken(a.Cfg, id)
}

func (a AuthSessions) VerifySignupToken(token string) (google.Identity, error) {
	return auth.VerifySignupToken(a.Cfg, token)
}
