package workout_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
	"github.com/smarniw/metamorphosis/internal/workout"
)

// fakeStore implements the few queries a test exercises; calling any other panics through the nil embedded Store.
type fakeStore struct {
	workout.Store
	zones      map[string]bool
	userTZ     string
	lastGym    uuid.NullUUID // invalid: the caller has no workout with a gym
	inserted   []db.InsertWorkoutParams
	nextExPos  int32
	nextSetPos int32
	exercises  []db.InsertWorkoutExerciseParams
	sets       []db.InsertSetParams
	locked     bool
	merged     map[uuid.UUID]uuid.UUID
	unknownEx  bool
	updated    []db.UpdateSetParams
}

func (f *fakeStore) WorkoutTimezoneKnown(_ context.Context, name string) (bool, error) {
	return f.zones[name], nil
}
func (f *fakeStore) WorkoutUserTimezone(context.Context, uuid.UUID) (string, error) {
	return f.userTZ, nil
}
func (f *fakeStore) WorkoutLastGym(context.Context, uuid.UUID) (uuid.NullUUID, error) {
	if !f.lastGym.Valid {
		return uuid.NullUUID{}, pgx.ErrNoRows
	}
	return f.lastGym, nil
}
func (f *fakeStore) InsertWorkout(_ context.Context, a db.InsertWorkoutParams) (uuid.UUID, error) {
	f.inserted = append(f.inserted, a)
	return uuid.New(), nil
}
func (f *fakeStore) GetWorkout(_ context.Context, a db.GetWorkoutParams) (db.GetWorkoutRow, error) {
	return db.GetWorkoutRow{ID: a.ID, StartedAt: time.Now(), Timezone: "UTC"}, nil
}
func (f *fakeStore) ListWorkoutExercises(context.Context, uuid.UUID) ([]db.ListWorkoutExercisesRow, error) {
	return nil, nil
}
func (f *fakeStore) ListWorkoutTags(context.Context, uuid.UUID) ([]db.ListWorkoutTagsRow, error) {
	return nil, nil
}
func (f *fakeStore) ListWorkoutSets(context.Context, uuid.UUID) ([]db.Set, error) { return nil, nil }
func (f *fakeStore) LockWorkout(_ context.Context, a db.LockWorkoutParams) (uuid.UUID, error) {
	f.locked = true
	return a.ID, nil
}
func (f *fakeStore) LockWorkoutExercise(_ context.Context, a db.LockWorkoutExerciseParams) (uuid.UUID, error) {
	f.locked = true
	return a.ID, nil
}
func (f *fakeStore) ResolveWorkoutExercise(_ context.Context, a db.ResolveWorkoutExerciseParams) (uuid.UUID, error) {
	if f.unknownEx {
		return uuid.Nil, pgx.ErrNoRows
	}
	if to, ok := f.merged[a.ID]; ok {
		return to, nil
	}
	return a.ID, nil
}
func (f *fakeStore) NextWorkoutExercisePosition(context.Context, uuid.UUID) (int32, error) {
	return f.nextExPos, nil
}
func (f *fakeStore) InsertWorkoutExercise(_ context.Context, a db.InsertWorkoutExerciseParams) (uuid.UUID, error) {
	f.exercises = append(f.exercises, a)
	return uuid.New(), nil
}
func (f *fakeStore) InsertWorkoutExerciseTags(context.Context, db.InsertWorkoutExerciseTagsParams) error {
	return nil
}
func (f *fakeStore) NextSetPosition(context.Context, uuid.UUID) (int32, error) {
	return f.nextSetPos, nil
}
func (f *fakeStore) InsertSet(_ context.Context, a db.InsertSetParams) (db.Set, error) {
	f.sets = append(f.sets, a)
	return db.Set{ID: uuid.New(), Position: a.Position, SetType: a.SetType, Rir: a.Rir}, nil
}
func (f *fakeStore) UpdateSet(_ context.Context, a db.UpdateSetParams) (db.Set, error) {
	f.updated = append(f.updated, a)
	return db.Set{ID: a.ID}, nil
}

