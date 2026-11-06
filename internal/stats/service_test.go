package stats_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
	"github.com/smarniw/metamorphosis/internal/stats"
)

type fakeStore struct {
	tz        string
	noUser    bool
	today     time.Time
	window    []db.StatsWorkoutsInWindowRow
	allDays   []time.Time
	windowArg db.StatsWorkoutsInWindowParams
	logged    []db.StatsLoggedExercisesRow
	tags      []db.StatsVariationTagNamesRow
	resolved  *db.StatsResolveExerciseRow
	sets      []db.StatsExerciseSetsRow
	setsArg   db.StatsExerciseSetsParams
}

func (f *fakeStore) StatsUserTimezone(context.Context, uuid.UUID) (string, error) {
	if f.noUser {
		return "", pgx.ErrNoRows
	}
	return f.tz, nil
}
func (f *fakeStore) StatsToday(context.Context, string) (time.Time, error) { return f.today, nil }
func (f *fakeStore) StatsWorkoutDays(context.Context, db.StatsWorkoutDaysParams) ([]time.Time, error) {
	return f.allDays, nil
}
func (f *fakeStore) StatsWorkoutsInWindow(_ context.Context, a db.StatsWorkoutsInWindowParams) ([]db.StatsWorkoutsInWindowRow, error) {
	f.windowArg = a
	return f.window, nil
}
func (f *fakeStore) StatsLoggedExercises(context.Context, db.StatsLoggedExercisesParams) ([]db.StatsLoggedExercisesRow, error) {
	return f.logged, nil
}
func (f *fakeStore) StatsVariationTagNames(context.Context) ([]db.StatsVariationTagNamesRow, error) {
	return f.tags, nil
}
func (f *fakeStore) StatsResolveExercise(context.Context, db.StatsResolveExerciseParams) (db.StatsResolveExerciseRow, error) {
	if f.resolved == nil {
		return db.StatsResolveExerciseRow{}, pgx.ErrNoRows
	}
	return *f.resolved, nil
}
func (f *fakeStore) StatsExerciseSets(_ context.Context, a db.StatsExerciseSetsParams) ([]db.StatsExerciseSetsRow, error) {
	f.setsArg = a
	return f.sets, nil
}

func kind(err error) domain.Kind {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Kind
	}
	return 0
}

func TestConsistencyBuildsTheWindowAndStreaksOverAllHistory(t *testing.T) {
	f := &fakeStore{
		tz: "Asia/Kolkata", today: day("2026-11-06"),
		window: []db.StatsWorkoutsInWindowRow{
			{Day: day("2026-11-03"), Sets: 2, Volume: dec("560")},
			{Day: day("2026-11-03"), Sets: 1, Volume: dec("100.04")},
			{Day: day("2026-11-05"), Sets: 0, Volume: dec("0")},
		},
		allDays: days("2020-01-01", "2020-01-08", "2020-01-15", "2020-01-22", "2026-11-03"),
	}
	c, err := stats.NewService(f).Consistency(context.Background(), uuid.New(), 12)
	if err != nil {
		t.Fatal(err)
	}
	if c.Timezone != "Asia/Kolkata" || ymd(c.To) != "2026-11-06" || ymd(c.From) != "2026-08-17" {
		t.Errorf("%s %s %s", c.Timezone, ymd(c.From), ymd(c.To))
	}
	if ymd(f.windowArg.FromDay) != "2026-08-17" || ymd(f.windowArg.ToDay) != "2026-11-06" || f.windowArg.Tz != "Asia/Kolkata" {
		t.Errorf("window asked for: %+v", f.windowArg)
	}
	if len(c.Days) != 2 || c.Days[0].Workouts != 2 || c.Days[0].Volume.String() != "660" || c.Days[1].Sets != 0 {
		t.Errorf("days %+v", c.Days)
	}
	if c.Totals.Workouts != 3 || c.Totals.ActiveDays != 2 || c.Totals.Sets != 3 || c.Totals.Volume.String() != "660" {
		t.Errorf("totals %+v", c.Totals)
	}
	if c.CurrentWeeks != 1 || c.LongestWeeks != 4 {
		t.Errorf("streaks %d %d", c.CurrentWeeks, c.LongestWeeks)
	}
}

func TestConsistencyClampsWeeks(t *testing.T) {
	f := &fakeStore{tz: "UTC", today: day("2026-11-06")}
	c, err := stats.NewService(f).Consistency(context.Background(), uuid.New(), 100000)
	if err != nil {
		t.Fatal(err)
	}
	if ymd(c.From) != "2024-11-11" {
		t.Errorf("from %s", ymd(c.From))
	}
}

