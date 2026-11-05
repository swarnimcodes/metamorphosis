// Package auth issues and checks the API's tokens. The formats match the Rust API byte for byte: a user
// who signed in before the switch keeps their session, and a rollback keeps working for new sessions too.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smarniw/metamorphosis/internal/config"
	"github.com/smarniw/metamorphosis/internal/google"
	"github.com/smarniw/metamorphosis/internal/httpx"
)

const (
	accessTyp  = "access"
	signupTyp  = "signup"
	leeway     = 60 * time.Second // jsonwebtoken's default
	RefreshTTL = 30 * 24 * 60 * 60
	SignupTTL  = 10 * 60
)

func sign(secret string, claims jwt.MapClaims) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func parse(secret, token string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return []byte(secret), nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithLeeway(leeway))
	return claims, err
}

func IssueAccessToken(cfg *config.Config, userID uuid.UUID) (string, error) {
	return sign(cfg.JWTSecret, jwt.MapClaims{
		"typ": accessTyp,
		"sub": userID.String(),
		"exp": time.Now().Unix() + cfg.AccessTokenTTLSecs,
	})
}

// IssueSignupToken carries a verified Google identity between /auth/google and /auth/google/signup.
func IssueSignupToken(cfg *config.Config, id google.Identity) (string, error) {
	return sign(cfg.JWTSecret, jwt.MapClaims{
		"typ":     signupTyp,
		"sub":     id.Sub,
		"email":   id.Email,
		"name":    id.Name,
		"picture": id.Picture,
		"exp":     time.Now().Unix() + SignupTTL,
	})
}

func VerifySignupToken(cfg *config.Config, token string) (google.Identity, error) {
	c, err := parse(cfg.JWTSecret, token)
	if err != nil || c["typ"] != signupTyp {
		return google.Identity{}, httpx.Unauthorized()
	}
	str := func(k string) string { s, _ := c[k].(string); return s }
	optional := func(k string) *string {
		if s, ok := c[k].(string); ok {
			return &s
		}
		return nil
	}
	return google.Identity{Sub: str("sub"), Email: str("email"), Name: optional("name"), Picture: optional("picture")}, nil
}

// Session is an access token plus the refresh token that renews it.
type Session struct {
	AccessToken      string
	ExpiresIn        int64
	RefreshToken     string
	RefreshExpiresIn int64
}

func hashRefresh(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// IssueSession starts a session for a user whose identity is established. Pass a family id only when
// rotating an existing token. Only the SHA-256 of the refresh token is stored.
func IssueSession(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, userID uuid.UUID, family *uuid.UUID, userAgent *string) (Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, httpx.Internal(err)
	}
	refresh := base64.RawURLEncoding.EncodeToString(raw)
	fam := uuid.Must(uuid.NewV7())
	if family != nil {
		fam = *family
	}
	_, err := pool.Exec(ctx,
		`insert into refresh_tokens (user_id, token_hash, family_id, user_agent, expires_at)
		 values ($1, $2, $3, $4, now() + make_interval(secs => $5))`,
		userID, hashRefresh(refresh), fam, userAgent, float64(RefreshTTL))
	if err != nil {
		return Session{}, httpx.FromDB(err)
	}
	access, err := IssueAccessToken(cfg, userID)
	if err != nil {
		return Session{}, httpx.Internal(err)
	}
	return Session{access, cfg.AccessTokenTTLSecs, refresh, RefreshTTL}, nil
}

// RotateSession revokes the presented refresh token and issues a new session in the same family.
// Presenting an already-revoked token means it leaked, so the whole family is revoked.
func RotateSession(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, refreshToken string, userAgent *string) (uuid.UUID, Session, error) {
	hash := hashRefresh(refreshToken)
	// The conditional update makes concurrent use of one token race-safe: only one caller wins.
	var userID, family uuid.UUID
	err := pool.QueryRow(ctx,
		`update refresh_tokens set revoked_at = now()
		  where token_hash = $1 and revoked_at is null and expires_at > now()
		  returning user_id, family_id`, hash).Scan(&userID, &family)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := pool.Exec(ctx,
			`update refresh_tokens set revoked_at = now()
			  where revoked_at is null
			    and family_id = (select family_id from refresh_tokens
			                      where token_hash = $1 and revoked_at is not null)`, hash); err != nil {
			return uuid.Nil, Session{}, httpx.FromDB(err)
		}
		return uuid.Nil, Session{}, httpx.Unauthorized()
	}
	if err != nil {
		return uuid.Nil, Session{}, httpx.FromDB(err)
	}
	s, err := IssueSession(ctx, pool, cfg, userID, &family, userAgent)
	return userID, s, err
}

// UserID checks the request's bearer token and that the account still exists: a deleted account's tokens
// stop working at once, not when they expire.
func UserID(r *http.Request, pool *pgxpool.Pool, cfg *config.Config) (uuid.UUID, error) {
	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return uuid.Nil, httpx.Unauthorized()
	}
	c, err := parse(cfg.JWTSecret, token)
	if err != nil || c["typ"] != accessTyp {
		return uuid.Nil, httpx.Unauthorized()
	}
	sub, _ := c["sub"].(string)
	id, err := uuid.Parse(sub)
	if err != nil {
		return uuid.Nil, httpx.Unauthorized()
	}
	var exists bool
	if err := pool.QueryRow(r.Context(),
		`select exists (select 1 from users where id = $1 and deleted_at is null)`, id).Scan(&exists); err != nil {
		return uuid.Nil, httpx.FromDB(fmt.Errorf("checking user: %w", err))
	}
	if !exists {
		return uuid.Nil, httpx.Unauthorized()
	}
	return id, nil
}
