package gym_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
	"github.com/smarniw/metamorphosis/internal/gym"
)

type fakeStore struct {
	similar   []db.SimilarGymsRow
	searched  db.SearchGymsParams
	listedAll db.ListGymsParams
	checked   *db.SimilarGymsParams
	inserted  []db.InsertGymParams
	current   db.LockGymRow
	missing   bool
	updates   []db.UpdateGymParams
	edits     []db.InsertGymEditParams
	listed    []db.ListGymEditsRow
	chains    []string
	role      db.UserRole // zero value means an ordinary user
	recent    int32
	verified  []db.SetGymVerifiedParams
	deleted   []uuid.UUID
	merged    []db.MarkGymMergedParams
	moved     []db.MoveGymWorkoutsParams
	userGyms  []db.MoveUserGymsParams
	dropped   []uuid.UUID
	edit      db.GetGymEditRow
	locks     map[uuid.UUID]db.LockGymRow
}

func (f *fakeStore) SearchGyms(_ context.Context, a db.SearchGymsParams) ([]db.SearchGymsRow, error) {
	f.searched = a
	return nil, nil
}

func (f *fakeStore) ListGyms(_ context.Context, a db.ListGymsParams) ([]db.ListGymsRow, error) {
	f.listedAll = a
	return nil, nil
}

func (f *fakeStore) SimilarGyms(_ context.Context, a db.SimilarGymsParams) ([]db.SimilarGymsRow, error) {
	f.checked = &a
	return f.similar, nil
}

func (f *fakeStore) InsertGym(_ context.Context, a db.InsertGymParams) (db.InsertGymRow, error) {
	f.inserted = append(f.inserted, a)
	return db.InsertGymRow{ID: uuid.New(), Name: a.Name, City: a.City, MapsUrl: a.MapsUrl}, nil
}

func ptr[T any](v T) *T { return &v }

func TestLimitIsClampedToOneThroughFifty(t *testing.T) {
	for _, c := range []struct {
		in   *int64
		want int32
	}{{nil, 20}, {ptr(int64(0)), 1}, {ptr(int64(-9)), 1}, {ptr(int64(7)), 7}, {ptr(int64(500)), 50}} {
		f := &fakeStore{}
		got, err := gym.NewTestService(f).Search(context.Background(), uuid.New(), "x", c.in)
		if err != nil || got == nil {
			t.Fatalf("Search: %v, %v (an empty result must be an empty slice, not nil)", got, err)
		}
		if f.searched.MaxRows != c.want {
			t.Errorf("limit %v: asked for %d rows, want %d", c.in, f.searched.MaxRows, c.want)
		}
	}
}

