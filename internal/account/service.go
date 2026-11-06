// Package account is the signed-in person's own record and how they get signed in: the profile, deleting the
// account, sessions, and the Google sign-in and signup flow with its username rules.
package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smarniw/metamorphosis/internal/auth"
	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
	"github.com/smarniw/metamorphosis/internal/google"
)

// Store is the database access the service needs. *db.Queries satisfies it.
type Store interface {
	GetMe(ctx context.Context, id uuid.UUID) (db.GetMeRow, error)
	UpdateMe(ctx context.Context, arg db.UpdateMeParams) (db.UpdateMeRow, error)
	TimezoneKnown(ctx context.Context, name *string) (bool, error)
	GetUsername(ctx context.Context, id uuid.UUID) (string, error)
	LockUserForDelete(ctx context.Context, id uuid.UUID) (string, error)
	DeleteUser(ctx context.Context, id uuid.UUID) error
	UpsertDevUser(ctx context.Context, arg db.UpsertDevUserParams) (db.UpsertDevUserRow, error)
	UsernameTaken(ctx context.Context, username string) (bool, error)
	TouchGoogleLogin(ctx context.Context, subject string) (db.TouchGoogleLoginRow, error)
	GoogleIdentityExists(ctx context.Context, subject string) (bool, error)
	InsertUser(ctx context.Context, arg db.InsertUserParams) (uuid.UUID, error)
	InsertGoogleIdentity(ctx context.Context, arg db.InsertGoogleIdentityParams) error
}

// Sessions issues and renews sign-ins and the short-lived token that carries a verified Google identity from
// the sign-in call to the signup call. AuthSessions is the real one.
type Sessions interface {
	Issue(ctx context.Context, user uuid.UUID, userAgent *string) (auth.Session, error)
	// Rotate revokes the presented refresh token and returns the user and a new session.
	Rotate(ctx context.Context, refreshToken string, userAgent *string) (uuid.UUID, auth.Session, error)
	IssueSignupToken(id google.Identity) (string, error)
	VerifySignupToken(token string) (google.Identity, error)
}

// GoogleVerifier checks a Google ID token. It returns google.ErrInvalid for a token that is not acceptable.
type GoogleVerifier interface {
	Verify(ctx context.Context, idToken string) (google.Identity, error)
}

// Service holds the account rules.
type Service struct {
	store    Store
	inTx     func(ctx context.Context, fn func(Store) error) error
	sessions Sessions
	google   GoogleVerifier // nil unless Google sign-in is configured

	// randN and newID are replaced in tests.
	randN func(n int) int
	newID func() string
}

// New builds the service. google may be nil.
func New(store Store, inTx func(ctx context.Context, fn func(Store) error) error, sessions Sessions, google GoogleVerifier) *Service {
	return &Service{
		store: store, inTx: inTx, sessions: sessions, google: google,
		randN: rand.IntN, //nolint:gosec // a username suffix, not a secret
		newID: func() string { return strings.ReplaceAll(uuid.Must(uuid.NewV7()).String(), "-", "") },
	}
}

// Profile is the account as its owner sees it.
type Profile struct {
	ID          uuid.UUID
	Username    string
	DisplayName string
	Email       string
	AvatarURL   *string
	UnitSystem  string
	Timezone    string
}

// Login is what every sign-in path returns.
type Login struct {
	Session  auth.Session
	UserID   uuid.UUID
	Username string
}

// ---- profile ----

func profile(id uuid.UUID, username, displayName, email string, avatar *string, unit db.UnitSystem, tz string) Profile {
	return Profile{ID: id, Username: username, DisplayName: displayName, Email: email, AvatarURL: avatar, UnitSystem: string(unit), Timezone: tz}
}

// Me returns the caller's profile. A token for an account that no longer exists is simply unauthorized.
func (s *Service) Me(ctx context.Context, user uuid.UUID) (Profile, error) {
	r, err := s.store.GetMe(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, domain.Unauthorized()
	}
	if err != nil {
		return Profile{}, err
	}
	return profile(r.ID, r.Username, r.DisplayName, r.Email, r.AvatarUrl, r.UnitSystem, r.Timezone), nil
}

// ProfileChanges are the fields to change; nil means unchanged.
type ProfileChanges struct {
	DisplayName *string
	UnitSystem  *string
	Timezone    *string
}

