// Package workout is workout logging: workouts, the exercises inside them (with their variation tags) and sets.
//
// The rules live here: a new workout starts at the gym of the last one, records the time zone of the device (checked
// against the zones the database knows), an added exercise follows a merge pointer to its canonical exercise, and
// exercises and sets get the next free position under a row lock.
package workout

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
)

const (
	defaultListLimit = 20
	maxListLimit     = 100
	// rirMessage is what the API has always answered when reps in reserve is outside 0..5 (the database enforces
	// the same bounds with this constraint).
	rirMessage = "invalid value (sets_rir_check)"
)

// Store is the database access the service needs. *db.Queries satisfies it.
type Store interface {
	WorkoutTimezoneKnown(ctx context.Context, name string) (bool, error)
	WorkoutUserTimezone(ctx context.Context, userID uuid.UUID) (string, error)
	WorkoutLastGym(ctx context.Context, userID uuid.UUID) (uuid.NullUUID, error)
	InsertWorkout(ctx context.Context, arg db.InsertWorkoutParams) (uuid.UUID, error)
	GetWorkout(ctx context.Context, arg db.GetWorkoutParams) (db.GetWorkoutRow, error)
	ListWorkoutExercises(ctx context.Context, workoutID uuid.UUID) ([]db.ListWorkoutExercisesRow, error)
	ListWorkoutTags(ctx context.Context, workoutID uuid.UUID) ([]db.ListWorkoutTagsRow, error)
	ListWorkoutSets(ctx context.Context, workoutID uuid.UUID) ([]db.Set, error)
	ListWorkoutSummaries(ctx context.Context, arg db.ListWorkoutSummariesParams) ([]db.ListWorkoutSummariesRow, error)
	UpdateWorkout(ctx context.Context, arg db.UpdateWorkoutParams) (uuid.UUID, error)
	FinishWorkout(ctx context.Context, arg db.FinishWorkoutParams) (uuid.UUID, error)
	DeleteWorkout(ctx context.Context, arg db.DeleteWorkoutParams) (int64, error)
	LockWorkout(ctx context.Context, arg db.LockWorkoutParams) (uuid.UUID, error)
	ResolveWorkoutExercise(ctx context.Context, arg db.ResolveWorkoutExerciseParams) (uuid.UUID, error)
	NextWorkoutExercisePosition(ctx context.Context, workoutID uuid.UUID) (int32, error)
	InsertWorkoutExercise(ctx context.Context, arg db.InsertWorkoutExerciseParams) (uuid.UUID, error)
	InsertWorkoutExerciseTags(ctx context.Context, arg db.InsertWorkoutExerciseTagsParams) error
	DeleteWorkoutExercise(ctx context.Context, arg db.DeleteWorkoutExerciseParams) (int64, error)
	DeleteExerciseSets(ctx context.Context, workoutExerciseID uuid.UUID) error
	LockWorkoutExercise(ctx context.Context, arg db.LockWorkoutExerciseParams) (uuid.UUID, error)
	NextSetPosition(ctx context.Context, workoutExerciseID uuid.UUID) (int32, error)
	InsertSet(ctx context.Context, arg db.InsertSetParams) (db.Set, error)
	UpdateSet(ctx context.Context, arg db.UpdateSetParams) (db.Set, error)
	DeleteSet(ctx context.Context, arg db.DeleteSetParams) (int64, error)
	LastWorkoutExercise(ctx context.Context, arg db.LastWorkoutExerciseParams) (db.LastWorkoutExerciseRow, error)
	ListExerciseSets(ctx context.Context, workoutExerciseID uuid.UUID) ([]db.Set, error)
}

var _ Store = (*db.Queries)(nil)

// Service holds the workout rules.
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

// Gym is the gym a workout was logged at.
type Gym struct {
	ID      uuid.UUID
	Name    string
	Area    *string
	City    *string
	Lat     *float64
	Lng     *float64
	MapsURL *string
}

// Tag is a variation tag on a logged exercise.
type Tag struct {
	ID       uuid.UUID
	Slug     string
	Name     string
	Category string
}

// Set is one logged set.
type Set struct {
	ID          uuid.UUID
	Position    int32
	SetType     string
	WeightKg    *decimal.Decimal
	Reps        *int32
	DurationS   *int32
	DistanceM   *decimal.Decimal
	Rir         *int16
	Note        *string
	CompletedAt *time.Time
}

