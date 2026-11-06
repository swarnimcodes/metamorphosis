// Package stats is the read-only statistics over a user's logged workouts.
//
// Conventions shared by every figure: a "day" is the calendar date in the workout's own time zone (falling back to
// the account's), a workout only counts if it has at least one logged set, warm-up sets are left out of every
// number, and entries logged against exercises that were merged into another count towards the target. The store
// resolves days and filters; the rules (windows, streaks, records, rounding) are the pure functions in calc.go.
package stats

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

// Store is the database access the service needs. *db.Queries satisfies it.
type Store interface {
	StatsUserTimezone(ctx context.Context, userID uuid.UUID) (string, error)
	StatsToday(ctx context.Context, tz string) (time.Time, error)
	StatsWorkoutDays(ctx context.Context, arg db.StatsWorkoutDaysParams) ([]time.Time, error)
	StatsWorkoutsInWindow(ctx context.Context, arg db.StatsWorkoutsInWindowParams) ([]db.StatsWorkoutsInWindowRow, error)
	StatsLoggedExercises(ctx context.Context, arg db.StatsLoggedExercisesParams) ([]db.StatsLoggedExercisesRow, error)
	StatsVariationTagNames(ctx context.Context) ([]db.StatsVariationTagNamesRow, error)
	StatsResolveExercise(ctx context.Context, arg db.StatsResolveExerciseParams) (db.StatsResolveExerciseRow, error)
	StatsExerciseSets(ctx context.Context, arg db.StatsExerciseSetsParams) ([]db.StatsExerciseSetsRow, error)
}

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

// timezone is the account's zone; an account that is gone is unauthorized.
func (s *Service) timezone(ctx context.Context, user uuid.UUID) (string, error) {
	tz, err := s.store.StatsUserTimezone(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.Unauthorized()
	}
	return tz, err
}

// Consistency is the activity calendar of the last weeks weeks plus the week streaks over all history.
type Consistency struct {
	Timezone     string
	From, To     time.Time
	Days         []Day
	Totals       Totals
	CurrentWeeks int
	LongestWeeks int
}

// Consistency builds the calendar. weeks is clamped to 1..MaxWeeks; the window starts on a Monday and ends today
// in the account's zone.
func (s *Service) Consistency(ctx context.Context, user uuid.UUID, weeks int64) (Consistency, error) {
	tz, err := s.timezone(ctx, user)
	if err != nil {
		return Consistency{}, err
	}
	today, err := s.store.StatsToday(ctx, tz)
	if err != nil {
		return Consistency{}, err
	}
	from := WindowStart(today, weeks)
	rows, err := s.store.StatsWorkoutsInWindow(ctx, db.StatsWorkoutsInWindowParams{UserID: user, Tz: tz, FromDay: from, ToDay: today})
	if err != nil {
		return Consistency{}, err
	}
	workouts := make([]WorkoutDay, len(rows))
	for i, r := range rows {
		workouts[i] = WorkoutDay{Day: r.Day, Sets: r.Sets, Volume: r.Volume}
	}
	days, totals := DayTotals(workouts)

	// The longest streak is over all history, not just the window.
	all, err := s.store.StatsWorkoutDays(ctx, db.StatsWorkoutDaysParams{UserID: user, Tz: tz})
	if err != nil {
		return Consistency{}, err
	}
	cur, longest := Streaks(all, today)
	return Consistency{Timezone: tz, From: from, To: date(today), Days: days, Totals: totals, CurrentWeeks: cur, LongestWeeks: longest}, nil
}

// LoggedExercise is an exercise (under its canonical id) the user has done, split by variant.
type LoggedExercise struct {
	ExerciseID    uuid.UUID
	Name          string
	Kind          string
	VariantKey    string
	VariantLabel  string
	Sessions      int64
	LastPerformed time.Time
}

// LoggedExercises lists what the user has logged, most recently performed first.
func (s *Service) LoggedExercises(ctx context.Context, user uuid.UUID) ([]LoggedExercise, error) {
	tz, err := s.timezone(ctx, user)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.StatsLoggedExercises(ctx, db.StatsLoggedExercisesParams{UserID: user, Tz: tz})
	if err != nil {
		return nil, err
	}
	tags, err := s.store.StatsVariationTagNames(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(tags))
	for _, t := range tags {
		names[t.Slug] = t.Name
	}
	out := make([]LoggedExercise, 0, len(rows))
	for _, r := range rows {
		out = append(out, LoggedExercise{
			ExerciseID: r.ExerciseID, Name: r.Name, Kind: string(r.Kind), VariantKey: r.VariantKey,
			VariantLabel: VariantLabel(r.VariantKey, names), Sessions: r.Sessions, LastPerformed: date(r.LastPerformed),
		})
	}
	return out, nil
}

// Progress is one exercise's history, a point per session, with the records across them.
type Progress struct {
	ExerciseID uuid.UUID
	Name       string
	Kind       string
	VariantKey *string
	Points     []Point
	Records    Records
}

// Progress returns the history of an exercise. A merged exercise resolves to its target. A nil variantKey combines
// every variation; a non-nil one (even empty, meaning untagged) selects exactly that variant.
func (s *Service) Progress(ctx context.Context, user, exercise uuid.UUID, variantKey *string) (Progress, error) {
	tz, err := s.timezone(ctx, user)
	if err != nil {
		return Progress{}, err
	}
	ex, err := s.store.StatsResolveExercise(ctx, db.StatsResolveExerciseParams{ID: exercise, UserID: uuid.NullUUID{UUID: user, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, domain.NotFound()
	}
	if err != nil {
		return Progress{}, err
	}
	rows, err := s.store.StatsExerciseSets(ctx, db.StatsExerciseSetsParams{UserID: user, Tz: tz, ExerciseID: ex.ID, VariantKey: variantKey})
	if err != nil {
		return Progress{}, err
	}

	// Rows arrive grouped by workout in time order; fold each group into one point.
	points := []Point{}
	var sets []Set
	flush := func(i int) {
		if len(sets) > 0 {
			points = append(points, PointOf(rows[i-1].Day, rows[i-1].WorkoutID, sets))
			sets = nil
		}
	}
	for i, r := range rows {
		if i > 0 && r.WorkoutID != rows[i-1].WorkoutID {
			flush(i)
		}
		sets = append(sets, Set{Weight: nullDec(r.WeightKg), Reps: r.Reps, DurationS: r.DurationS, DistanceM: nullDec(r.DistanceM)})
	}
	flush(len(rows))
	return Progress{
		ExerciseID: ex.ID, Name: ex.Name, Kind: string(ex.Kind), VariantKey: variantKey,
		Points: points, Records: RecordsOf(points),
	}, nil
}

func nullDec(n decimal.NullDecimal) *decimal.Decimal {
	if !n.Valid {
		return nil
	}
	return &n.Decimal
}