// UpdateProfile applies the changes. The display name is trimmed and must be 1-80 characters.
func (s *Service) UpdateProfile(ctx context.Context, user uuid.UUID, c ProfileChanges) (Profile, error) {
	if c.DisplayName != nil {
		name := strings.TrimSpace(*c.DisplayName)
		if name == "" || utf8.RuneCountInString(name) > 80 {
			return Profile{}, domain.Invalid("display_name must be 1-80 characters")
		}
		c.DisplayName = &name
	}
	var unit *db.UnitSystem
	if c.UnitSystem != nil {
		u := db.UnitSystem(*c.UnitSystem)
		if u != db.UnitSystemMetric && u != db.UnitSystemImperial {
			return Profile{}, domain.Invalid("unit_system must be metric or imperial")
		}
		unit = &u
	}
	if c.Timezone != nil {
		known, err := s.store.TimezoneKnown(ctx, c.Timezone)
		if err != nil {
			return Profile{}, err
		}
		if !known {
			return Profile{}, domain.Invalid("unknown timezone")
		}
	}
	r, err := s.store.UpdateMe(ctx, db.UpdateMeParams{ID: user, DisplayName: c.DisplayName, UnitSystem: unit, Timezone: c.Timezone})
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, domain.Unauthorized()
	}
	if err != nil {
		return Profile{}, err
	}
	return profile(r.ID, r.Username, r.DisplayName, r.Email, r.AvatarUrl, r.UnitSystem, r.Timezone), nil
}

// Delete permanently removes the account and everything it owns (it all cascades from the user row). The
// confirmation must match the username, ignoring case and surrounding space: a deliberate step, and a guard
// against a stray request or a stolen access token deleting everything by itself.
func (s *Service) Delete(ctx context.Context, user uuid.UUID, confirmUsername string) error {
	err := s.inTx(ctx, func(tx Store) error {
		username, err := tx.LockUserForDelete(ctx, user)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Unauthorized()
		}
		if err != nil {
			return err
		}
		if !strings.EqualFold(username, strings.TrimSpace(confirmUsername)) {
			return domain.InvalidCode("username_mismatch", "the username does not match this account")
		}
		return tx.DeleteUser(ctx, user)
	})
	if err != nil {
		return err
	}
	slog.Info("account deleted", "user_id", user)
	return nil
}

// ---- sessions ----

// Refresh exchanges a refresh token for a new session.
func (s *Service) Refresh(ctx context.Context, refreshToken string, userAgent *string) (Login, error) {
	user, session, err := s.sessions.Rotate(ctx, refreshToken, userAgent)
	if err != nil {
		return Login{}, err
	}
	username, err := s.store.GetUsername(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return Login{}, domain.Unauthorized()
	}
	if err != nil {
		return Login{}, err
	}
	return Login{Session: session, UserID: user, Username: username}, nil
}

// DevLogin creates the user on first use, then issues the same session a real sign-in would. The username is
// trimmed and lowercased; the database refuses what is not a valid one. The display name only matters on the
// first call.
func (s *Service) DevLogin(ctx context.Context, username string, displayName *string, userAgent *string) (Login, error) {
	username = normalizeUsername(username)
	name := username
	if displayName != nil {
		name = *displayName
	}
	u, err := s.store.UpsertDevUser(ctx, db.UpsertDevUserParams{Email: username + "@dev.local", Username: username, DisplayName: name})
	if err != nil {
		return Login{}, err
	}
	session, err := s.sessions.Issue(ctx, u.ID, userAgent)
	if err != nil {
		return Login{}, err
	}
	return Login{Session: session, UserID: u.ID, Username: u.Username}, nil
}

// ---- google ----

// SignupOffer is what a new Google account gets instead of a session: nothing is created until they choose a
// username.
type SignupOffer struct {
	Token             string
	SuggestedUsername string
	DisplayName       string
	Email             string
	AvatarURL         *string
}

// GoogleResult is a login (Login set) or an offer to sign up (Signup set).
type GoogleResult struct {
	Login  *Login
	Signup *SignupOffer
}

// GoogleSignIn verifies the ID token. A known Google account is logged in; a new one gets a signup offer.
func (s *Service) GoogleSignIn(ctx context.Context, idToken string, userAgent *string) (GoogleResult, error) {
	identity, err := s.google.Verify(ctx, idToken)
	switch {
	case errors.Is(err, google.ErrInvalid):
		return GoogleResult{}, domain.Unauthorized()
	case err != nil:
		return GoogleResult{}, domain.Upstream("could not verify the Google token right now, please try again")
	}

	known, err := s.store.TouchGoogleLogin(ctx, identity.Sub)
	if err == nil {
		session, err := s.sessions.Issue(ctx, known.ID, userAgent)
		if err != nil {
			return GoogleResult{}, err
		}
		return GoogleResult{Login: &Login{Session: session, UserID: known.ID, Username: known.Username}}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return GoogleResult{}, err
	}

	suggested, err := s.suggestUsername(ctx, identity)
	if err != nil {
		return GoogleResult{}, err
	}
	displayName := suggested
	if identity.Name != nil && strings.TrimSpace(*identity.Name) != "" {
		displayName = *identity.Name
	}
	token, err := s.sessions.IssueSignupToken(identity)
	if err != nil {
		return GoogleResult{}, err
	}
	return GoogleResult{Signup: &SignupOffer{
		Token: token, SuggestedUsername: suggested, DisplayName: displayName, Email: identity.Email, AvatarURL: identity.Picture,
	}}, nil
}

