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
	similar  []db.SimilarGymsRow
	searched db.SearchGymsParams
	checked  *db.SimilarGymsParams
	inserted []db.InsertGymParams
	current  db.LockGymRow
	missing  bool
	updates  []db.UpdateGymParams
	edits    []db.InsertGymEditParams
	listed   []db.ListGymEditsRow
	chains   []string
}

func (f *fakeStore) SearchGyms(_ context.Context, a db.SearchGymsParams) ([]db.SearchGymsRow, error) {
	f.searched = a
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

func (f *fakeStore) LockGym(context.Context, uuid.UUID) (db.LockGymRow, error) {
	if f.missing {
		return db.LockGymRow{}, pgx.ErrNoRows
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
	if f.edits[0].UserID.UUID != me {
		t.Fatal("the edit must name the editor")
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
	f := &fakeStore{current: db.LockGymRow{ID: id, Name: "Gold's", Verified: true}}
	err := func() error {
		_, e := gym.NewTestService(f).Update(ctx, uuid.New(), id, gym.Patch{City: ptr("X")})
		return e
	}()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != "gym_locked" {
		t.Fatalf("verified gym: got %v", err)
	}
	f = &fakeStore{current: db.LockGymRow{ID: id, Name: "Mine"}}
	for name, p := range map[string]gym.Patch{
		"blank name":  {Name: ptr("  ")},
		"bad country": {CountryCode: ptr("IND")},
		"bad maps":    {MapsURL: ptr("https://evil.example/x")},
		"long city":   {City: ptr(strings.Repeat("a", 201))},
	} {
		if _, err := gym.NewTestService(f).Update(ctx, uuid.New(), id, p); !errors.As(err, &de) || de.Kind != domain.KindInvalid {
			t.Errorf("%s: got %v, want invalid", name, err)
		}
	}
	if len(f.updates) != 0 {
		t.Fatal("a refused edit must not be saved")
	}
	f.missing = true
	if _, err := gym.NewTestService(f).Update(ctx, uuid.New(), id, gym.Patch{City: ptr("X")}); !errors.As(err, &de) || de.Kind != domain.KindNotFound {
		t.Fatalf("missing gym: got %v", err)
	}
}

func TestDetailAndHistoryNeverNameTheEditor(t *testing.T) {
	id, me := uuid.New(), uuid.New()
	f := &fakeStore{current: db.LockGymRow{ID: id, Name: "Mine"}, listed: []db.ListGymEditsRow{
		{ID: uuid.New(), UserID: uuid.NullUUID{UUID: me, Valid: true}, Changes: []byte(`{"city":{"from":null,"to":"Pune"}}`)},
		{ID: uuid.New(), UserID: uuid.NullUUID{UUID: uuid.New(), Valid: true}, Changes: []byte(`{}`)},
		{ID: uuid.New(), Changes: []byte(`{}`)},
	}}
	s := gym.NewTestService(f)
	d, err := s.Get(context.Background(), me, id)
	if err != nil || !d.Editable || d.Visits != 2 || d.LastVisit == nil {
		t.Fatalf("detail %+v, %v", d, err)
	}
	h, err := s.History(context.Background(), me, id)
	if err != nil || len(h) != 3 || !h[0].ByMe || h[1].ByMe || h[2].ByMe || *h[0].Changes["city"].To != "Pune" {
		t.Fatalf("history %+v, %v", h, err)
	}
}

func (f *fakeStore) ListGymChains(context.Context, int32) ([]string, error) { return f.chains, nil }

func TestChainsIsNeverNull(t *testing.T) {
	got, err := gym.NewTestService(&fakeStore{}).Chains(context.Background())
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}