// Exercise is an exercise logged in a workout, with its tags and sets.
type Exercise struct {
	ID            uuid.UUID
	ExerciseID    uuid.UUID
	ExerciseName  string
	Kind          string
	Position      int32
	VariantKey    string
	SupersetGroup *int32
	Notes         *string
	Tags          []Tag
	Sets          []Set
}

// Workout is a workout with everything in it.
type Workout struct {
	ID        uuid.UUID
	Title     *string
	GymID     *uuid.UUID
	Gym       *Gym
	StartedAt time.Time
	EndedAt   *time.Time
	Timezone  string
	Notes     *string
	Exercises []Exercise
}

// Summary is a workout in a list.
type Summary struct {
	ID            uuid.UUID
	Title         *string
	GymID         *uuid.UUID
	Gym           *Gym
	StartedAt     time.Time
	EndedAt       *time.Time
	Timezone      string
	ExerciseCount int64
	SetCount      int64
}

// LastSession is the caller's most recent sets of an exercise variant.
type LastSession struct {
	WorkoutID uuid.UUID
	StartedAt time.Time
	Sets      []Set
}

// CreateInput starts a workout. Everything is optional.
type CreateInput struct {
	Title     *string
	GymID     *uuid.UUID
	Notes     *string
	StartedAt *time.Time
	Timezone  *string
}

// UpdateInput changes a workout; nil leaves a field as it is.
type UpdateInput struct {
	Title     *string
	Notes     *string
	GymID     *uuid.UUID
	StartedAt *time.Time
	EndedAt   *time.Time
}

// AddExerciseInput adds an exercise to a workout.
type AddExerciseInput struct {
	ExerciseID    *uuid.UUID
	TagIDs        []uuid.UUID
	SupersetGroup *int32
	Notes         *string
}

// SetInput is a new set.
type SetInput struct {
	SetType     *string
	WeightKg    *decimal.Decimal
	Reps        *int32
	DurationS   *int32
	DistanceM   *decimal.Decimal
	Rir         *int16
	Note        *string
	CompletedAt *time.Time
}

// OptionalInt16 tells an omitted value (unchanged) from an explicit null (cleared) and a number.
type OptionalInt16 struct {
	Specified bool
	Value     *int16 // nil when Specified and null
}

// SetPatch changes a set. Nil leaves a field unchanged, also for an explicit null, except Rir.
type SetPatch struct {
	SetType     *string
	WeightKg    *decimal.Decimal
	Reps        *int32
	DurationS   *int32
	DistanceM   *decimal.Decimal
	Rir         OptionalInt16
	Note        *string
	CompletedAt *time.Time
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotFound()
	}
	return err
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func uuidPtr(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	id := n.UUID
	return &id
}

func decPtr(n decimal.NullDecimal) *decimal.Decimal {
	if !n.Valid {
		return nil
	}
	d := n.Decimal
	return &d
}

func nullDec(d *decimal.Decimal) decimal.NullDecimal {
	if d == nil {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: *d, Valid: true}
}

// gymRef: a soft-deleted gym keeps its name in history, so the name decides, not the gym's state.
func gymRef(id uuid.NullUUID, name, area, city *string, lat, lng *float64, mapsURL *string) *Gym {
	if !id.Valid || name == nil {
		return nil
	}
	return &Gym{ID: id.UUID, Name: *name, Area: area, City: city, Lat: lat, Lng: lng, MapsURL: mapsURL}
}

func setFrom(r db.Set) Set {
	return Set{
		ID: r.ID, Position: r.Position, SetType: string(r.SetType), WeightKg: decPtr(r.WeightKg), Reps: r.Reps,
		DurationS: r.DurationS, DistanceM: decPtr(r.DistanceM), Rir: r.Rir, Note: r.Note,
		CompletedAt: utc(r.CompletedAt),
	}
}

func setsFrom(rows []db.Set) []Set {
	out := make([]Set, 0, len(rows))
	for _, r := range rows {
		out = append(out, setFrom(r))
	}
	return out
}

