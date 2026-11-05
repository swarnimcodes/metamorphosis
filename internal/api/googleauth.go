package api

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smarniw/metamorphosis/internal/auth"
	"github.com/smarniw/metamorphosis/internal/google"
	"github.com/smarniw/metamorphosis/internal/httpx"
)

// googleRoutes are mounted only when GOOGLE_CLIENT_IDS is configured.
//
// POST /auth/google verifies the ID token. A known Google account is logged in; a new one gets a short-lived
// signup token and nothing is created, because every account needs a username. POST /auth/google/signup then
// creates the user and the linked identity together.
func (s *Server) googleRoutes(mux *http.ServeMux) {
	if s.Google == nil {
		return
	}
	h := httpx.Handle
	mux.HandleFunc("POST /auth/google", h(s.googleLogin))
	mux.HandleFunc("POST /auth/google/signup", h(s.googleSignup))
	mux.HandleFunc("GET /auth/username-available", h(s.usernameAvailable))
}

// Handles nobody should be able to claim.
var reserved = []string{
	"about", "account", "admin", "administrator", "api", "app", "auth", "explore", "exercise", "exercises",
	"feed", "friends", "gym", "gyms", "help", "login", "logout", "me", "metamorph", "mod", "moderator",
	"null", "official", "privacy", "program", "programs", "register", "root", "search", "settings",
	"signin", "signup", "staff", "support", "system", "terms", "undefined", "user", "users", "workout",
	"workouts", "www",
}

type usernameProblem int

const (
	usernameOK usernameProblem = iota
	usernameInvalid
	usernameReserved
)

// checkUsername is the same rule as the users_username_check constraint, plus the reserved list.
func checkUsername(username string) usernameProblem {
	wellFormed := len(username) >= 3 && len(username) <= 30
	for i := 0; i < len(username) && wellFormed; i++ {
		b := username[i]
		wellFormed = (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_'
	}
	switch {
	case !wellFormed:
		return usernameInvalid
	case slices.Contains(reserved, username):
		return usernameReserved
	}
	return usernameOK
}

func normalizeUsername(raw string) string { return strings.ToLower(strings.TrimSpace(raw)) }

func (s *Server) usernameTaken(r *http.Request, username string) (bool, error) {
	var taken bool
	err := s.DB.QueryRow(r.Context(), `select exists (select 1 from users where username = $1)`, username).Scan(&taken)
	return taken, httpx.FromDB(err)
}

// baseUsername is `rahul_sharma` from "Rahul Sharma", falling back to the email's local part.
func baseUsername(id google.Identity) string {
	slug := func(text string) string {
		var out []rune
		for _, c := range strings.ToLower(text) {
			switch {
			case c < 128 && (c >= 'a' && c <= 'z' || c >= '0' && c <= '9'):
				out = append(out, c)
			case len(out) > 0 && out[len(out)-1] != '_':
				out = append(out, '_')
			}
		}
		trimmed := strings.Trim(string(out), "_")
		if r := []rune(trimmed); len(r) > 24 {
			trimmed = string(r[:24])
		}
		return trimmed
	}
	name := ""
	if id.Name != nil {
		name = *id.Name
	}
	local, _, _ := strings.Cut(id.Email, "@")
	for _, candidate := range []string{slug(name), slug(local)} {
		if len(candidate) >= 3 {
			return candidate
		}
	}
	return "lifter"
}

func (s *Server) suggestUsername(r *http.Request, id google.Identity) (string, error) {
	base := baseUsername(id)
	for attempt := 0; attempt < 12; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s%d", base, 10+rand.IntN(9990))
		}
		if checkUsername(candidate) != usernameOK {
			continue
		}
		taken, err := s.usernameTaken(r, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	simple := strings.ReplaceAll(uuid.Must(uuid.NewV7()).String(), "-", "")
	return base + simple[26:], nil
}

func (s *Server) usernameAvailable(w http.ResponseWriter, r *http.Request) error {
	raw := r.URL.Query()["username"]
	if len(raw) == 0 {
		return httpx.BadRequest("username is required")
	}
	username := normalizeUsername(raw[0])
	unavailable := func(reason string) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"available": false, "reason": reason})
	}
	switch checkUsername(username) {
	case usernameInvalid:
		unavailable("invalid")
		return nil
	case usernameReserved:
		unavailable("reserved")
		return nil
	}
	taken, err := s.usernameTaken(r, username)
	if err != nil {
		return err
	}
	if taken {
		unavailable("taken")
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"available": true})
	return nil
}