func TestCreateTrimsAndBlanksFields(t *testing.T) {
	f := &fakeStore{}
	_, err := gym.NewTestService(f).Create(context.Background(), uuid.New(), gym.NewGym{
		Name: ptr("  Iron Works "), Chain: ptr("  "), Area: ptr(" Baner "), Address: ptr(""), City: ptr("\t"), Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	g := f.inserted[0]
	if g.Name != "Iron Works" || g.Chain != nil || g.Address != nil || g.City != nil || g.MapsUrl != nil || *g.Area != "Baner" {
		t.Errorf("stored %+v", g)
	}
	if f.checked != nil {
		t.Error("force must skip the duplicate check")
	}
}

func TestCreateRefusesBlankNames(t *testing.T) {
	for _, n := range []*string{nil, ptr(""), ptr("  \t")} {
		_, err := gym.NewTestService(&fakeStore{}).Create(context.Background(), uuid.New(), gym.NewGym{Name: n})
		var de *domain.Error
		if !errors.As(err, &de) || de.Kind != domain.KindInvalid || de.Message != "name must not be empty" {
			t.Errorf("name %v: %v", n, err)
		}
	}
}

func TestMapsURLAllowList(t *testing.T) {
	good := []string{
		"https://maps.app.goo.gl/abc", "https://goo.gl/maps/abc", "https://maps.apple.com/?q=x",
		"https://www.openstreetmap.org/node/1", "https://maps.google.com/?q=x", "https://google.com/maps/place/x",
		"https://www.google.com/maps/place/Iron+Works", "https://maps.app.goo.gl",
	}
	bad := []string{
		"http://maps.app.goo.gl/x", "https://evil.example/maps", "https://maps.app.goo.gl.evil.example/x",
		"https://www.google.com/search?q=x", "https://goo.gl/other", "https://user@maps.app.goo.gl/x",
		"https://maps.app.goo.gl/has space", "javascript:alert(1)", "https://maps.app.goo.gl/tab\there",
		"HTTPS://maps.app.goo.gl/x",
	}
	for _, u := range append(append([]string{}, good...), bad...) {
		_, err := gym.NewTestService(&fakeStore{}).Create(context.Background(), uuid.New(),
			gym.NewGym{Name: ptr("G"), MapsURL: ptr(u), Force: true})
		isGood := false
		for _, g := range good {
			isGood = isGood || g == u
		}
		if (err == nil) != isGood {
			t.Errorf("%q: accepted=%v, want %v (%v)", u, err == nil, isGood, err)
		}
	}
}

func TestDuplicateGuardRefusesWithTheCandidates(t *testing.T) {
	existing := db.SimilarGymsRow{ID: uuid.New(), Name: "Golds Gym Aundh", Verified: true}
	f := &fakeStore{similar: []db.SimilarGymsRow{existing}}
	_, err := gym.NewTestService(f).Create(context.Background(), uuid.New(), gym.NewGym{Name: ptr(" Gold's Gym Aundh "), City: ptr(" Pune ")})
	var de *domain.Error
	if !errors.As(err, &de) || de.Kind != domain.KindConflict || de.Message != "similar gyms exist" || de.Code != "" {
		t.Fatalf("err = %v", err)
	}
	similar, ok := de.Extra["similar"].([]gym.Gym)
	if !ok || len(similar) != 1 || similar[0].ID != existing.ID || !similar[0].Verified {
		t.Errorf("similar = %#v", de.Extra["similar"])
	}
	if len(f.inserted) != 0 {
		t.Error("a refused create must not insert")
	}
	if f.checked.Name != "Gold's Gym Aundh" || *f.checked.City != "Pune" || f.checked.MaxRows != gym.MaxSimilar {
		t.Errorf("guard asked %+v", f.checked)
	}
}

func TestDuplicateGuardWithoutCityComparesEveryCity(t *testing.T) {
	f := &fakeStore{}
	if _, err := gym.NewTestService(f).Create(context.Background(), uuid.New(), gym.NewGym{Name: ptr("X"), City: ptr("  ")}); err != nil {
		t.Fatal(err)
	}
	if f.checked == nil || f.checked.City != nil || len(f.inserted) != 1 {
		t.Errorf("checked %+v, inserted %d", f.checked, len(f.inserted))
	}
}

func (f *fakeStore) GetGym(context.Context, db.GetGymParams) (db.GetGymRow, error) {
	if f.missing {
		return db.GetGymRow{}, pgx.ErrNoRows
	}
	return db.GetGymRow{ID: f.current.ID, Name: f.current.Name, Verified: f.current.Verified, Visits: 2, LastVisit: time.Now()}, nil
}

func (f *fakeStore) LockGym(_ context.Context, id uuid.UUID) (db.LockGymRow, error) {
	if f.missing {
		return db.LockGymRow{}, pgx.ErrNoRows
	}
	if r, ok := f.locks[id]; ok {
		return r, nil
	}
	return f.current, nil
}

func (f *fakeStore) UpdateGym(_ context.Context, a db.UpdateGymParams) error {
	f.updates = append(f.updates, a)
	return nil
}

func (f *fakeStore) InsertGymEdit(_ context.Context, a db.InsertGymEditParams) error {
	f.edits = append(f.edits, a)
	return nil
}

func (f *fakeStore) ListGymEdits(context.Context, db.ListGymEditsParams) ([]db.ListGymEditsRow, error) {
	return f.listed, nil
}

func (f *fakeStore) ListGymChains(context.Context, int32) ([]string, error) { return f.chains, nil }

func (f *fakeStore) GymUserRole(context.Context, uuid.UUID) (db.UserRole, error) {
	if f.role == "" {
		return db.UserRoleUser, nil
	}
	return f.role, nil
}

func (f *fakeStore) CountRecentGymEdits(context.Context, db.CountRecentGymEditsParams) (int32, error) {
	return f.recent, nil
}

func (f *fakeStore) SetGymVerified(_ context.Context, a db.SetGymVerifiedParams) error {
	f.verified = append(f.verified, a)
	return nil
}

func (f *fakeStore) SoftDeleteGym(_ context.Context, id uuid.UUID) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeStore) MoveGymWorkouts(_ context.Context, a db.MoveGymWorkoutsParams) (int64, error) {
	f.moved = append(f.moved, a)
	return 3, nil
}

func (f *fakeStore) MoveUserGyms(_ context.Context, a db.MoveUserGymsParams) error {
	f.userGyms = append(f.userGyms, a)
	return nil
}

func (f *fakeStore) DropUserGyms(_ context.Context, id uuid.UUID) error {
	f.dropped = append(f.dropped, id)
	return nil
}

func (f *fakeStore) MarkGymMerged(_ context.Context, a db.MarkGymMergedParams) error {
	f.merged = append(f.merged, a)
	return nil
}

func (f *fakeStore) GetGymEdit(context.Context, db.GetGymEditParams) (db.GetGymEditRow, error) {
	if f.edit.ID == uuid.Nil {
		return db.GetGymEditRow{}, pgx.ErrNoRows
	}
	return f.edit, nil
}

func kindOf(err error) domain.Kind {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Kind
	}
	return 0
}

