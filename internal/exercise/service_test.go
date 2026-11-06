package exercise

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/smarniw/metamorphosis/internal/db"
)

func id(n byte) uuid.UUID { return uuid.UUID{15: n} }

func phr(n byte, name string) phrase { return phrase{id(n), tokens(name)} }

func TestExtractTags(t *testing.T) {
	tags := []phrase{phr(1, "Cable"), phr(2, "EZ Bar"), phr(3, "Bar"), phr(4, "Preacher"), phr(5, "Barbell"), phr(6, "Neutral Grip")}
	for _, c := range []struct {
		name  string
		query string
		found []uuid.UUID
		rest  string
	}{
		{"whole word", "preacher curl", []uuid.UUID{id(4)}, "curl"},
		{"longer tag name wins", "ez bar curl", []uuid.UUID{id(2)}, "curl"},
		{"a word is used once", "ez bar bar curl", []uuid.UUID{id(2), id(3)}, "curl"},
		{"whole words beat partial ones", "bar cable", []uuid.UUID{id(1), id(3)}, ""},
		{"single word tag needs three letters", "pr curl", nil, "pr curl"},
		{"partial single word", "pre curl", []uuid.UUID{id(4)}, "curl"},
		{"shortest name wins among partials", "bar", []uuid.UUID{id(3)}, ""},
		{"partial multi word is anchored by its head", "neutral g curl", []uuid.UUID{id(6)}, "curl"},
		{"head without anchor is no match", "g curl", nil, "g curl"},
		{"partial prefix of two tags: shortest", "barb", []uuid.UUID{id(5)}, ""},
		{"nothing", "squat", nil, "squat"},
	} {
		found, rest := extractTags(tokens(c.query), tags)
		if len(found) != len(c.found) || (len(found) > 0 && !slices.Equal(found, c.found)) {
			t.Errorf("%s: found %v, want %v", c.name, found, c.found)
		}
		if strings.Join(rest, " ") != c.rest {
			t.Errorf("%s: rest %q, want %q", c.name, rest, c.rest)
		}
		if found == nil || rest == nil {
			t.Errorf("%s: found and rest must never be nil", c.name)
		}
	}
}

func TestTokensKeepLettersAndNumbers(t *testing.T) {
	if got := tokens("  Lat-Pull_Down 2x!"); !slices.Equal(got, []string{"lat", "pull", "down", "2x"}) {
		t.Errorf("%q", got)
	}
}

type fakeStore struct {
	byName   map[string][]db.SearchExercisesRow // by normalised query text as passed in
	asked    []string
	suggest  []db.ExercisesSuggestingTagsRow
	links    []db.ExerciseVariationTag
	tagNames []db.TagNamesRow
	maxRows  int32
}

func (f *fakeStore) SearchExercises(_ context.Context, a db.SearchExercisesParams) ([]db.SearchExercisesRow, error) {
	f.asked = append(f.asked, a.Q)
	f.maxRows = a.MaxRows
	return f.byName[a.Q], nil
}

func (f *fakeStore) ExercisesSuggestingTags(context.Context, db.ExercisesSuggestingTagsParams) ([]db.ExercisesSuggestingTagsRow, error) {
	return f.suggest, nil
}

func (f *fakeStore) ExerciseTagLinks(context.Context, db.ExerciseTagLinksParams) ([]db.ExerciseVariationTag, error) {
	return f.links, nil
}
func (f *fakeStore) TagNames(context.Context) ([]db.TagNamesRow, error) { return f.tagNames, nil }
func (f *fakeStore) ListTags(context.Context) ([]db.ListTagsRow, error) { return nil, nil }

func row(n byte, name string, exact bool) db.SearchExercisesRow {
	return db.SearchExercisesRow{ID: id(n), Name: name, Kind: "weight_reps", Status: "official", Exact: exact}
}

func names(es []Exercise) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

func newFake() *fakeStore {
	return &fakeStore{
		byName:   map[string][]db.SearchExercisesRow{},
		tagNames: []db.TagNamesRow{{ID: id(100), Name: "Preacher"}, {ID: id(101), Name: "Cable"}},
	}
}