func kind(err error) domain.Kind {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Kind
	}
	return 0
}

func ptr[T any](v T) *T { return &v }

var user = uuid.New()

func TestNewWorkoutStartsAtTheLastGymUnlessOneIsGiven(t *testing.T) {
	last, given := uuid.New(), uuid.New()
	f := &fakeStore{userTZ: "UTC", lastGym: uuid.NullUUID{UUID: last, Valid: true}}
	s := workout.NewTestService(f)

	if _, err := s.Create(context.Background(), user, workout.CreateInput{}); err != nil {
		t.Fatal(err)
	}
	if got := f.inserted[0].GymID; !got.Valid || got.UUID != last {
		t.Fatalf("no gym given should default to the last one, got %+v", got)
	}
	if _, err := s.Create(context.Background(), user, workout.CreateInput{GymID: &given}); err != nil {
		t.Fatal(err)
	}
	if got := f.inserted[1].GymID; got.UUID != given {
		t.Fatalf("a given gym wins, got %+v", got)
	}
	f.lastGym = uuid.NullUUID{}
	if _, err := s.Create(context.Background(), user, workout.CreateInput{}); err != nil {
		t.Fatal(err)
	}
	if f.inserted[2].GymID.Valid {
		t.Fatalf("no previous gym means no gym, got %+v", f.inserted[2].GymID)
	}
}

func TestWorkoutRecordsTheDeviceZoneOrTheAccountsAndRefusesUnknownOnes(t *testing.T) {
	f := &fakeStore{userTZ: "Asia/Kolkata", zones: map[string]bool{"Europe/London": true}}
	s := workout.NewTestService(f)
	ctx := context.Background()

	if _, err := s.Create(ctx, user, workout.CreateInput{}); err != nil {
		t.Fatal(err)
	}
	if got := *f.inserted[0].Timezone; got != "Asia/Kolkata" {
		t.Fatalf("default zone is the account's, got %q", got)
	}
	if _, err := s.Create(ctx, user, workout.CreateInput{Timezone: ptr("Europe/London")}); err != nil {
		t.Fatal(err)
	}
	if got := *f.inserted[1].Timezone; got != "Europe/London" {
		t.Fatalf("device zone wins, got %q", got)
	}
	for _, bad := range []string{"Mars/Olympus", "", "UTC+5"} {
		_, err := s.Create(ctx, user, workout.CreateInput{Timezone: ptr(bad)})
		if kind(err) != domain.KindInvalid || err.Error() != "unknown timezone" {
			t.Fatalf("%q: want invalid unknown timezone, got %v", bad, err)
		}
	}
	if len(f.inserted) != 2 {
		t.Fatalf("a refused zone must not create a workout, %d inserted", len(f.inserted))
	}
}

func TestExercisePositionFollowsTheLockAndMergedExercisesResolve(t *testing.T) {
	old, canonical, wid := uuid.New(), uuid.New(), uuid.New()
	f := &fakeStore{nextExPos: 3, merged: map[uuid.UUID]uuid.UUID{old: canonical}}
	s := workout.NewTestService(f)
	// The fake never returns the new exercise in the workout, so the call ends in not found after the insert.
	_, err := s.AddExercise(context.Background(), user, wid, workout.AddExerciseInput{ExerciseID: &old})
	if kind(err) != domain.KindNotFound {
		t.Fatalf("got %v", err)
	}
	if !f.locked || len(f.exercises) != 1 {
		t.Fatalf("locked=%v inserted=%d", f.locked, len(f.exercises))
	}
	if got := f.exercises[0]; got.Position != 3 || got.ExerciseID != canonical || got.WorkoutID != wid {
		t.Fatalf("got %+v", got)
	}

	if _, err := s.AddExercise(context.Background(), user, wid, workout.AddExerciseInput{}); kind(err) != domain.KindInvalid {
		t.Fatalf("missing exercise_id should be invalid, got %v", err)
	}
	f.unknownEx = true
	_, err = s.AddExercise(context.Background(), user, wid, workout.AddExerciseInput{ExerciseID: &old})
	if kind(err) != domain.KindInvalid || err.Error() != "unknown exercise" {
		t.Fatalf("got %v", err)
	}
}

