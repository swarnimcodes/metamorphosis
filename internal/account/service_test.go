package account

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smarniw/metamorphosis/internal/auth"
	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
	"github.com/smarniw/metamorphosis/internal/google"
)

type fakeStore struct {
	username   string // the one existing account's username; "" means none
	user       uuid.UUID
	taken      map[string]bool
	zones      map[string]bool
	identities map[string]bool // google subjects already registered
	known      *db.TouchGoogleLoginRow

	deleted    bool
	inserted   []db.InsertUserParams
	identityIn []db.InsertGoogleIdentityParams
	updated    db.UpdateMeParams
	insertErr  error
	failIDLink error
}

func (f *fakeStore) GetMe(context.Context, uuid.UUID) (db.GetMeRow, error) {
	return db.GetMeRow{}, pgx.ErrNoRows
}
func (f *fakeStore) UpdateMe(_ context.Context, a db.UpdateMeParams) (db.UpdateMeRow, error) {
	f.updated = a
	return db.UpdateMeRow{ID: a.ID, UnitSystem: db.UnitSystemMetric}, nil
}
func (f *fakeStore) TimezoneKnown(_ context.Context, n *string) (bool, error) {
	return f.zones[*n], nil
}
func (f *fakeStore) GetUsername(context.Context, uuid.UUID) (string, error) {
	if f.username == "" {
		return "", pgx.ErrNoRows
	}
	return f.username, nil
}
func (f *fakeStore) LockUserForDelete(context.Context, uuid.UUID) (string, error) {
	return f.GetUsername(context.Background(), uuid.Nil)
}
func (f *fakeStore) DeleteUser(context.Context, uuid.UUID) error { f.deleted = true; return nil }
func (f *fakeStore) UpsertDevUser(_ context.Context, a db.UpsertDevUserParams) (db.UpsertDevUserRow, error) {
	return db.UpsertDevUserRow{ID: f.user, Username: a.Username}, nil
}
func (f *fakeStore) UsernameTaken(_ context.Context, u string) (bool, error) { return f.taken[u], nil }
func (f *fakeStore) TouchGoogleLogin(context.Context, string) (db.TouchGoogleLoginRow, error) {
	if f.known == nil {
		return db.TouchGoogleLoginRow{}, pgx.ErrNoRows
	}
	return *f.known, nil
}
func (f *fakeStore) GoogleIdentityExists(_ context.Context, s string) (bool, error) {
	return f.identities[s], nil
}
func (f *fakeStore) InsertUser(_ context.Context, a db.InsertUserParams) (uuid.UUID, error) {
	if f.insertErr != nil {
		return uuid.Nil, f.insertErr
	}
	f.inserted = append(f.inserted, a)
	return f.user, nil
}
func (f *fakeStore) InsertGoogleIdentity(_ context.Context, a db.InsertGoogleIdentityParams) error {
	f.identityIn = append(f.identityIn, a)
	return f.failIDLink
}

type fakeSessions struct{ id google.Identity }

func (fakeSessions) Issue(context.Context, uuid.UUID, *string) (auth.Session, error) {
	return auth.Session{AccessToken: "a", RefreshToken: "r"}, nil
}
func (fakeSessions) Rotate(_ context.Context, tok string, _ *string) (uuid.UUID, auth.Session, error) {
	if tok != "good" {
		return uuid.Nil, auth.Session{}, domain.Unauthorized()
	}
	return uuid.Nil, auth.Session{AccessToken: "a2"}, nil
}
func (fakeSessions) IssueSignupToken(google.Identity) (string, error)    { return "signup-token", nil }
func (f fakeSessions) VerifySignupToken(string) (google.Identity, error) { return f.id, nil }

type fakeGoogle struct {
	id  google.Identity
	err error
}

func (g fakeGoogle) Verify(context.Context, string) (google.Identity, error) { return g.id, g.err }

// txRolledBack records whether the transaction function failed, standing in for a rollback.
func newSvc(st *fakeStore, ss fakeSessions, g GoogleVerifier) (*Service, *bool) {
	rolledBack := false
	inTx := func(_ context.Context, fn func(Store) error) error {
		err := fn(st)
		rolledBack = err != nil
		return err
	}
	s := New(st, inTx, ss, g)
	s.randN = func(int) int { return 0 }
	s.newID = func() string { return strings.Repeat("0", 26) + "abcdef" }
	return s, &rolledBack
}

func kind(t *testing.T, err error) (domain.Kind, string, string) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("want a domain error, got %v", err)
	}
	return de.Kind, de.Code, de.Message
}