func (s *Server) googleLogin(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		IDToken *string `json:"id_token"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.IDToken == nil {
		return httpx.BadRequest("id_token is required")
	}
	identity, err := s.Google.Verify(r.Context(), *body.IDToken)
	switch {
	case errors.Is(err, google.ErrInvalid):
		return httpx.Unauthorized()
	case err != nil:
		return httpx.Upstream("could not verify the Google token right now, please try again")
	}

	var userID uuid.UUID
	var username string
	err = s.DB.QueryRow(r.Context(),
		`update auth_identities ai set last_login_at = now()
		   from users u
		  where ai.provider = 'google' and ai.provider_subject = $1
		    and u.id = ai.user_id and u.deleted_at is null
		returning u.id, u.username::text`, identity.Sub).Scan(&userID, &username)
	if err == nil {
		session, err := auth.IssueSession(r.Context(), s.DB, s.Cfg, userID, nil, userAgent(r))
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, struct {
			SignupRequired bool `json:"signup_required"`
			loginResponse
		}{false, newLogin(session, userID, username)})
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return httpx.FromDB(err)
	}

	suggested, err := s.suggestUsername(r, identity)
	if err != nil {
		return err
	}
	displayName := suggested
	if identity.Name != nil && strings.TrimSpace(*identity.Name) != "" {
		displayName = *identity.Name
	}
	token, err := auth.IssueSignupToken(s.Cfg, identity)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"signup_required":    true,
		"signup_token":       token,
		"suggested_username": suggested,
		"display_name":       displayName,
		"email":              identity.Email,
		"avatar_url":         identity.Picture,
	})
	return nil
}

func conflict(code, message string) error { return httpx.Coded(http.StatusConflict, code, message) }

// signupError maps the unique constraints a signup can trip to errors the UI can act on.
func signupError(err error) error {
	switch httpx.UniqueViolation(err) {
	case "users_username_key":
		return conflict("username_taken", "that username is taken")
	case "users_email_key":
		return conflict("email_in_use", "an account with this email already exists")
	case "auth_identities_provider_provider_subject_key":
		return conflict("identity_exists", "this Google account is already registered")
	}
	return httpx.FromDB(err)
}

func (s *Server) googleSignup(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		SignupToken *string `json:"signup_token"`
		Username    *string `json:"username"`
		DisplayName *string `json:"display_name"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.SignupToken == nil || body.Username == nil {
		return httpx.BadRequest("signup_token and username are required")
	}
	identity, err := auth.VerifySignupToken(s.Cfg, *body.SignupToken)
	if err != nil {
		return err
	}

	username := normalizeUsername(*body.Username)
	switch checkUsername(username) {
	case usernameInvalid:
		return httpx.Coded(http.StatusBadRequest, "invalid_username",
			"usernames are 3-30 characters: lowercase letters, digits and underscores")
	case usernameReserved:
		return httpx.Coded(http.StatusBadRequest, "reserved_username", "that username is reserved")
	}

	displayName := username
	if body.DisplayName != nil && strings.TrimSpace(*body.DisplayName) != "" {
		displayName = strings.TrimSpace(*body.DisplayName)
	} else if identity.Name != nil && strings.TrimSpace(*identity.Name) != "" {
		displayName = *identity.Name
	}
	if utf8.RuneCountInString(displayName) > 80 {
		return httpx.BadRequest("display_name must be at most 80 characters")
	}

	ctx := r.Context()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// A replayed signup token must not create a second account.
	var already bool
	if err := tx.QueryRow(ctx,
		`select exists (select 1 from auth_identities where provider = 'google' and provider_subject = $1)`,
		identity.Sub).Scan(&already); err != nil {
		return httpx.FromDB(err)
	}
	if already {
		return conflict("identity_exists", "this Google account is already registered")
	}

	var userID uuid.UUID
	if err := tx.QueryRow(ctx,
		`insert into users (email, username, display_name, avatar_url) values ($1, $2, $3, $4) returning id`,
		identity.Email, username, displayName, identity.Picture).Scan(&userID); err != nil {
		return signupError(err)
	}
	if _, err := tx.Exec(ctx,
		`insert into auth_identities (user_id, provider, provider_subject, email_at_signup, last_login_at)
		 values ($1, 'google', $2, $3, now())`, userID, identity.Sub, identity.Email); err != nil {
		return signupError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return httpx.FromDB(err)
	}

	session, err := auth.IssueSession(ctx, s.DB, s.Cfg, userID, nil, userAgent(r))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, newLogin(session, userID, username))
	return nil
}