func validSetType(v *string) error {
	if v == nil {
		return nil
	}
	switch *v {
	case "working", "warmup", "drop", "failure":
		return nil
	}
	return domain.Invalid("unknown variant `%s`, expected one of `working`, `warmup`, `drop`, `failure`", *v)
}

// validRir: reps in reserve is 0 to 5 (5 meaning five or more). The database enforces the same bounds.
func validRir(v *int16) error {
	if v != nil && (*v < 0 || *v > 5) {
		return domain.Invalid("%s", rirMessage)
	}
	return nil
}

// Create starts a workout. Without a gym it starts where the caller last trained; the time zone is the device's, or
// failing that the account's, so every new workout records the zone it happened in.
func (s *Service) Create(ctx context.Context, user uuid.UUID, in CreateInput) (Workout, error) {
	var tz string
	if in.Timezone != nil {
		known, err := s.store.WorkoutTimezoneKnown(ctx, *in.Timezone)
		if err != nil {
			return Workout{}, err
		}
		if !known {
			return Workout{}, domain.Invalid("unknown timezone")
		}
		tz = *in.Timezone
	} else {
		var err error
		if tz, err = s.store.WorkoutUserTimezone(ctx, user); err != nil {
			return Workout{}, notFound(err)
		}
	}
	gym := nullUUID(in.GymID)
	if !gym.Valid {
		// The join in the query means a gym-less workout never matches, so one in between does not reset it.
		last, err := s.store.WorkoutLastGym(ctx, user)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Workout{}, err
		}
		if err == nil {
			gym = last
		}
	}
	id, err := s.store.InsertWorkout(ctx, db.InsertWorkoutParams{
		UserID: user, Title: in.Title, GymID: gym, Notes: in.Notes, StartedAt: in.StartedAt, Timezone: &tz,
	})
	if err != nil {
		return Workout{}, err
	}
	return s.Get(ctx, user, id)
}

// List returns the caller's workouts newest first. The limit defaults to 20 and is clamped to 1..100; before is
// exclusive.
func (s *Service) List(ctx context.Context, user uuid.UUID, limit *int64, before *time.Time) ([]Summary, error) {
	n := int64(defaultListLimit)
	if limit != nil {
		n = *limit
	}
	n = min(max(n, 1), maxListLimit)
	rows, err := s.store.ListWorkoutSummaries(ctx, db.ListWorkoutSummariesParams{
		UserID: user, Before: before, MaxRows: int32(n),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(rows))
	for _, r := range rows {
		out = append(out, Summary{
			ID: r.ID, Title: r.Title, GymID: uuidPtr(r.GymID),
			Gym:       gymRef(r.GymID, r.GymName, r.GymArea, r.GymCity, r.GymLat, r.GymLng, r.GymMapsUrl),
			StartedAt: r.StartedAt.UTC(), EndedAt: utc(r.EndedAt), Timezone: r.Timezone,
			ExerciseCount: r.ExerciseCount, SetCount: r.SetCount,
		})
	}
	return out, nil
}

// Get returns one workout with its exercises, their tags (by slug) and sets (by position).
func (s *Service) Get(ctx context.Context, user, id uuid.UUID) (Workout, error) {
	return load(ctx, s.store, user, id)
}

func load(ctx context.Context, st Store, user, id uuid.UUID) (Workout, error) {
	r, err := st.GetWorkout(ctx, db.GetWorkoutParams{ID: id, UserID: user})
	if err != nil {
		return Workout{}, notFound(err)
	}
	w := Workout{
		ID: r.ID, Title: r.Title, GymID: uuidPtr(r.GymID),
		Gym:       gymRef(r.GymID, r.GymName, r.GymArea, r.GymCity, r.GymLat, r.GymLng, r.GymMapsUrl),
		StartedAt: r.StartedAt.UTC(), EndedAt: utc(r.EndedAt), Timezone: r.Timezone, Notes: r.Notes,
		Exercises: []Exercise{},
	}
	exs, err := st.ListWorkoutExercises(ctx, id)
	if err != nil {
		return Workout{}, err
	}
	for _, e := range exs {
		w.Exercises = append(w.Exercises, Exercise{
			ID: e.ID, ExerciseID: e.ExerciseID, ExerciseName: e.ExerciseName, Kind: string(e.Kind),
			Position: e.Position, VariantKey: e.VariantKey, SupersetGroup: e.SupersetGroup, Notes: e.Notes,
			Tags: []Tag{}, Sets: []Set{},
		})
	}
	byID := make(map[uuid.UUID]*Exercise, len(w.Exercises))
	for i := range w.Exercises {
		byID[w.Exercises[i].ID] = &w.Exercises[i]
	}
	tags, err := st.ListWorkoutTags(ctx, id)
	if err != nil {
		return Workout{}, err
	}
	for _, t := range tags {
		if e := byID[t.WorkoutExerciseID]; e != nil {
			e.Tags = append(e.Tags, Tag{ID: t.ID, Slug: t.Slug, Name: t.Name, Category: string(t.Category)})
		}
	}
	sets, err := st.ListWorkoutSets(ctx, id)
	if err != nil {
		return Workout{}, err
	}
	for _, row := range sets {
		if e := byID[row.WorkoutExerciseID]; e != nil {
			e.Sets = append(e.Sets, setFrom(row))
		}
	}
	return w, nil
}

// Update changes a workout. Omitted fields are left unchanged; a field cannot be cleared back to null.
func (s *Service) Update(ctx context.Context, user, id uuid.UUID, in UpdateInput) (Workout, error) {
	_, err := s.store.UpdateWorkout(ctx, db.UpdateWorkoutParams{
		ID: id, UserID: user, Title: in.Title, Notes: in.Notes, GymID: nullUUID(in.GymID),
		StartedAt: in.StartedAt, EndedAt: in.EndedAt,
	})
	if err != nil {
		return Workout{}, notFound(err)
	}
	return s.Get(ctx, user, id)
}

// Finish ends the workout now. Finishing twice keeps the first end time.
func (s *Service) Finish(ctx context.Context, user, id uuid.UUID) (Workout, error) {
	if _, err := s.store.FinishWorkout(ctx, db.FinishWorkoutParams{ID: id, UserID: user}); err != nil {
		return Workout{}, notFound(err)
	}
	return s.Get(ctx, user, id)
}

// Remove soft-deletes a workout.
func (s *Service) Remove(ctx context.Context, user, id uuid.UUID) error {
	n, err := s.store.DeleteWorkout(ctx, db.DeleteWorkoutParams{ID: id, UserID: user})
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NotFound()
	}
	return nil
}