func TestAnAccountThatIsGoneIsUnauthorized(t *testing.T) {
	s := stats.NewService(&fakeStore{noUser: true})
	ctx, u := context.Background(), uuid.New()
	if _, err := s.Consistency(ctx, u, 1); kind(err) != domain.KindUnauthorized {
		t.Errorf("consistency: %v", err)
	}
	if _, err := s.LoggedExercises(ctx, u); kind(err) != domain.KindUnauthorized {
		t.Errorf("exercises: %v", err)
	}
	if _, err := s.Progress(ctx, u, uuid.New(), nil); kind(err) != domain.KindUnauthorized {
		t.Errorf("progress: %v", err)
	}
}

func TestLoggedExercisesLabelVariants(t *testing.T) {
	id := uuid.New()
	f := &fakeStore{
		tz:     "UTC",
		tags:   []db.StatsVariationTagNamesRow{{Slug: "ez-bar", Name: "EZ Bar"}, {Slug: "preacher", Name: "Preacher"}},
		logged: []db.StatsLoggedExercisesRow{{ExerciseID: id, Name: "Curl", Kind: "weight_reps", VariantKey: "ez-bar+preacher", Sessions: 3, LastPerformed: day("2026-11-01")}},
	}
	got, err := stats.NewService(f).LoggedExercises(context.Background(), uuid.New())
	if err != nil || len(got) != 1 || got[0].VariantLabel != "EZ Bar + Preacher" || got[0].Kind != "weight_reps" || got[0].Sessions != 3 {
		t.Errorf("%+v %v", got, err)
	}
	f.logged = nil
	got, err = stats.NewService(f).LoggedExercises(context.Background(), uuid.New())
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("empty history must be an empty non-nil list: %#v %v", got, err)
	}
}

func TestProgressFoldsSetsPerWorkoutAndFindsRecords(t *testing.T) {
	ex := uuid.New()
	w1, w2 := uuid.New(), uuid.New()
	d := func(s string) decimal.NullDecimal { return decimal.NullDecimal{Decimal: dec(s), Valid: true} }
	f := &fakeStore{
		tz:       "UTC",
		resolved: &db.StatsResolveExerciseRow{ID: ex, Name: "Bench Press", Kind: "weight_reps"},
		sets: []db.StatsExerciseSetsRow{
			{WorkoutID: w1, Day: day("2026-11-01"), WeightKg: d("40"), Reps: i32(8)},
			{WorkoutID: w1, Day: day("2026-11-01"), WeightKg: d("40"), Reps: i32(6)},
			{WorkoutID: w1, Day: day("2026-11-01"), Reps: i32(10)}, // NULL weight
			{WorkoutID: w2, Day: day("2026-11-08"), WeightKg: d("40"), Reps: i32(8)},
		},
	}
	variant := ""
	p, err := stats.NewService(f).Progress(context.Background(), uuid.New(), uuid.New(), &variant)
	if err != nil {
		t.Fatal(err)
	}
	if f.setsArg.ExerciseID != ex || f.setsArg.VariantKey == nil || *f.setsArg.VariantKey != "" {
		t.Errorf("sets asked for %+v", f.setsArg)
	}
	if p.ExerciseID != ex || p.Name != "Bench Press" || p.VariantKey == nil || len(p.Points) != 2 {
		t.Fatalf("%+v", p)
	}
	if p.Points[0].Sets != 3 || p.Points[0].WorkoutID != w1 || p.Points[0].VolumeKg.String() != "560" || *p.Points[0].TotalReps != 24 {
		t.Errorf("%+v", p.Points[0])
	}
	if ymd(p.Records.TopWeightKg.Date) != "2026-11-01" || ymd(p.Records.BestVolumeKg.Date) != "2026-11-01" || p.Records.TopReps.Value != 10 {
		t.Errorf("records %+v", p.Records)
	}
}

func TestProgressWithNoHistoryAndUnknownExercise(t *testing.T) {
	f := &fakeStore{tz: "UTC"}
	if _, err := stats.NewService(f).Progress(context.Background(), uuid.New(), uuid.New(), nil); kind(err) != domain.KindNotFound {
		t.Errorf("unknown exercise: %v", err)
	}
	f.resolved = &db.StatsResolveExerciseRow{ID: uuid.New(), Name: "Squat", Kind: "weight_reps"}
	p, err := stats.NewService(f).Progress(context.Background(), uuid.New(), uuid.New(), nil)
	if err != nil || p.Points == nil || len(p.Points) != 0 || p.VariantKey != nil || p.Records.TopWeightKg != nil {
		t.Errorf("%+v %v", p, err)
	}
}
