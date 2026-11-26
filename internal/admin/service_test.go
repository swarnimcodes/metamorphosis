package admin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smarniw/metamorphosis/internal/admin"
	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
)

type fakeStore struct {
	actor   db.UserRole
	target  db.AdminLockUserRow
	missing bool
	listed  []db.AdminListUsersRow
	asked   db.AdminListUsersParams
	set     []db.AdminSetRoleParams
	logged  []db.AdminLogRoleChangeParams
}

func (f *fakeStore) AdminActorRole(context.Context, uuid.UUID) (db.UserRole, error) {
	if f.actor == "" {
		return db.UserRoleUser, nil
	}
	return f.actor, nil
}

func (f *fakeStore) AdminListUsers(_ context.Context, a db.AdminListUsersParams) ([]db.AdminListUsersRow, error) {
	f.asked = a
	return f.listed, nil
}

func (f *fakeStore) AdminLockUser(context.Context, uuid.UUID) (db.AdminLockUserRow, error) {
	if f.missing {
		return db.AdminLockUserRow{}, pgx.ErrNoRows
	}
	return f.target, nil
}

func (f *fakeStore) AdminSetRole(_ context.Context, a db.AdminSetRoleParams) error {
	f.set = append(f.set, a)
	return nil
}

func (f *fakeStore) AdminLogRoleChange(_ context.Context, a db.AdminLogRoleChangeParams) error {
	f.logged = append(f.logged, a)
	return nil
}

// service's "transaction" just runs fn on the store.
func service(f *fakeStore) *admin.Service { return admin.NewTestService(f) }

func kind(err error) domain.Kind {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Kind
	}
	return 0
}

func code(err error) string {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Code
	}
	return ""
}

func TestOnlyAdminsMayListOrChangeRoles(t *testing.T) {
	ctx, me, them := context.Background(), uuid.New(), uuid.New()
	for _, role := range []db.UserRole{db.UserRoleUser, db.UserRoleModerator} {
		f := &fakeStore{actor: role, target: db.AdminLockUserRow{ID: them, Role: db.UserRoleUser}}
		if _, err := service(f).List(ctx, me, "", ""); kind(err) != domain.KindForbidden {
			t.Errorf("%s listing: %v", role, err)
		}
		if _, err := service(f).SetRole(ctx, me, them, "moderator"); kind(err) != domain.KindForbidden || len(f.set) != 0 {
			t.Errorf("%s changing a role: %v, %d saved", role, err, len(f.set))
		}
	}
}

func TestSetRoleRefusals(t *testing.T) {
	ctx, me, them := context.Background(), uuid.New(), uuid.New()
	f := &fakeStore{actor: db.UserRoleAdmin, target: db.AdminLockUserRow{ID: them, Role: db.UserRoleUser}}
	for _, bad := range []string{"", "admin", "root", "Moderator"} {
		if _, err := service(f).SetRole(ctx, me, them, bad); kind(err) != domain.KindInvalid {
			t.Errorf("role %q: %v", bad, err)
		}
	}
	if _, err := service(f).SetRole(ctx, me, me, "user"); kind(err) != domain.KindInvalid {
		t.Errorf("your own role: %v", err)
	}
	f.target.Role = db.UserRoleAdmin
	if _, err := service(f).SetRole(ctx, me, them, "user"); kind(err) != domain.KindInvalid || code(err) != "admin_role" {
		t.Errorf("an admin's role: %v", err)
	}
	f.missing = true
	if _, err := service(f).SetRole(ctx, me, them, "moderator"); kind(err) != domain.KindNotFound {
		t.Errorf("unknown person: %v", err)
	}
	if len(f.set) != 0 || len(f.logged) != 0 {
		t.Fatalf("a refused change must not be saved or logged: %d, %d", len(f.set), len(f.logged))
	}
}

func TestSetRoleSavesAndRecordsOnlyRealChanges(t *testing.T) {
	ctx, me, them := context.Background(), uuid.New(), uuid.New()
	f := &fakeStore{actor: db.UserRoleAdmin, target: db.AdminLockUserRow{ID: them, Username: "asha", DisplayName: "Asha", Role: db.UserRoleUser, CreatedAt: time.Now()}}
	u, err := service(f).SetRole(ctx, me, them, "moderator")
	if err != nil || u.Role != "moderator" || u.Username != "asha" {
		t.Fatalf("got %+v, %v", u, err)
	}
	if f.set[0].Role != db.UserRoleModerator || f.set[0].ID != them {
		t.Fatalf("saved %+v", f.set)
	}
	if l := f.logged[0]; l.AdminID.UUID != me || l.UserID != them || l.FromRole != db.UserRoleUser || l.ToRole != db.UserRoleModerator {
		t.Fatalf("recorded %+v", l)
	}
	// Saying what is already so does nothing.
	f.target.Role = db.UserRoleModerator
	if u, err = service(f).SetRole(ctx, me, them, "moderator"); err != nil || u.Role != "moderator" || len(f.set) != 1 || len(f.logged) != 1 {
		t.Fatalf("no-op: %+v, %v, %d saved, %d logged", u, err, len(f.set), len(f.logged))
	}
	// And back down.
	if _, err = service(f).SetRole(ctx, me, them, "user"); err != nil || f.logged[1].FromRole != db.UserRoleModerator || f.logged[1].ToRole != db.UserRoleUser {
		t.Fatalf("demote: %v, %+v", err, f.logged)
	}
}

func TestListFiltersAndAsksTheRightQuestion(t *testing.T) {
	ctx, me := context.Background(), uuid.New()
	f := &fakeStore{actor: db.UserRoleAdmin, listed: []db.AdminListUsersRow{{ID: uuid.New(), Username: "n1k", DisplayName: "Nikith", Role: db.UserRoleUser}}}
	got, err := service(f).List(ctx, me, "  RuCh ", "staff")
	if err != nil || len(got) != 1 || got[0].Username != "n1k" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if f.asked.Needle != "ruch" || !f.asked.StaffOnly || f.asked.MaxRows != 50 {
		t.Fatalf("asked %+v", f.asked)
	}
	if _, err := service(f).List(ctx, me, "", "everyone"); kind(err) != domain.KindInvalid {
		t.Fatalf("bad filter: %v", err)
	}
	if got, _ = service(&fakeStore{actor: db.UserRoleAdmin}).List(ctx, me, "", ""); got == nil {
		t.Fatal("an empty result must be an empty list, not nil")
	}
}