func codeOf(err error) string {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Code
	}
	return ""
}

func TestWhatEachRoleMayDo(t *testing.T) {
	type want struct{ edit, verify, revert, merge, del bool }
	for _, c := range []struct {
		role     gym.Role
		verified bool
		want     want
	}{
		{gym.RoleUser, false, want{edit: true}},
		{gym.RoleUser, true, want{}},
		{gym.RoleModerator, false, want{edit: true, verify: true, revert: true, merge: true}},
		{gym.RoleModerator, true, want{edit: true, verify: true, revert: true, merge: true}},
		{gym.RoleAdmin, false, want{true, true, true, true, true}},
		{gym.RoleAdmin, true, want{true, true, true, true, true}},
	} {
		a := gym.AbilitiesFor(c.role, c.verified)
		got := want{a.Edit, a.Verify, a.Revert, a.Merge, a.Delete}
		if got != c.want {
			t.Errorf("%s verified=%v: got %+v, want %+v", c.role, c.verified, got, c.want)
		}
	}
}

func TestEditChangesOnlyWhatWasSentAndRecordsIt(t *testing.T) {
	id, me := uuid.New(), uuid.New()
	f := &fakeStore{current: db.LockGymRow{ID: id, Name: "Iron Works", City: ptr("Pune"), Chain: ptr("Cult")}}
	got, err := gym.NewTestService(f).Update(context.Background(), me, id, gym.Patch{
		City: ptr(" Pune City "), Chain: ptr("  "), State: ptr("Maharashtra"), CountryCode: ptr("in"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Iron Works" || *got.City != "Pune City" || got.Chain != nil || *got.State != "Maharashtra" || *got.CountryCode != "IN" {
		t.Fatalf("got %+v", got)
	}
	u := f.updates[0]
	if u.Name != "Iron Works" || *u.City != "Pune City" || u.Chain != nil {
		t.Fatalf("saved %+v", u)
	}
	var ch map[string]gym.Change
	if err := json.Unmarshal(f.edits[0].Changes, &ch); err != nil {
		t.Fatal(err)
	}
	if len(ch) != 4 || *ch["city"].From != "Pune" || *ch["city"].To != "Pune City" || ch["chain"].To != nil || *ch["country_code"].To != "IN" {
		t.Fatalf("recorded %+v", ch)
	}
	if f.edits[0].UserID.UUID != me || f.edits[0].Kind != "edit" {
		t.Fatalf("the edit must name the editor and its kind: %+v", f.edits[0])
	}
}

func TestEditThatChangesNothingIsNotRecorded(t *testing.T) {
	id := uuid.New()
	f := &fakeStore{current: db.LockGymRow{ID: id, Name: "Iron Works", City: ptr("Pune")}}
	if _, err := gym.NewTestService(f).Update(context.Background(), uuid.New(), id, gym.Patch{Name: ptr(" Iron Works "), City: ptr("Pune")}); err != nil {
		t.Fatal(err)
	}
	if len(f.updates) != 0 || len(f.edits) != 0 {
		t.Fatalf("saved %d, recorded %d", len(f.updates), len(f.edits))
	}
}

func TestEditRefusals(t *testing.T) {
	id := uuid.New()
	ctx := context.Background()
	f := &fakeStore{current: db.LockGymRow{ID: id, Name: "Mine"}}
	for name, p := range map[string]gym.Patch{
		"blank name":  {Name: ptr("  ")},
		"bad country": {CountryCode: ptr("IND")},
		"bad maps":    {MapsURL: ptr("https://evil.example/x")},
		"long city":   {City: ptr(strings.Repeat("a", 201))},
	} {
		if _, err := gym.NewTestService(f).Update(ctx, uuid.New(), id, p); kindOf(err) != domain.KindInvalid {
			t.Errorf("%s: got %v, want invalid", name, err)
		}
	}
	if len(f.updates) != 0 {
		t.Fatal("a refused edit must not be saved")
	}
	f.missing = true
	if _, err := gym.NewTestService(f).Update(ctx, uuid.New(), id, gym.Patch{City: ptr("X")}); kindOf(err) != domain.KindNotFound {
		t.Fatalf("missing gym: got %v", err)
	}
}

func TestOnlyStaffEditVerifiedGyms(t *testing.T) {
	id := uuid.New()
	ctx := context.Background()
	for _, c := range []struct {
		role db.UserRole
		ok   bool
	}{{db.UserRoleUser, false}, {db.UserRoleModerator, true}, {db.UserRoleAdmin, true}} {
		f := &fakeStore{role: c.role, current: db.LockGymRow{ID: id, Name: "Gold's", Verified: true}}
		_, err := gym.NewTestService(f).Update(ctx, uuid.New(), id, gym.Patch{City: ptr("X")})
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.role, err)
		}
		if !c.ok && (kindOf(err) != domain.KindForbidden || codeOf(err) != "gym_locked") {
			t.Errorf("%s: got %v, want forbidden gym_locked", c.role, err)
		}
		if !c.ok && len(f.updates) != 0 {
			t.Errorf("%s: a refused edit must not be saved", c.role)
		}
	}
}

