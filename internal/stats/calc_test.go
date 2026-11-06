package stats_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/smarniw/metamorphosis/internal/stats"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func days(ss ...string) []time.Time {
	out := []time.Time{}
	for _, s := range ss {
		out = append(out, day(s))
	}
	return out
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func decp(s string) *decimal.Decimal { d := dec(s); return &d }

func i32(v int32) *int32 { return &v }

func ymd(t time.Time) string { return t.Format("2006-01-02") }

func TestClampWeeks(t *testing.T) {
	for in, want := range map[int64]int{-9223372036854775808: 1, -5: 1, 0: 1, 1: 1, 12: 12, 104: 104, 105: 104, 500: 104, 9223372036854775807: 104} {
		if got := stats.ClampWeeks(in); got != want {
			t.Errorf("ClampWeeks(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestWindowStartIsAMondayAndCoversWholeWeeks(t *testing.T) {
	for _, c := range []struct {
		today string
		weeks int64
		want  string
	}{
		{"2026-11-02", 1, "2026-11-02"}, // today is a Monday
		{"2026-11-08", 1, "2026-11-02"}, // today is a Sunday: still that week
		{"2026-11-06", 1, "2026-11-02"},
		{"2026-11-06", 2, "2026-10-26"},
		{"2026-11-06", 0, "2026-11-02"}, // clamped up
		{"2026-11-06", -5, "2026-11-02"},
		{"2026-11-06", 53, "2025-11-03"},  // 52 weeks back across a year end
		{"2026-11-06", 500, "2024-11-11"}, // clamped to 104: 103 weeks back
		{"2024-03-01", 2, "2024-02-19"},   // across a leap day
		{"2027-01-01", 1, "2026-12-28"},   // a week spanning the new year
	} {
		got := stats.WindowStart(day(c.today), c.weeks)
		if ymd(got) != c.want || got.Weekday() != time.Monday {
			t.Errorf("WindowStart(%s, %d) = %s (%s), want %s", c.today, c.weeks, ymd(got), got.Weekday(), c.want)
		}
	}
}

func TestWeekIDGroupsMondayToSunday(t *testing.T) {
	mon := stats.WeekID(day("2026-01-05"))
	for i, d := range []string{"2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08", "2026-01-09", "2026-01-10", "2026-01-11"} {
		if got := stats.WeekID(day(d)); got != mon {
			t.Errorf("day %d (%s): week %d, want %d", i, d, got, mon)
		}
	}
	if stats.WeekID(day("2026-01-12")) != mon+1 || stats.WeekID(day("2026-01-04")) != mon-1 {
		t.Error("Sunday 4th and Monday 12th must be the neighbouring weeks")
	}
	// Before the epoch, across a year end and on a leap day the numbering stays consecutive.
	for _, d := range []string{"1969-12-28", "1969-12-29", "2025-12-29", "2024-02-26", "1900-01-01"} {
		a, b := stats.WeekID(day(d)), stats.WeekID(day(d).AddDate(0, 0, 7))
		if b != a+1 {
			t.Errorf("%s: a week later is %d, want %d", d, b, a+1)
		}
	}
	if stats.WeekID(day("1969-12-28")) == stats.WeekID(day("1969-12-29")) {
		t.Error("a Sunday and the next Monday are different weeks")
	}
	// A clock time or zone on the value never moves the day.
	late := time.Date(2026, 1, 11, 23, 59, 59, 0, time.FixedZone("x", 14*3600))
	if stats.WeekID(late) != mon {
		t.Error("23:59 Sunday belongs to the Sunday's week")
	}
}

func TestStreaks(t *testing.T) {
	today := day("2026-11-06") // Friday; this week starts Mon Nov 2
	for _, c := range []struct {
		name         string
		days         []time.Time
		cur, longest int
	}{
		{"no history", nil, 0, 0},
		{"only this week", days("2026-11-03"), 1, 1},
		{"this and the two before, a gap, then four", days(
			"2026-11-04", "2026-10-28", "2026-10-20", "2026-10-02", "2026-09-24", "2026-09-17", "2026-09-10"), 3, 4},
		{"nothing yet this week keeps last week's run", days("2026-10-28", "2026-10-21"), 2, 2},
		{"a whole skipped week breaks the current run, not the record", days("2026-10-21", "2026-10-14"), 0, 2},
		{"old history only", days("2025-01-01", "2025-01-08", "2025-01-15"), 0, 3},
		{"many days in one week count once", days("2026-11-02", "2026-11-03", "2026-11-08"), 1, 1},
		{"Sunday and the next Monday are consecutive weeks (last week still counts)", days("2026-10-25", "2026-10-26"), 2, 2},
		{"Sunday to Sunday is the same week", days("2026-10-26", "2026-11-01"), 1, 1},
		{"a streak across a year end", days("2025-12-24", "2026-01-02", "2026-01-09"), 0, 3},
		{"a streak across a leap day", days("2024-02-22", "2024-02-29", "2024-03-07"), 0, 3},
		{"order does not matter and duplicates are fine", days("2026-10-28", "2026-11-04", "2026-11-04", "2026-10-21"), 3, 3},
		{"a future day extends only the record", days("2026-11-04", "2026-11-12"), 1, 2},
	} {
		cur, longest := stats.Streaks(c.days, today)
		if cur != c.cur || longest != c.longest {
			t.Errorf("%s: got (%d, %d), want (%d, %d)", c.name, cur, longest, c.cur, c.longest)
		}
	}
	// Today on a Monday or a Sunday: the same week, so the same answer.
	for _, d := range []string{"2026-11-02", "2026-11-08"} {
		if cur, _ := stats.Streaks(days("2026-10-28", "2026-11-04"), day(d)); cur != 2 {
			t.Errorf("today %s: current %d, want 2", d, cur)
		}
	}
}

// The days handed to the calculations are calendar dates already resolved per workout; a DST change only shifts
// which instant a date came from, never how weeks are counted. Consecutive Mondays across the changes in both
// hemispheres and a 23/25 hour day are exactly one week apart.
func TestWeeksAreCalendarWeeksAcrossDST(t *testing.T) {
	for _, c := range []struct{ zone, start string }{
		{"America/New_York", "2026-03-02"}, // spring forward on Sunday Mar 8
		{"America/New_York", "2026-10-26"}, // fall back on Sunday Nov 1
		{"Australia/Sydney", "2026-09-28"}, // spring forward on Sunday Oct 4
		{"Pacific/Apia", "2011-12-26"},     // skipped 2011-12-30 entirely
	} {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Skip("no tzdata:", err)
		}
		d := day(c.start)
		for i := 0; i < 4; i++ {
			// The same wall-clock moment in the zone, a week apart in local time.
			a := time.Date(d.Year(), d.Month(), d.Day()+7*i, 0, 30, 0, 0, loc)
			b := time.Date(d.Year(), d.Month(), d.Day()+7*i+6, 23, 30, 0, 0, loc)
			if stats.WeekID(a) != stats.WeekID(d)+int64(i) || stats.WeekID(b) != stats.WeekID(a) {
				t.Errorf("%s week %d: ids %d/%d", c.zone, i, stats.WeekID(a), stats.WeekID(b))
			}
		}
		if cur, longest := stats.Streaks([]time.Time{d, d.AddDate(0, 0, 7), d.AddDate(0, 0, 14), d.AddDate(0, 0, 21)}, d.AddDate(0, 0, 21)); cur != 4 || longest != 4 {
			t.Errorf("%s: streak (%d, %d), want (4, 4)", c.zone, cur, longest)
		}
	}
}

func TestDayTotals(t *testing.T) {
	w := func(d string, sets int64, vol string) stats.WorkoutDay {
		return stats.WorkoutDay{Day: day(d), Sets: sets, Volume: dec(vol)}
	}
	t.Run("empty", func(t *testing.T) {
		d, tot := stats.DayTotals(nil)
		if len(d) != 0 || d == nil || tot.Workouts != 0 || tot.ActiveDays != 0 || tot.Sets != 0 || !tot.Volume.IsZero() {
			t.Errorf("got %v %+v", d, tot)
		}
		if tot.Volume.InexactFloat64() != 0 || 1/tot.Volume.InexactFloat64() < 0 {
			t.Error("an empty sum must be a positive zero")
		}
	})
	t.Run("grouped by day, ascending, rounded per day then summed", func(t *testing.T) {
		d, tot := stats.DayTotals([]stats.WorkoutDay{
			w("2026-11-03", 2, "99.99"), w("2026-11-01", 1, "0.04"), w("2026-11-03", 1, "500"), w("2026-11-01", 0, "0.04"),
		})
		if len(d) != 2 || ymd(d[0].Date) != "2026-11-01" || ymd(d[1].Date) != "2026-11-03" {
			t.Fatalf("days: %+v", d)
		}
		if d[0].Workouts != 2 || d[0].Sets != 1 || d[0].Volume.String() != "0.1" { // 0.08 -> 0.1
			t.Errorf("first day: %+v", d[0])
		}
		if d[1].Workouts != 2 || d[1].Sets != 3 || d[1].Volume.String() != "600" { // 599.99 -> 600
			t.Errorf("second day: %+v", d[1])
		}
		if tot.Workouts != 4 || tot.ActiveDays != 2 || tot.Sets != 4 || tot.Volume.String() != "600.1" {
			t.Errorf("totals: %+v", tot)
		}
	})
	t.Run("totals add the rounded days", func(t *testing.T) {
		// Two days of 0.04 each round to 0 each, so the window is 0, not the 0.1 that rounding the raw sum gives.
		_, tot := stats.DayTotals([]stats.WorkoutDay{w("2026-11-01", 1, "0.04"), w("2026-11-02", 1, "0.04")})
		if !tot.Volume.IsZero() {
			t.Errorf("got %s", tot.Volume)
		}
	})
	t.Run("half rounds away from zero", func(t *testing.T) {
		d, _ := stats.DayTotals([]stats.WorkoutDay{w("2026-11-01", 1, "10.25"), w("2026-11-02", 1, "10.35")})
		if d[0].Volume.String() != "10.3" || d[1].Volume.String() != "10.4" {
			t.Errorf("got %s %s", d[0].Volume, d[1].Volume)
		}
	})
	t.Run("a workout with only warm-ups counts with no sets and no volume", func(t *testing.T) {
		d, tot := stats.DayTotals([]stats.WorkoutDay{w("2026-11-01", 0, "0")})
		if len(d) != 1 || d[0].Workouts != 1 || d[0].Sets != 0 || tot.ActiveDays != 1 {
			t.Errorf("%+v %+v", d, tot)
		}
	})
}

func TestVariantLabel(t *testing.T) {
	names := map[string]string{"ez-bar": "EZ Bar", "preacher": "Preacher"}
	for in, want := range map[string]string{
		"": "", "ez-bar": "EZ Bar", "ez-bar+preacher": "EZ Bar + Preacher", "unknown+preacher": "unknown + Preacher", "+": "",
	} {
		if got := stats.VariantLabel(in, names); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestE1RM(t *testing.T) {
	for _, c := range []struct {
		weight string
		reps   int32
		want   string // "" means no estimate
	}{
		{"40", 8, "50.67"}, {"42.5", 5, "49.58"}, {"80", 5, "93.33"}, {"100", 1, "100"},
		{"30", 12, "42"}, {"30", 13, ""}, {"30", 15, ""}, {"30", 0, ""}, {"0", 5, ""}, {"-1", 5, ""}, {"0.5", 1, "0.5"},
	} {
		e, ok := stats.E1RM(dec(c.weight), c.reps)
		if c.want == "" {
			if ok {
				t.Errorf("%sx%d: want no estimate, got %s", c.weight, c.reps, e)
			}
			continue
		}
		if !ok || e.Round(2).String() != c.want {
			t.Errorf("%sx%d: got %s (%v), want %s", c.weight, c.reps, e.Round(2), ok, c.want)
		}
	}
}

func set(w string, reps int32) stats.Set {
	s := stats.Set{Reps: i32(reps)}
	if w != "" {
		s.Weight = decp(w)
	}
	return s
}

func TestPointOfWeightAndReps(t *testing.T) {
	id := uuid.New()
	p := stats.PointOf(day("2026-11-01"), id, []stats.Set{set("40", 8), set("40", 6)})
	if p.Sets != 2 || p.WorkoutID != id || ymd(p.Date) != "2026-11-01" {
		t.Fatalf("%+v", p)
	}
	if p.VolumeKg.String() != "560" || p.TopWeightKg.String() != "40" || p.BestE1RMKg.String() != "50.67" {
		t.Errorf("volume %s top %s e1rm %s", p.VolumeKg, p.TopWeightKg, p.BestE1RMKg)
	}
	if *p.TopReps != 8 || *p.TotalReps != 14 {
		t.Errorf("reps %d total %d", *p.TopReps, *p.TotalReps)
	}
	if p.BestDurationS != nil || p.TotalDurationS != nil || p.BestDistanceM != nil || p.TotalDistanceM != nil {
		t.Errorf("figures that do not apply must be nil: %+v", p)
	}

	// A set too long for the estimate still counts for volume and top weight.
	p = stats.PointOf(day("2026-11-01"), id, []stats.Set{set("42.5", 5), set("30", 15)})
	if p.VolumeKg.String() != "662.5" || p.BestE1RMKg.String() != "49.58" || p.TopWeightKg.String() != "42.5" || *p.TopReps != 15 || *p.TotalReps != 20 {
		t.Errorf("%+v", p)
	}
	// Volume is rounded to two decimals half away from zero: 33.333 x 3 = 99.999.
	p = stats.PointOf(day("2026-11-01"), id, []stats.Set{set("33.333", 3)})
	if p.VolumeKg.String() != "100" {
		t.Errorf("got %s", p.VolumeKg)
	}
}

func TestPointOfMissingValues(t *testing.T) {
	id := uuid.New()
	// Reps without weight: counts reps but has no volume, weight or e1RM.
	p := stats.PointOf(day("2026-11-01"), id, []stats.Set{{Reps: i32(10)}})
	if p.VolumeKg != nil || p.TopWeightKg != nil || p.BestE1RMKg != nil || *p.TopReps != 10 || *p.TotalReps != 10 {
		t.Errorf("%+v", p)
	}
	// Weight without reps: top weight only.
	p = stats.PointOf(day("2026-11-01"), id, []stats.Set{{Weight: decp("20")}})
	if p.VolumeKg != nil || p.TopWeightKg.String() != "20" || p.TopReps != nil || p.TotalReps != nil {
		t.Errorf("%+v", p)
	}
	// Zero reps: a real, logged value (total 0), not "missing".
	p = stats.PointOf(day("2026-11-01"), id, []stats.Set{set("50", 0)})
	if p.TotalReps == nil || *p.TotalReps != 0 || p.VolumeKg.String() != "0" || p.BestE1RMKg != nil {
		t.Errorf("%+v", p)
	}
	// Nothing logged at all.
	p = stats.PointOf(day("2026-11-01"), id, []stats.Set{{}, {}})
	if p.Sets != 2 || p.VolumeKg != nil || p.TopReps != nil || p.TotalDurationS != nil {
		t.Errorf("%+v", p)
	}
}

func TestPointOfDurationAndDistance(t *testing.T) {
	id := uuid.New()
	p := stats.PointOf(day("2026-11-01"), id, []stats.Set{{DurationS: i32(90)}, {DurationS: i32(45)}})
	if *p.BestDurationS != 90 || *p.TotalDurationS != 135 || p.Sets != 2 || p.VolumeKg != nil {
		t.Errorf("%+v", p)
	}
	p = stats.PointOf(day("2026-11-01"), id, []stats.Set{
		{DistanceM: decp("5000.005"), DurationS: i32(1500)}, {DistanceM: decp("0.1"), DurationS: i32(5)},
	})
	if p.BestDistanceM.String() != "5000.01" || p.TotalDistanceM.String() != "5000.11" || *p.TotalDurationS != 1505 {
		t.Errorf("best %s total %s (want 5000.11)", p.BestDistanceM, p.TotalDistanceM)
	}
	// Durations beyond int32 in total must not overflow.
	big := int32(2000000000)
	p = stats.PointOf(day("2026-11-01"), id, []stats.Set{{DurationS: &big}, {DurationS: &big}})
	if *p.TotalDurationS != 4000000000 {
		t.Errorf("total %d", *p.TotalDurationS)
	}
}

func TestRecords(t *testing.T) {
	id := uuid.New()
	a := stats.PointOf(day("2026-11-01"), id, []stats.Set{set("80", 5)})
	b := stats.PointOf(day("2026-11-05"), id, []stats.Set{set("80", 5)})
	c := stats.PointOf(day("2026-11-09"), id, []stats.Set{set("100", 1), {DurationS: i32(30)}})
	r := stats.RecordsOf([]stats.Point{a, b, c})
	if r.TopWeightKg.Value.String() != "100" || ymd(r.TopWeightKg.Date) != "2026-11-09" {
		t.Errorf("top weight %+v", r.TopWeightKg)
	}
	if r.BestVolumeKg.Value.String() != "400" || ymd(r.BestVolumeKg.Date) != "2026-11-01" {
		t.Errorf("volume tie must go to the earliest session: %+v", r.BestVolumeKg)
	}
	if r.BestE1RMKg.Value.String() != "100" || ymd(r.BestE1RMKg.Date) != "2026-11-09" {
		t.Errorf("e1rm %+v", r.BestE1RMKg)
	}
	if r.TopReps.Value != 5 || ymd(r.TopReps.Date) != "2026-11-01" {
		t.Errorf("reps tie must go to the earliest session: %+v", r.TopReps)
	}
	if r.BestDurationS.Value != 30 || r.BestDistanceM != nil {
		t.Errorf("duration %+v distance %+v", r.BestDurationS, r.BestDistanceM)
	}
	// A tie on the very first point stays with it even when later points equal it.
	tie := stats.RecordsOf([]stats.Point{b, a})
	if ymd(tie.TopWeightKg.Date) != "2026-11-05" {
		t.Errorf("the first in the given order wins: %+v", tie.TopWeightKg)
	}
}

func TestRecordsOfNothing(t *testing.T) {
	r := stats.RecordsOf(nil)
	if r.TopWeightKg != nil || r.BestE1RMKg != nil || r.BestVolumeKg != nil || r.TopReps != nil || r.BestDurationS != nil || r.BestDistanceM != nil {
		t.Errorf("%+v", r)
	}
	// A zero value is still a record when it is all there is.
	z := stats.RecordsOf([]stats.Point{stats.PointOf(day("2026-11-01"), uuid.New(), []stats.Set{set("50", 0)})})
	if z.BestVolumeKg == nil || !z.BestVolumeKg.Value.IsZero() || z.TopReps == nil {
		t.Errorf("%+v", z)
	}
}