func TestSetsGetTheNextPositionAndDefaults(t *testing.T) {
	f := &fakeStore{nextSetPos: 4}
	s := workout.NewTestService(f)
	got, err := s.AddSet(context.Background(), user, uuid.New(), workout.SetInput{Reps: ptr(int32(5))})
	if err != nil {
		t.Fatal(err)
	}
	if got.Position != 4 || got.SetType != "working" || !f.locked {
		t.Fatalf("got %+v locked=%v", got, f.locked)
	}
	if _, err := s.AddSet(context.Background(), user, uuid.New(), workout.SetInput{SetType: ptr("bogus")}); kind(err) != domain.KindInvalid {
		t.Fatalf("bogus set type should be invalid, got %v", err)
	}
}

func TestRirBounds(t *testing.T) {
	f := &fakeStore{nextSetPos: 1}
	s := workout.NewTestService(f)
	for _, c := range []struct {
		rir int16
		ok  bool
	}{{-1, false}, {0, true}, {3, true}, {5, true}, {6, false}, {9, false}} {
		_, errAdd := s.AddSet(context.Background(), user, uuid.New(), workout.SetInput{Rir: ptr(c.rir)})
		_, errUpd := s.UpdateSet(context.Background(), user, uuid.New(),
			workout.SetPatch{Rir: workout.OptionalInt16{Specified: true, Value: ptr(c.rir)}})
		for _, err := range []error{errAdd, errUpd} {
			if c.ok && err != nil {
				t.Errorf("rir %d: unexpected %v", c.rir, err)
			}
			if !c.ok && (kind(err) != domain.KindInvalid || err.Error() != "invalid value (sets_rir_check)") {
				t.Errorf("rir %d: want the rir check error, got %v", c.rir, err)
			}
		}
	}
}

func TestOnlyRirCanBeCleared(t *testing.T) {
	f := &fakeStore{}
	s := workout.NewTestService(f)
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		rir     workout.OptionalInt16
		wantSet bool
	}{
		{"omitted", workout.OptionalInt16{}, false},
		{"null", workout.OptionalInt16{Specified: true}, true},
		{"value", workout.OptionalInt16{Specified: true, Value: ptr(int16(2))}, true},
	} {
		if _, err := s.UpdateSet(ctx, user, uuid.New(), workout.SetPatch{Rir: c.rir}); err != nil {
			t.Fatal(err)
		}
		got := f.updated[len(f.updated)-1]
		if got.RirSet != c.wantSet || (got.Rir == nil) != (c.rir.Value == nil) {
			t.Errorf("%s: got RirSet=%v Rir=%v", c.name, got.RirSet, got.Rir)
		}
	}
}

func TestListLimitIsClampedAndDefaults(t *testing.T) {
	var got []int32
	f := &listStore{onList: func(a db.ListWorkoutSummariesParams) { got = append(got, a.MaxRows) }}
	s := workout.NewTestService(f)
	for _, l := range []*int64{nil, ptr(int64(0)), ptr(int64(-4)), ptr(int64(2)), ptr(int64(1000))} {
		if _, err := s.List(context.Background(), user, l, nil); err != nil {
			t.Fatal(err)
		}
	}
	want := []int32{20, 1, 1, 2, 100}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("limits %v, want %v", got, want)
		}
	}
}

type listStore struct {
	workout.Store
	onList func(db.ListWorkoutSummariesParams)
}

func (l *listStore) ListWorkoutSummaries(_ context.Context, a db.ListWorkoutSummariesParams) ([]db.ListWorkoutSummariesRow, error) {
	l.onList(a)
	return nil, nil
}