func TestOrdinaryUsersAreRateLimitedButStaffAreNot(t *testing.T) {
	id := uuid.New()
	ctx := context.Background()
	f := &fakeStore{recent: 30, current: db.LockGymRow{ID: id, Name: "Mine"}}
	if _, err := gym.NewTestService(f).Update(ctx, uuid.New(), id, gym.Patch{City: ptr("X")}); kindOf(err) != domain.KindRateLimited {
		t.Fatalf("the 31st edit of the hour: got %v", err)
	}
	f.recent = 29
	if _, err := gym.NewTestService(f).Update(ctx, uuid.New(), id, gym.Patch{City: ptr("X")}); err != nil {
		t.Fatalf("the 30th: %v", err)
	}
	f = &fakeStore{recent: 500, role: db.UserRoleModerator, current: db.LockGymRow{ID: id, Name: "Mine"}}
	if _, err := gym.NewTestService(f).Update(ctx, uuid.New(), id, gym.Patch{City: ptr("X")}); err != nil {
		t.Fatalf("a moderator is not limited: %v", err)
	}
}

func TestVerifyIsForStaffAndIdempotent(t *testing.T) {
	id, me := uuid.New(), uuid.New()
	ctx := context.Background()
	f := &fakeStore{current: db.LockGymRow{ID: id, Name: "G"}}
	if _, err := gym.NewTestService(f).SetVerified(ctx, me, id, true); kindOf(err) != domain.KindForbidden {
		t.Fatalf("a user may not verify: %v", err)
	}
	f.role = db.UserRoleModerator
	g, err := gym.NewTestService(f).SetVerified(ctx, me, id, true)
	if err != nil || !g.Verified {
		t.Fatalf("got %+v, %v", g, err)
	}
	if v := f.verified[0]; !v.Verified || v.By.UUID != me || f.edits[0].Kind != "verify" {
		t.Fatalf("saved %+v, recorded %+v", f.verified, f.edits)
	}
	f.current.Verified = true
	if _, err := gym.NewTestService(f).SetVerified(ctx, me, id, true); err != nil || len(f.verified) != 1 || len(f.edits) != 1 {
		t.Fatalf("saying what is already so must do nothing: %v, %d saves, %d records", err, len(f.verified), len(f.edits))
	}
	if g, err := gym.NewTestService(f).SetVerified(ctx, me, id, false); err != nil || g.Verified || f.edits[1].Kind != "unverify" {
		t.Fatalf("unverify: %+v, %v, %+v", g, err, f.edits)
	}
}

