package api

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smarniw/metamorphosis/internal/auth"
	"github.com/smarniw/metamorphosis/internal/httpx"
)

func (s *Server) authRoutes(r chi.Router) {
	h := httpx.Handle
	r.MethodFunc("GET", "/me", h(s.authed(s.me)))
	r.MethodFunc("PATCH", "/me", h(s.authed(s.updateMe)))
	r.MethodFunc("DELETE", "/me", h(s.authed(s.deleteMe)))
	r.MethodFunc("POST", "/auth/refresh", h(s.refresh))
}

type meResponse struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
	AvatarURL   *string   `json:"avatar_url"`
	UnitSystem  string    `json:"unit_system"`
	Timezone    string    `json:"timezone"`
}

const meColumns = `id, username::text, display_name, email::text, avatar_url, unit_system::text, timezone`

func scanMe(row pgx.Row) (meResponse, error) {
	var m meResponse
	err := row.Scan(&m.ID, &m.Username, &m.DisplayName, &m.Email, &m.AvatarURL, &m.UnitSystem, &m.Timezone)
	return m, err
}

// meOrUnauthorized: a token for a user that no longer exists is simply unauthorized.
func meOrUnauthorized(m meResponse, err error) (meResponse, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return m, httpx.Unauthorized()
	}
	return m, httpx.FromDB(err)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	m, err := meOrUnauthorized(scanMe(s.DB.QueryRow(r.Context(),
		`select `+meColumns+` from users where id = $1 and deleted_at is null`, userID)))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, m)
	return nil
}

// Omitted fields are left unchanged.
type updateMeRequest struct {
	DisplayName *string `json:"display_name"`
	UnitSystem  *string `json:"unit_system"`
	Timezone    *string `json:"timezone"`
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	var body updateMeRequest
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.DisplayName != nil {
		name := strings.TrimSpace(*body.DisplayName)
		if name == "" || utf8.RuneCountInString(name) > 80 {
			return httpx.BadRequest("display_name must be 1-80 characters")
		}
		body.DisplayName = &name
	}
	if body.UnitSystem != nil && *body.UnitSystem != "metric" && *body.UnitSystem != "imperial" {
		return httpx.BadRequest("unit_system must be metric or imperial")
	}
	if body.Timezone != nil {
		known, err := s.knownTimezone(r.Context(), *body.Timezone)
		if err != nil {
			return err
		}
		if !known {
			return httpx.BadRequest("unknown timezone")
		}
	}
	m, err := meOrUnauthorized(scanMe(s.DB.QueryRow(r.Context(),
		`update users
		    set display_name = coalesce($2, display_name),
		        unit_system = coalesce($3::unit_system, unit_system),
		        timezone = coalesce($4, timezone)
		  where id = $1 and deleted_at is null
		returning `+meColumns, userID, body.DisplayName, body.UnitSystem, body.Timezone)))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, m)
	return nil
}

// deleteMe permanently deletes the caller's account and everything it owns: workouts and their sets, weigh-ins,
// programs, saved gyms, sign-in links and refresh tokens all cascade from the user row. Shared gyms and exercises
// the user created stay in the directory without their name on them.
func (s *Server) deleteMe(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	var body struct {
		// Must match the account's username (ignoring case): a deliberate step, and a guard against a stray
		// request or a stolen access token deleting everything by itself.
		ConfirmUsername *string `json:"confirm_username"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.ConfirmUsername == nil {
		return httpx.BadRequest("confirm_username is required")
	}
	ctx := r.Context()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var username string
	err = tx.QueryRow(ctx,
		`select username::text from users where id = $1 and deleted_at is null for update`, userID).Scan(&username)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.Unauthorized()
	}
	if err != nil {
		return httpx.FromDB(err)
	}
	if !strings.EqualFold(username, strings.TrimSpace(*body.ConfirmUsername)) {
		return httpx.Coded(http.StatusBadRequest, "username_mismatch", "the username does not match this account")
	}
	if _, err := tx.Exec(ctx, `delete from users where id = $1`, userID); err != nil {
		return httpx.FromDB(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return httpx.FromDB(err)
	}
	slog.Info("account deleted", "user_id", userID)
	httpx.NoContent(w)
	return nil
}

// loginResponse is what every login path returns.
type loginResponse struct {
	AccessToken      string    `json:"access_token"`
	TokenType        string    `json:"token_type"`
	ExpiresIn        int64     `json:"expires_in"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresIn int64     `json:"refresh_expires_in"`
	UserID           uuid.UUID `json:"user_id"`
	Username         string    `json:"username"`
}

func newLogin(s auth.Session, userID uuid.UUID, username string) loginResponse {
	return loginResponse{s.AccessToken, "Bearer", s.ExpiresIn, s.RefreshToken, s.RefreshExpiresIn, userID, username}
}

func userAgent(r *http.Request) *string {
	if ua := r.Header.Get("User-Agent"); ua != "" {
		return &ua
	}
	return nil
}

// refresh is mounted regardless of DEV_LOGIN: every login path ends up here to renew.
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		RefreshToken *string `json:"refresh_token"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.RefreshToken == nil {
		return httpx.BadRequest("refresh_token is required")
	}
	userID, session, err := auth.RotateSession(r.Context(), s.DB, s.Cfg, *body.RefreshToken, userAgent(r))
	if err != nil {
		return err
	}
	var username string
	err = s.DB.QueryRow(r.Context(),
		`select username::text from users where id = $1 and deleted_at is null`, userID).Scan(&username)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.Unauthorized()
	}
	if err != nil {
		return httpx.FromDB(err)
	}
	httpx.WriteJSON(w, http.StatusOK, newLogin(session, userID, username))
	return nil
}

// devLogin creates the user on first use, then issues the same token a real login would.
func (s *Server) devLogin(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Username    *string `json:"username"`
		DisplayName *string `json:"display_name"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Username == nil {
		return httpx.BadRequest("username is required")
	}
	username := strings.ToLower(strings.TrimSpace(*body.Username))
	displayName := username
	if body.DisplayName != nil {
		displayName = *body.DisplayName
	}
	var id uuid.UUID
	var stored string
	if err := s.DB.QueryRow(r.Context(),
		`insert into users (email, username, display_name)
		 values ($1, $2, $3)
		 on conflict (username) do update set username = excluded.username
		 returning id, username::text`, username+"@dev.local", username, displayName).Scan(&id, &stored); err != nil {
		return httpx.FromDB(err)
	}
	session, err := auth.IssueSession(r.Context(), s.DB, s.Cfg, id, nil, userAgent(r))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, newLogin(session, id, stored))
	return nil
}
