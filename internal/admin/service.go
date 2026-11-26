// Package admin is what only an admin may do: see everyone and make people moderators.
//
// Admins themselves are made and removed in the database, never here, so a slip in the app can't create or lose
// one. Every role change made through the app is recorded in role_changes.
package admin

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
)

const (
	listRows      = 50
	maxNeedleLen  = 50
	filterAll     = "all"
	filterStaff   = "staff"
	roleModerator = string(db.UserRoleModerator)
	roleUser      = string(db.UserRoleUser)
)

// Store is the database access the service needs. *db.Queries satisfies it.
type Store interface {
	AdminActorRole(ctx context.Context, id uuid.UUID) (db.UserRole, error)
	AdminListUsers(ctx context.Context, arg db.AdminListUsersParams) ([]db.AdminListUsersRow, error)
	AdminLockUser(ctx context.Context, id uuid.UUID) (db.AdminLockUserRow, error)
	AdminSetRole(ctx context.Context, arg db.AdminSetRoleParams) error
	AdminLogRoleChange(ctx context.Context, arg db.AdminLogRoleChangeParams) error
}

var _ Store = (*db.Queries)(nil)

// Service holds the admin rules.
type Service struct {
	store Store
	// tx runs fn with a store bound to one transaction.
	tx func(ctx context.Context, fn func(Store) error) error
}

// NewService builds the service. inTx is db.InTx bound to the pool.
func NewService(store Store, inTx func(ctx context.Context, fn func(*db.Queries) error) error) *Service {
	return &Service{store: store, tx: func(ctx context.Context, fn func(Store) error) error {
		return inTx(ctx, func(q *db.Queries) error { return fn(q) })
	}}
}

// User is a person as an admin sees them: name, username, role and when they joined. Never the email.
type User struct {
	ID          uuid.UUID
	Username    string
	DisplayName string
	Role        string
	JoinedAt    time.Time
}

func userOf(id uuid.UUID, username, name string, role db.UserRole, at time.Time) User {
	return User{ID: id, Username: username, DisplayName: name, Role: string(role), JoinedAt: at}
}

// requireAdmin is nil only for an admin.
func requireAdmin(ctx context.Context, st Store, user uuid.UUID) error {
	role, err := st.AdminActorRole(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Unauthorized()
	}
	if err != nil {
		return err
	}
	if role != db.UserRoleAdmin {
		return domain.Forbidden("forbidden", "only admins can manage roles")
	}
	return nil
}

// List finds people by name or username (anyone when the text is empty, newest first), or only the moderators and
// admins when filter is "staff". At most 50.
func (s *Service) List(ctx context.Context, admin uuid.UUID, q, filter string) ([]User, error) {
	if filter == "" {
		filter = filterAll
	}
	if filter != filterAll && filter != filterStaff {
		return nil, domain.Invalid("filter must be all or staff")
	}
	if err := requireAdmin(ctx, s.store, admin); err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(q))
	if utf8.RuneCountInString(needle) > maxNeedleLen {
		needle = string([]rune(needle)[:maxNeedleLen])
	}
	rows, err := s.store.AdminListUsers(ctx, db.AdminListUsersParams{Needle: needle, StaffOnly: filter == filterStaff, MaxRows: listRows})
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(rows))
	for _, r := range rows {
		out = append(out, userOf(r.ID, r.Username, r.DisplayName, r.Role, r.CreatedAt))
	}
	return out, nil
}

// SetRole makes someone a moderator or an ordinary user again. It refuses to touch the admin's own role or any
// admin's (those change in the database), and saying what is already so changes and records nothing.
func (s *Service) SetRole(ctx context.Context, admin, target uuid.UUID, role string) (User, error) {
	if role != roleModerator && role != roleUser {
		return User{}, domain.Invalid("role must be user or moderator")
	}
	if admin == target {
		return User{}, domain.Invalid("you can't change your own role")
	}
	var out User
	err := s.tx(ctx, func(st Store) error {
		if err := requireAdmin(ctx, st, admin); err != nil {
			return err
		}
		cur, err := st.AdminLockUser(ctx, target)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound()
		}
		if err != nil {
			return err
		}
		if cur.Role == db.UserRoleAdmin {
			return domain.InvalidCode("admin_role", "an admin's role can only be changed in the database")
		}
		next := db.UserRole(role)
		out = userOf(cur.ID, cur.Username, cur.DisplayName, next, cur.CreatedAt)
		if cur.Role == next {
			return nil
		}
		if err := st.AdminSetRole(ctx, db.AdminSetRoleParams{ID: target, Role: next}); err != nil {
			return err
		}
		return st.AdminLogRoleChange(ctx, db.AdminLogRoleChangeParams{
			AdminID: uuid.NullUUID{UUID: admin, Valid: true}, UserID: target, FromRole: cur.Role, ToRole: next,
		})
	})
	return out, err
}