func TestOnlyAdminsDelete(t *testing.T) {
	id := uuid.New()
	ctx := context.Background()
	for _, role := range []db.UserRole{db.UserRoleUser, db.UserRoleModerator} {
		f := &fakeStore{role: role, current: db.LockGymRow{ID: id}}
		if err := gym.NewTestService(f).Remove(ctx, uuid.New(), id); kindOf(err) != domain.KindForbidden || len(f.deleted) != 0 {
			t.Errorf("%s: got %v, %d deleted", role, err, len(f.deleted))
		}
	}
	f := &fakeStore{role: db.UserRoleAdmin, current: db.LockGymRow{ID: id}}
	if err := gym.NewTestService(f).Remove(ctx, uuid.New(), id); err != nil || f.deleted[0] != id || f.edits[0].Kind != "delete" {
		t.Fatalf("got %v, %v, %+v", err, f.deleted, f.edits)
	}
	f.missing = true
	if err := gym.NewTestService(f).Remove(ctx, uuid.New(), id); kindOf(err) != domain.KindNotFound {
		t.Fatalf("missing: %v", err)
	}
}

func TestMergeMovesEverythingAndHidesTheDuplicate(t *testing.T) {
	src, dst := uuid.New(), uuid.New()
	ctx := context.Background()
	f := &fakeStore{locks: map[uuid.UUID]db.LockGymRow{src: {ID: src, Name: "Golds Gym Aundh"}, dst: {ID: dst, Name: "Gold's Gym, Aundh"}}}
	if _, err := gym.NewTestService(f).Merge(ctx, uuid.New(), src, dst); kindOf(err) != domain.KindForbidden {
		t.Fatalf("a user may not merge: %v", err)
	}
	f.role = db.UserRoleModerator
	if _, err := gym.NewTestService(f).Merge(ctx, uuid.New(), src, src); kindOf(err) != domain.KindInvalid {
		t.Fatalf("into itself: %v", err)
	}
	g, err := gym.NewTestService(f).Merge(ctx, uuid.New(), src, dst)
	if err != nil || g.ID != dst {
		t.Fatalf("the target must be returned: %+v, %v", g, err)
	}
	if f.moved[0].SourceID.UUID != src || f.moved[0].TargetID.UUID != dst || f.userGyms[0].SourceID != src || f.userGyms[0].TargetID != dst {
		t.Fatalf("moved %+v %+v", f.moved, f.userGyms)
	}
	if f.dropped[0] != src || f.merged[0].ID != src || f.merged[0].TargetID.UUID != dst {
		t.Fatalf("dropped %v merged %+v", f.dropped, f.merged)
	}
	var ch map[string]gym.Change
	_ = json.Unmarshal(f.edits[0].Changes, &ch)
	if f.edits[0].GymID != dst || f.edits[0].Kind != "merge" || *ch["merged_gym"].From != "Golds Gym Aundh" {
		t.Fatalf("recorded on the gym that stays: %+v %+v", f.edits[0], ch)
	}
	f.missing = true
	if _, err := gym.NewTestService(f).Merge(ctx, uuid.New(), src, dst); kindOf(err) != domain.KindNotFound {
		t.Fatalf("missing: %v", err)
	}
}