func TestLimitIsClamped(t *testing.T) {
	for _, c := range []struct {
		in   *int64
		want int32
	}{{nil, 20}, {new(int64), 1}, {ptr(int64(-3)), 1}, {ptr(int64(9)), 9}, {ptr(int64(500)), 50}} {
		f := newFake()
		got, err := NewService(f).Search(context.Background(), uuid.New(), "", c.in)
		if err != nil || got == nil || f.maxRows != c.want {
			t.Errorf("limit %v: %v %v rows=%d want %d", c.in, got, err, f.maxRows, c.want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestAnExactNameIsNotSplitIntoTags(t *testing.T) {
	f := newFake()
	f.byName["preacher curl"] = []db.SearchExercisesRow{row(1, "Preacher Curl", true)}
	got, _ := NewService(f).Search(context.Background(), uuid.New(), "preacher curl", nil)
	if len(f.asked) != 1 || len(got) != 1 || len(got[0].SuggestedTagIDs) != 0 || got[0].SuggestedTagIDs == nil {
		t.Errorf("asked %v, got %+v", f.asked, got)
	}
}

func TestTagWordsAreStrippedAndSuggestedWhereTheCatalogListsThem(t *testing.T) {
	f := newFake()
	f.byName["Preacher Curl"] = []db.SearchExercisesRow{row(3, "Preacher Thing", false)}
	f.byName["curl"] = []db.SearchExercisesRow{row(1, "Bicep Curl", false), row(2, "Hammer Curl", false)}
	f.links = []db.ExerciseVariationTag{{ExerciseID: id(1), TagID: id(100)}}
	got, err := NewService(f).Search(context.Background(), uuid.New(), "Preacher Curl", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The stripped search leads; the full-query hits not already there follow, with no suggestion.
	if !slices.Equal(names(got), []string{"Bicep Curl", "Hammer Curl", "Preacher Thing"}) {
		t.Fatalf("order %v", names(got))
	}
	if !slices.Equal(got[0].SuggestedTagIDs, []uuid.UUID{id(100)}) || len(got[1].SuggestedTagIDs) != 0 || len(got[2].SuggestedTagIDs) != 0 {
		t.Errorf("suggestions %+v", got)
	}
}

func TestAnExactMatchOfTheRemainingWordsGetsEveryFoundTag(t *testing.T) {
	f := newFake()
	f.byName["curl"] = []db.SearchExercisesRow{row(1, "Curl", true)}
	got, _ := NewService(f).Search(context.Background(), uuid.New(), "cable preacher curl", nil)
	if len(got) != 1 || len(got[0].SuggestedTagIDs) != 2 {
		t.Errorf("%+v", got)
	}
}

func TestOnlyTagWordsListNameMatchesThenTagOnlyExercisesByName(t *testing.T) {
	f := newFake()
	f.byName["preacher"] = []db.SearchExercisesRow{row(5, "Preacher Named", false), row(1, "Zed Curl", false)}
	f.suggest = []db.ExercisesSuggestingTagsRow{
		{ID: id(1), Name: "Zed Curl", Kind: "weight_reps", Status: "official"},
		{ID: id(3), Name: "Bicep Curl", Kind: "weight_reps", Status: "official"},
		{ID: id(2), Name: "Arm Curl", Kind: "weight_reps", Status: "official"},
	}
	f.links = []db.ExerciseVariationTag{{ExerciseID: id(1), TagID: id(100)}, {ExerciseID: id(2), TagID: id(100)}, {ExerciseID: id(3), TagID: id(100)}}
	got, _ := NewService(f).Search(context.Background(), uuid.New(), "preacher", nil)
	if !slices.Equal(names(got), []string{"Preacher Named", "Zed Curl", "Arm Curl", "Bicep Curl"}) {
		t.Fatalf("order %v", names(got))
	}
	if len(got[0].SuggestedTagIDs) != 0 || len(got[1].SuggestedTagIDs) != 1 || len(got[2].SuggestedTagIDs) != 1 {
		t.Errorf("suggestions %+v", got)
	}
}

func TestResultsAreCutToTheLimit(t *testing.T) {
	f := newFake()
	f.byName["preacher"] = []db.SearchExercisesRow{row(5, "A", false), row(6, "B", false)}
	f.suggest = []db.ExercisesSuggestingTagsRow{{ID: id(7), Name: "C"}}
	got, _ := NewService(f).Search(context.Background(), uuid.New(), "preacher", ptr(int64(2)))
	if len(got) != 2 {
		t.Errorf("%v", names(got))
	}
}

func TestQueryWithoutTagsReturnsTheFullSearch(t *testing.T) {
	f := newFake()
	f.byName["squat"] = []db.SearchExercisesRow{row(1, "Squat", false)}
	got, _ := NewService(f).Search(context.Background(), uuid.New(), "squat", nil)
	if !slices.Equal(names(got), []string{"Squat"}) || got[0].SuggestedTagIDs == nil {
		t.Errorf("%+v", got)
	}
}
