package gym_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
	"github.com/smarniw/metamorphosis/internal/gym"
)

type fakeStore struct {
	similar  []db.SimilarGymsRow
	searched db.SearchGymsParams
	checked  *db.SimilarGymsParams
	inserted []db.InsertGymParams
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
		got, err := gym.NewService(f).Search(context.Background(), uuid.New(), "x", c.in)
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
	_, err := gym.NewService(f).Create(context.Background(), uuid.New(), gym.NewGym{
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
		_, err := gym.NewService(&fakeStore{}).Create(context.Background(), uuid.New(), gym.NewGym{Name: n})
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
		_, err := gym.NewService(&fakeStore{}).Create(context.Background(), uuid.New(),
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
	_, err := gym.NewService(f).Create(context.Background(), uuid.New(), gym.NewGym{Name: ptr(" Gold's Gym Aundh "), City: ptr(" Pune ")})
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
	if _, err := gym.NewService(f).Create(context.Background(), uuid.New(), gym.NewGym{Name: ptr("X"), City: ptr("  ")}); err != nil {
		t.Fatal(err)
	}
	if f.checked == nil || f.checked.City != nil || len(f.inserted) != 1 {
		t.Errorf("checked %+v, inserted %d", f.checked, len(f.inserted))
	}
}