func TestRevertPutsOldValuesBackOnlyWhenNothingChangedSince(t *testing.T) {
	id, editID, mod := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
	changes := `{"city":{"from":"Pun","to":"Pune"},"chain":{"from":null,"to":"Cult"}}`
	f := &fakeStore{
		role:    db.UserRoleModerator,
		current: db.LockGymRow{ID: id, Name: "G", City: ptr("Pune"), Chain: ptr("Cult"), Verified: true},
		edit:    db.GetGymEditRow{ID: editID, Kind: "edit", Changes: []byte(changes)},
	}
	g, err := gym.NewTestService(f).Revert(ctx, mod, id, editID)
	if err != nil {
		t.Fatal(err)
	}
	if *g.City != "Pun" || g.Chain != nil || !g.Verified {
		t.Fatalf("got %+v", g)
	}
	if u := f.updates[0]; *u.City != "Pun" || u.Chain != nil {
		t.Fatalf("saved %+v", u)
	}
	e := f.edits[0]
	var inv map[string]gym.Change
	_ = json.Unmarshal(e.Changes, &inv)
	if e.Kind != "revert" || e.RevertOf.UUID != editID || *inv["city"].From != "Pune" || *inv["city"].To != "Pun" {
		t.Fatalf("recorded %+v %+v", e, inv)
	}

	// The city was changed again since: undoing the old edit would undo the new one too.
	f = &fakeStore{
		role:    db.UserRoleModerator,
		current: db.LockGymRow{ID: id, Name: "G", City: ptr("Pune City"), Chain: ptr("Cult")},
		edit:    db.GetGymEditRow{ID: editID, Kind: "edit", Changes: []byte(changes)},
	}
	if _, err := gym.NewTestService(f).Revert(ctx, mod, id, editID); kindOf(err) != domain.KindConflict || codeOf(err) != "changed_since" || len(f.updates) != 0 {
		t.Fatalf("got %v, %d saves", err, len(f.updates))
	}
}

func TestRevertRefusals(t *testing.T) {
	id, editID := uuid.New(), uuid.New()
	ctx := context.Background()
	base := func(role db.UserRole, e db.GetGymEditRow) *fakeStore {
		return &fakeStore{role: role, current: db.LockGymRow{ID: id, Name: "G"}, edit: e}
	}
	edit := db.GetGymEditRow{ID: editID, Kind: "edit", Changes: []byte(`{}`)}
	if _, err := gym.NewTestService(base(db.UserRoleUser, edit)).Revert(ctx, uuid.New(), id, editID); kindOf(err) != domain.KindForbidden {
		t.Errorf("a user: %v", err)
	}
	if _, err := gym.NewTestService(base(db.UserRoleModerator, db.GetGymEditRow{ID: editID, Kind: "verify", Changes: []byte(`{}`)})).Revert(ctx, uuid.New(), id, editID); kindOf(err) != domain.KindInvalid {
		t.Errorf("only edits: %v", err)
	}
	if _, err := gym.NewTestService(base(db.UserRoleModerator, db.GetGymEditRow{ID: editID, Kind: "edit", Changes: []byte(`{}`), Reverted: true})).Revert(ctx, uuid.New(), id, editID); codeOf(err) != "already_reverted" {
		t.Errorf("twice: %v", err)
	}
	if _, err := gym.NewTestService(base(db.UserRoleModerator, db.GetGymEditRow{})).Revert(ctx, uuid.New(), id, editID); kindOf(err) != domain.KindNotFound {
		t.Errorf("no such edit: %v", err)
	}
}