// GoogleSignup creates the account and its linked Google identity together, then signs it in.
func (s *Service) GoogleSignup(ctx context.Context, signupToken, username string, displayName *string, userAgent *string) (Login, error) {
	identity, err := s.sessions.VerifySignupToken(signupToken)
	if err != nil {
		return Login{}, err
	}

	username = normalizeUsername(username)
	switch CheckUsername(username) {
	case UsernameInvalid:
		return Login{}, domain.InvalidCode("invalid_username", "usernames are 3-30 characters: lowercase letters, digits and underscores")
	case UsernameReserved:
		return Login{}, domain.InvalidCode("reserved_username", "that username is reserved")
	}

	name := username
	if displayName != nil && strings.TrimSpace(*displayName) != "" {
		name = strings.TrimSpace(*displayName)
	} else if identity.Name != nil && strings.TrimSpace(*identity.Name) != "" {
		name = *identity.Name
	}
	if utf8.RuneCountInString(name) > 80 {
		return Login{}, domain.Invalid("display_name must be at most 80 characters")
	}

	var userID uuid.UUID
	err = s.inTx(ctx, func(tx Store) error {
		// A replayed signup token must not create a second account.
		already, err := tx.GoogleIdentityExists(ctx, identity.Sub)
		if err != nil {
			return err
		}
		if already {
			return errIdentityExists
		}
		userID, err = tx.InsertUser(ctx, db.InsertUserParams{Email: identity.Email, Username: username, DisplayName: name, AvatarUrl: identity.Picture})
		if err != nil {
			return signupError(err)
		}
		email := identity.Email
		return signupError(tx.InsertGoogleIdentity(ctx, db.InsertGoogleIdentityParams{UserID: userID, Subject: identity.Sub, Email: &email}))
	})
	if err != nil {
		return Login{}, err
	}

	session, err := s.sessions.Issue(ctx, userID, userAgent)
	if err != nil {
		return Login{}, err
	}
	return Login{Session: session, UserID: userID, Username: username}, nil
}

var errIdentityExists = domain.Conflict("identity_exists", "this Google account is already registered")

// signupError maps the unique constraints a signup can trip to errors the UI can act on.
func signupError(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23505" {
		return err
	}
	switch pg.ConstraintName {
	case "users_username_key":
		return domain.Conflict("username_taken", "that username is taken")
	case "users_email_key":
		return domain.Conflict("email_in_use", "an account with this email already exists")
	case "auth_identities_provider_provider_subject_key":
		return errIdentityExists
	}
	return err
}

// ---- usernames ----

// reserved are handles nobody should be able to claim.
var reserved = []string{
	"about", "account", "admin", "administrator", "api", "app", "auth", "explore", "exercise", "exercises",
	"feed", "friends", "gym", "gyms", "help", "login", "logout", "me", "metamorph", "mod", "moderator",
	"null", "official", "privacy", "program", "programs", "register", "root", "search", "settings",
	"signin", "signup", "staff", "support", "system", "terms", "undefined", "user", "users", "workout",
	"workouts", "www",
}

// UsernameProblem says why a username cannot be used.
type UsernameProblem int

const (
	UsernameOK UsernameProblem = iota
	UsernameInvalid
	UsernameReserved
)

// CheckUsername is the same rule as the users_username_check constraint, plus the reserved list.
func CheckUsername(username string) UsernameProblem {
	wellFormed := len(username) >= 3 && len(username) <= 30
	for i := 0; i < len(username) && wellFormed; i++ {
		b := username[i]
		wellFormed = (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_'
	}
	switch {
	case !wellFormed:
		return UsernameInvalid
	case slices.Contains(reserved, username):
		return UsernameReserved
	}
	return UsernameOK
}

func normalizeUsername(raw string) string { return strings.ToLower(strings.TrimSpace(raw)) }

// Availability says whether a username can be taken: "" when it can, otherwise "invalid", "reserved" or "taken".
func (s *Service) Availability(ctx context.Context, raw string) (reason string, err error) {
	username := normalizeUsername(raw)
	switch CheckUsername(username) {
	case UsernameInvalid:
		return "invalid", nil
	case UsernameReserved:
		return "reserved", nil
	}
	taken, err := s.store.UsernameTaken(ctx, username)
	if err != nil {
		return "", err
	}
	if taken {
		return "taken", nil
	}
	return "", nil
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

// suggestUsername tries the base name, then twelve numbered variants, then settles for a unique suffix.
func (s *Service) suggestUsername(ctx context.Context, id google.Identity) (string, error) {
	base := baseUsername(id)
	for attempt := 0; attempt < 12; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s%d", base, 10+s.randN(9990))
		}
		if CheckUsername(candidate) != UsernameOK {
			continue
		}
		taken, err := s.store.UsernameTaken(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return base + s.newID()[26:], nil
}