// AddExercise appends an exercise to the workout. The workout row is locked so concurrent adds get distinct
// positions, and a merged exercise is replaced by the one it was merged into so history lands on the canonical one.
func (s *Service) AddExercise(ctx context.Context, user, workoutID uuid.UUID, in AddExerciseInput) (Exercise, error) {
	if in.ExerciseID == nil {
		return Exercise{}, domain.Invalid("missing field `exercise_id`")
	}
	tagIDs := in.TagIDs
	if tagIDs == nil {
		tagIDs = []uuid.UUID{}
	}
	var weID uuid.UUID
	err := s.tx(ctx, func(st Store) error {
		if _, err := st.LockWorkout(ctx, db.LockWorkoutParams{ID: workoutID, UserID: user}); err != nil {
			return notFound(err)
		}
		exerciseID, err := st.ResolveWorkoutExercise(ctx, db.ResolveWorkoutExerciseParams{ID: *in.ExerciseID, UserID: user})
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Invalid("unknown exercise")
		}
		if err != nil {
			return err
		}
		position, err := st.NextWorkoutExercisePosition(ctx, workoutID)
		if err != nil {
			return err
		}
		weID, err = st.InsertWorkoutExercise(ctx, db.InsertWorkoutExerciseParams{
			WorkoutID: workoutID, ExerciseID: exerciseID, Position: position,
			SupersetGroup: in.SupersetGroup, Notes: in.Notes,
		})
		if err != nil {
			return err
		}
		// The tag trigger recomputes variant_key for each inserted row.
		return st.InsertWorkoutExerciseTags(ctx, db.InsertWorkoutExerciseTagsParams{
			WorkoutExerciseID: weID, TagIds: tagIDs,
		})
	})
	if err != nil {
		return Exercise{}, err
	}
	w, err := load(ctx, s.store, user, workoutID)
	if err != nil {
		return Exercise{}, err
	}
	for _, e := range w.Exercises {
		if e.ID == weID {
			return e, nil
		}
	}
	return Exercise{}, domain.NotFound()
}