func TestDetailTellsWhatTheCallerMayDo(t *testing.T) {
	id, me := uuid.New(), uuid.New()
	ctx := context.Background()
	f := &fakeStore{current: db.LockGymRow{ID: id, Name: "Mine", Verified: true}}
	d, err := gym.NewTestService(f).Get(ctx, me, id)
	if err != nil || d.Can.Edit || d.Can.Delete || d.Visits != 2 || d.LastVisit == nil {
		t.Fatalf("a user on a verified gym: %+v, %v", d, err)
	}
	f.role = db.UserRoleAdmin
	if d, _ = gym.NewTestService(f).Get(ctx, me, id); (d.Can != gym.Abilities{Edit: true, Verify: true, Revert: true, Merge: true, Delete: true}) {
		t.Fatalf("an admin may do everything: %+v", d.Can)
	}
}

func TestHistoryNamesEditorsOnlyToStaff(t *testing.T) {
	id, me := uuid.New(), uuid.New()
	ctx := context.Background()
	name := "Asha"
	rows := []db.ListGymEditsRow{
		{ID: uuid.New(), Kind: "edit", UserID: uuid.NullUUID{UUID: me, Valid: true}, Changes: []byte(`{"city":{"from":null,"to":"Pune"}}`), EditorName: &name},
		{ID: uuid.New(), Kind: "edit", UserID: uuid.NullUUID{UUID: uuid.New(), Valid: true}, Changes: []byte(`{}`), EditorName: &name, Reverted: true},
		{ID: uuid.New(), Kind: "verify", Changes: []byte(`{}`)},
	}
	f := &fakeStore{current: db.LockGymRow{ID: id, Name: "Mine"}, listed: rows}
	h, err := gym.NewTestService(f).History(ctx, me, id)
	if err != nil || len(h) != 3 || !h[0].ByMe || h[1].ByMe || h[2].ByMe || *h[0].Changes["city"].To != "Pune" {
		t.Fatalf("history %+v, %v", h, err)
	}
	for _, e := range h {
		if e.By != nil || e.Revertible {
			t.Fatalf("a user learns nobody's name and reverts nothing: %+v", e)
		}
	}
	f.role = db.UserRoleModerator
	h, _ = gym.NewTestService(f).History(ctx, me, id)
	if h[0].By == nil || *h[0].By != "Asha" || !h[0].Revertible || h[1].Revertible || h[2].Revertible {
		t.Fatalf("staff see names; only live plain edits are revertible: %+v", h)
	}
}

func TestChainsIsNeverNull(t *testing.T) {
	got, err := gym.NewTestService(&fakeStore{}).Chains(context.Background())
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestEmptySearchListsTheDirectoryInsteadOfSearching(t *testing.T) {
	for _, q := range []string{"", "   ", "\t\n"} {
		f := &fakeStore{}
		got, err := gym.NewTestService(f).Search(context.Background(), uuid.New(), q, ptr(int64(7)))
		if err != nil || got == nil {
			t.Fatalf("%q: %v, %v", q, got, err)
		}
		if f.listedAll.MaxRows != 7 || f.searched.Q != "" || f.searched.MaxRows != 0 {
			t.Errorf("%q: listed %+v, searched %+v", q, f.listedAll, f.searched)
		}
	}
}