func TestCheckUsername(t *testing.T) {
	cases := map[string]UsernameProblem{
		"abc": UsernameOK, "a_b_9": UsernameOK, strings.Repeat("a", 30): UsernameOK,
		"ab": UsernameInvalid, strings.Repeat("a", 31): UsernameInvalid, "Abc": UsernameInvalid,
		"a-b": UsernameInvalid, "a b": UsernameInvalid, "": UsernameInvalid, "é_é": UsernameInvalid,
		"admin": UsernameReserved, "www": UsernameReserved, "me_": UsernameOK, "me": UsernameInvalid,
		"api": UsernameReserved,
	}
	for in, want := range cases {
		if got := CheckUsername(in); got != want {
			t.Errorf("CheckUsername(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAvailability(t *testing.T) {
	s, _ := newSvc(&fakeStore{taken: map[string]bool{"bob_1": true}}, fakeSessions{}, nil)
	for in, want := range map[string]string{" BOB_1 ": "taken", "bob_2": "", "x": "invalid", "Admin": "reserved"} {
		got, err := s.Availability(context.Background(), in)
		if err != nil || got != want {
			t.Errorf("Availability(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestBaseUsername(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		id   google.Identity
		want string
	}{
		{google.Identity{Name: str("Rahul Sharma"), Email: "x@y.z"}, "rahul_sharma"},
		{google.Identity{Name: str("  José--Álvarez "), Email: "x@y.z"}, "jos_lvarez"},
		{google.Identity{Name: str("Al"), Email: "alice.smith@gmail.com"}, "alice_smith"},
		{google.Identity{Email: "ab@gmail.com"}, "lifter"},
		{google.Identity{Name: str(strings.Repeat("a", 40)), Email: "x@y.z"}, strings.Repeat("a", 24)},
	}
	for _, c := range cases {
		if got := baseUsername(c.id); got != c.want {
			t.Errorf("baseUsername(%v) = %q, want %q", c.id, got, c.want)
		}
	}
}

func TestSuggestUsernameSkipsTakenAndFallsBack(t *testing.T) {
	id := google.Identity{Email: "sam@x.y"}
	s, _ := newSvc(&fakeStore{taken: map[string]bool{"sam": true}}, fakeSessions{}, nil)
	got, _ := s.suggestUsername(context.Background(), id)
	if got != "sam10" { // randN returns 0, so the suffix is 10
		t.Errorf("got %q, want sam10", got)
	}

	everything := &fakeStore{taken: map[string]bool{"sam": true, "sam10": true}}
	s, _ = newSvc(everything, fakeSessions{}, nil)
	got, _ = s.suggestUsername(context.Background(), id)
	if got != "samabcdef" {
		t.Errorf("fallback = %q, want samabcdef", got)
	}
}

func TestUpdateProfileRules(t *testing.T) {
	st := &fakeStore{zones: map[string]bool{"Asia/Kolkata": true}}
	s, _ := newSvc(st, fakeSessions{}, nil)
	ctx := context.Background()
	str := func(v string) *string { return &v }

	for _, c := range []struct {
		in  ProfileChanges
		msg string
	}{
		{ProfileChanges{DisplayName: str("   ")}, "display_name must be 1-80 characters"},
		{ProfileChanges{DisplayName: str(strings.Repeat("a", 81))}, "display_name must be 1-80 characters"},
		{ProfileChanges{UnitSystem: str("furlongs")}, "unit_system must be metric or imperial"},
		{ProfileChanges{Timezone: str("Mars/Olympus")}, "unknown timezone"},
	} {
		_, err := s.UpdateProfile(ctx, uuid.New(), c.in)
		if k, _, msg := kind(t, err); k != domain.KindInvalid || msg != c.msg {
			t.Errorf("%+v: got %v %q, want invalid %q", c.in, k, msg, c.msg)
		}
	}

	// 80 multi-byte characters are fine (counted in characters), and the name is stored trimmed.
	name := " " + strings.Repeat("é", 80) + " "
	if _, err := s.UpdateProfile(ctx, uuid.New(), ProfileChanges{DisplayName: &name, UnitSystem: str("imperial"), Timezone: str("Asia/Kolkata")}); err != nil {
		t.Fatal(err)
	}
	if *st.updated.DisplayName != strings.Repeat("é", 80) || *st.updated.UnitSystem != db.UnitSystemImperial {
		t.Errorf("stored %+v", st.updated)
	}
	// Nothing to change passes nils through.
	if _, err := s.UpdateProfile(ctx, uuid.New(), ProfileChanges{}); err != nil || st.updated.DisplayName != nil || st.updated.Timezone != nil {
		t.Errorf("empty patch: %v %+v", err, st.updated)
	}
}

func TestDeleteNeedsTheUsernameConfirmed(t *testing.T) {
	st := &fakeStore{username: "leaver"}
	s, _ := newSvc(st, fakeSessions{}, nil)
	ctx := context.Background()

	for _, wrong := range []string{"", "stayer", "leaver2"} {
		k, code, msg := kind(t, s.Delete(ctx, uuid.New(), wrong))
		if k != domain.KindInvalid || code != "username_mismatch" || msg != "the username does not match this account" {
			t.Errorf("%q: got %v %q %q", wrong, k, code, msg)
		}
	}
	if st.deleted {
		t.Fatal("a wrong confirmation deleted the account")
	}
	if err := s.Delete(ctx, uuid.New(), "  LEAVER "); err != nil || !st.deleted {
		t.Fatalf("matching confirmation: err=%v deleted=%v", err, st.deleted)
	}

	gone, _ := newSvc(&fakeStore{}, fakeSessions{}, nil)
	if k, _, _ := kind(t, gone.Delete(ctx, uuid.New(), "x")); k != domain.KindUnauthorized {
		t.Errorf("deleted account: got %v, want unauthorized", k)
	}
}

func TestMeOfAMissingAccountIsUnauthorized(t *testing.T) {
	s, _ := newSvc(&fakeStore{}, fakeSessions{}, nil)
	_, err := s.Me(context.Background(), uuid.New())
	if k, _, _ := kind(t, err); k != domain.KindUnauthorized {
		t.Errorf("got %v", k)
	}
}

func TestRefresh(t *testing.T) {
	s, _ := newSvc(&fakeStore{username: "rita"}, fakeSessions{}, nil)
	l, err := s.Refresh(context.Background(), "good", nil)
	if err != nil || l.Username != "rita" || l.Session.AccessToken != "a2" {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := s.Refresh(context.Background(), "bad", nil); err == nil {
		t.Fatal("a bad refresh token was accepted")
	}
	gone, _ := newSvc(&fakeStore{}, fakeSessions{}, nil)
	_, err = gone.Refresh(context.Background(), "good", nil)
	if k, _, _ := kind(t, err); k != domain.KindUnauthorized {
		t.Errorf("deleted account: got %v", k)
	}
}

func TestDevLoginNormalizesTheUsername(t *testing.T) {
	s, _ := newSvc(&fakeStore{}, fakeSessions{}, nil)
	l, err := s.DevLogin(context.Background(), "  BoB_1 ", nil, nil)
	if err != nil || l.Username != "bob_1" {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestGoogleSignInOutcomes(t *testing.T) {
	name := "Rahul Sharma"
	id := google.Identity{Sub: "s1", Email: "rahul@x.y", Name: &name}
	ctx := context.Background()

	s, _ := newSvc(&fakeStore{known: &db.TouchGoogleLoginRow{ID: uuid.New(), Username: "rahul"}}, fakeSessions{}, fakeGoogle{id: id})
	if r, err := s.GoogleSignIn(ctx, "t", nil); err != nil || r.Login == nil || r.Login.Username != "rahul" || r.Signup != nil {
		t.Fatalf("known account: %+v %v", r, err)
	}

	s, _ = newSvc(&fakeStore{}, fakeSessions{}, fakeGoogle{id: id})
	r, err := s.GoogleSignIn(ctx, "t", nil)
	if err != nil || r.Signup == nil || r.Login != nil {
		t.Fatalf("new account: %+v %v", r, err)
	}
	if o := r.Signup; o.SuggestedUsername != "rahul_sharma" || o.DisplayName != "Rahul Sharma" || o.Email != "rahul@x.y" || o.Token != "signup-token" {
		t.Errorf("offer %+v", o)
	}

	s, _ = newSvc(&fakeStore{}, fakeSessions{}, fakeGoogle{err: google.ErrInvalid})
	_, err = s.GoogleSignIn(ctx, "t", nil)
	if k, _, _ := kind(t, err); k != domain.KindUnauthorized {
		t.Errorf("invalid token: %v", k)
	}
	s, _ = newSvc(&fakeStore{}, fakeSessions{}, fakeGoogle{err: google.ErrKeysUnavailable})
	_, err = s.GoogleSignIn(ctx, "t", nil)
	if k, _, _ := kind(t, err); k != domain.KindUpstream {
		t.Errorf("keys unavailable: %v", k)
	}
}

func TestGoogleSignup(t *testing.T) {
	ctx := context.Background()
	name := "Given Name"
	ident := google.Identity{Sub: "s1", Email: "a@b.c", Name: &name}
	str := func(v string) *string { return &v }

	t.Run("creates the user and identity together", func(t *testing.T) {
		st := &fakeStore{user: uuid.New()}
		s, _ := newSvc(st, fakeSessions{id: ident}, nil)
		l, err := s.GoogleSignup(ctx, "tok", " New_User ", nil, nil)
		if err != nil || l.Username != "new_user" || l.UserID != st.user {
			t.Fatalf("%+v %v", l, err)
		}
		if len(st.inserted) != 1 || st.inserted[0].DisplayName != "Given Name" || len(st.identityIn) != 1 || st.identityIn[0].Subject != "s1" {
			t.Errorf("inserted %+v / %+v", st.inserted, st.identityIn)
		}
	})
	t.Run("a chosen display name wins and is trimmed", func(t *testing.T) {
		st := &fakeStore{}
		s, _ := newSvc(st, fakeSessions{id: ident}, nil)
		if _, err := s.GoogleSignup(ctx, "tok", "newbie", str("  Me  "), nil); err != nil || st.inserted[0].DisplayName != "Me" {
			t.Fatalf("%v %+v", err, st.inserted)
		}
	})
	t.Run("falls back to the username without a Google name", func(t *testing.T) {
		st := &fakeStore{}
		s, _ := newSvc(st, fakeSessions{id: google.Identity{Sub: "s", Email: "a@b.c"}}, nil)
		if _, err := s.GoogleSignup(ctx, "tok", "newbie", str("   "), nil); err != nil || st.inserted[0].DisplayName != "newbie" {
			t.Fatalf("%v %+v", err, st.inserted)
		}
	})
	t.Run("username rules", func(t *testing.T) {
		s, _ := newSvc(&fakeStore{}, fakeSessions{id: ident}, nil)
		for in, code := range map[string]string{"a!": "invalid_username", "admin": "reserved_username"} {
			_, err := s.GoogleSignup(ctx, "tok", in, nil, nil)
			if k, c, _ := kind(t, err); k != domain.KindInvalid || c != code {
				t.Errorf("%q: %v %q", in, k, c)
			}
		}
		_, err := s.GoogleSignup(ctx, "tok", "newbie", str(strings.Repeat("a", 81)), nil)
		if _, _, msg := kind(t, err); msg != "display_name must be at most 80 characters" {
			t.Errorf("long name: %q", msg)
		}
	})
	t.Run("a replayed token conflicts and creates nothing", func(t *testing.T) {
		st := &fakeStore{identities: map[string]bool{"s1": true}}
		s, rolled := newSvc(st, fakeSessions{id: ident}, nil)
		_, err := s.GoogleSignup(ctx, "tok", "newbie", nil, nil)
		if k, c, _ := kind(t, err); k != domain.KindConflict || c != "identity_exists" {
			t.Errorf("%v %q", k, c)
		}
		if len(st.inserted) != 0 || !*rolled {
			t.Errorf("inserted %v, rolled back %v", st.inserted, *rolled)
		}
	})
	t.Run("unique violations become the conflicts the UI knows", func(t *testing.T) {
		for constraint, code := range map[string]string{
			"users_username_key": "username_taken", "users_email_key": "email_in_use",
			"auth_identities_provider_provider_subject_key": "identity_exists",
		} {
			st := &fakeStore{insertErr: &pgconn.PgError{Code: "23505", ConstraintName: constraint}}
			s, _ := newSvc(st, fakeSessions{id: ident}, nil)
			_, err := s.GoogleSignup(ctx, "tok", "newbie", nil, nil)
			if k, c, _ := kind(t, err); k != domain.KindConflict || c != code {
				t.Errorf("%s: %v %q", constraint, k, c)
			}
		}
	})
	t.Run("a failure linking the identity rolls the user back", func(t *testing.T) {
		boom := errors.New("boom")
		st := &fakeStore{failIDLink: boom}
		s, rolled := newSvc(st, fakeSessions{id: ident}, nil)
		if _, err := s.GoogleSignup(ctx, "tok", "newbie", nil, nil); !errors.Is(err, boom) || !*rolled {
			t.Errorf("err=%v rolled=%v", err, *rolled)
		}
	})
}