// RemoveExercise soft-deletes a logged exercise and its sets.
func (s *Service) RemoveExercise(ctx context.Context, user, id uuid.UUID) error {
	return s.tx(ctx, func(st Store) error {
		n, err := st.DeleteWorkoutExercise(ctx, db.DeleteWorkoutExerciseParams{ID: id, UserID: user})
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.NotFound()
		}
		return st.DeleteExerciseSets(ctx, id)
	})
}

// AddSet appends a set to a logged exercise (position after the highest remaining one) under a lock on the
// exercise. The set type defaults to working and completed_at to now.
func (s *Service) AddSet(ctx context.Context, user, workoutExerciseID uuid.UUID, in SetInput) (Set, error) {
	if err := validSetType(in.SetType); err != nil {
		return Set{}, err
	}
	setType := db.SetTypeWorking
	if in.SetType != nil {
		setType = db.SetType(*in.SetType)
	}
	var out Set
	err := s.tx(ctx, func(st Store) error {
		if _, err := st.LockWorkoutExercise(ctx, db.LockWorkoutExerciseParams{ID: workoutExerciseID, UserID: user}); err != nil {
			return notFound(err)
		}
		if err := validRir(in.Rir); err != nil {
			return err
		}
		position, err := st.NextSetPosition(ctx, workoutExerciseID)
		if err != nil {
			return err
		}
		row, err := st.InsertSet(ctx, db.InsertSetParams{
			WorkoutExerciseID: workoutExerciseID, Position: position, SetType: setType,
			WeightKg: nullDec(in.WeightKg), Reps: in.Reps, DurationS: in.DurationS, DistanceM: nullDec(in.DistanceM),
			Rir: in.Rir, Note: in.Note, CompletedAt: in.CompletedAt,
		})
		if err != nil {
			return err
		}
		out = setFrom(row)
		return nil
	})
	return out, err
}

// UpdateSet changes a set. Omitted fields, and null for any field but rir, leave it unchanged; null clears rir.
func (s *Service) UpdateSet(ctx context.Context, user, id uuid.UUID, in SetPatch) (Set, error) {
	if err := validSetType(in.SetType); err != nil {
		return Set{}, err
	}
	if err := validRir(in.Rir.Value); err != nil {
		return Set{}, err
	}
	var setType *db.SetType
	if in.SetType != nil {
		t := db.SetType(*in.SetType)
		setType = &t
	}
	row, err := s.store.UpdateSet(ctx, db.UpdateSetParams{
		ID: id, UserID: user, SetType: setType, WeightKg: nullDec(in.WeightKg), Reps: in.Reps,
		DurationS: in.DurationS, DistanceM: nullDec(in.DistanceM), RirSet: in.Rir.Specified, Rir: in.Rir.Value,
		Note: in.Note, CompletedAt: in.CompletedAt,
	})
	if err != nil {
		return Set{}, notFound(err)
	}
	return setFrom(row), nil
}

// RemoveSet soft-deletes a set.
func (s *Service) RemoveSet(ctx context.Context, user, id uuid.UUID) error {
	n, err := s.store.DeleteSet(ctx, db.DeleteSetParams{ID: id, UserID: user})
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NotFound()
	}
	return nil
}

// LastSession is the caller's most recent logged sets for an exercise with this exact variant, used to prefill a new
// set. A merged exercise resolves to its target, and entries logged against the merged-away duplicates still count.
func (s *Service) LastSession(ctx context.Context, user, exerciseID uuid.UUID, variantKey string) (LastSession, error) {
	last, err := s.store.LastWorkoutExercise(ctx, db.LastWorkoutExerciseParams{
		ExerciseID: exerciseID, UserID: user, VariantKey: variantKey,
	})
	if err != nil {
		return LastSession{}, notFound(err)
	}
	rows, err := s.store.ListExerciseSets(ctx, last.WorkoutExerciseID)
	if err != nil {
		return LastSession{}, err
	}
	return LastSession{WorkoutID: last.WorkoutID, StartedAt: last.StartedAt.UTC(), Sets: setsFrom(rows)}, nil
}
