package stats

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Everything in this file is a pure function of its arguments: no database, no clock, no time zone database. The
// calendar dates it works on were already resolved by the store, each in the zone its workout was done in, so a
// date here is just a year-month-day and DST never enters.

const (
	// DefaultWeeks is the length of the consistency window when the client does not ask for one.
	DefaultWeeks = 53
	// MaxWeeks is the longest window a client can get.
	MaxWeeks = 104
)

// ClampWeeks bounds a requested window length to 1..MaxWeeks.
func ClampWeeks(n int64) int {
	return int(min(max(n, 1), MaxWeeks))
}

// date is the calendar date of t as a UTC midnight, whatever zone or clock time t carries.
func date(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// MondayOf is the Monday of the week containing d (weeks run Monday to Sunday).
func MondayOf(d time.Time) time.Time {
	d = date(d)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// WindowStart is the Monday of the first week of a window that ends in the week of today and covers weeks weeks
// (clamped to 1..MaxWeeks).
func WindowStart(today time.Time, weeks int64) time.Time {
	return MondayOf(today).AddDate(0, 0, -7*(ClampWeeks(weeks)-1))
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// WeekID numbers weeks so that the seven days Monday..Sunday share one id and consecutive weeks differ by one.
func WeekID(d time.Time) int64 {
	days := floorDiv(date(d).Unix(), 86400)
	// 1970-01-01 was a Thursday: shifting by 3 makes Monday the first day of a week.
	return floorDiv(days+3, 7)
}

// Streaks returns the current and longest run of consecutive weeks that contain at least one of the days. The
// current run is the one ending this week, or last week when nothing is logged yet this week; a whole empty week
// before that ends it. days may repeat and need not be sorted.
func Streaks(days []time.Time, today time.Time) (current, longest int) {
	active := make(map[int64]bool, len(days))
	for _, d := range days {
		active[WeekID(d)] = true
	}
	weeks := make([]int64, 0, len(active))
	for w := range active {
		weeks = append(weeks, w)
	}
	sort.Slice(weeks, func(i, j int) bool { return weeks[i] < weeks[j] })
	run := 0
	for i, w := range weeks {
		if i > 0 && weeks[i-1] == w-1 {
			run++
		} else {
			run = 1
		}
		longest = max(longest, run)
	}
	this := WeekID(today)
	start := this
	if !active[start] {
		start = this - 1
	}
	for active[start-int64(current)] {
		current++
	}
	return current, longest
}

// WorkoutDay is one counted workout reduced to what the day totals need: the local day it belongs to, its
// working sets and their volume (weight x reps, a missing factor counting as 0).
type WorkoutDay struct {
	Day    time.Time
	Sets   int64
	Volume decimal.Decimal
}

// Day is the total of every counted workout on one local day.
type Day struct {
	Date     time.Time
	Workouts int64
	Sets     int64
	Volume   decimal.Decimal // rounded to one decimal
}

// Totals sums a window.
type Totals struct {
	Workouts   int64
	ActiveDays int64
	Sets       int64
	Volume     decimal.Decimal // the rounded day volumes, summed and rounded to one decimal
}

// DayTotals groups workouts by local day, ascending, and totals them. Volumes are rounded per day to one decimal
// (half away from zero) and the window volume is the sum of those rounded day volumes.
func DayTotals(workouts []WorkoutDay) ([]Day, Totals) {
	byDay := map[time.Time]*Day{}
	for _, w := range workouts {
		k := date(w.Day)
		d := byDay[k]
		if d == nil {
			d = &Day{Date: k}
			byDay[k] = d
		}
		d.Workouts++
		d.Sets += w.Sets
		d.Volume = d.Volume.Add(w.Volume)
	}
	days := make([]Day, 0, len(byDay))
	for _, d := range byDay {
		days = append(days, *d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date.Before(days[j].Date) })
	var t Totals
	for i := range days {
		days[i].Volume = days[i].Volume.Round(1)
		t.Workouts += days[i].Workouts
		t.Sets += days[i].Sets
		t.Volume = t.Volume.Add(days[i].Volume)
	}
	t.ActiveDays = int64(len(days))
	t.Volume = t.Volume.Round(1)
	return days, t
}

// VariantLabel turns a variant key ("ez-bar+preacher") into its display label ("EZ Bar + Preacher") using the
// catalog's tag names; a slug without a name stays as it is.
func VariantLabel(key string, names map[string]string) string {
	parts := []string{}
	for _, slug := range strings.Split(key, "+") {
		if slug == "" {
			continue
		}
		if n, ok := names[slug]; ok {
			slug = n
		}
		parts = append(parts, slug)
	}
	return strings.Join(parts, " + ")
}

// ---- progress ----

var (
	thirty = decimal.NewFromInt(30)
	// maxE1RMReps: the Epley estimate is only trusted for sets of up to this many reps.
	maxE1RMReps int32 = 12
)

// E1RM is the Epley estimate of a one-rep max, weight x (1 + reps/30), trusted only for 1-12 rep sets; a single
// is its own max. ok is false when the set gives no estimate.
func E1RM(weight decimal.Decimal, reps int32) (est decimal.Decimal, ok bool) {
	switch {
	case !weight.IsPositive() || reps < 1 || reps > maxE1RMReps:
		return decimal.Zero, false
	case reps == 1:
		return weight, true
	}
	return weight.Mul(thirty.Add(decimal.NewFromInt32(reps))).Div(thirty), true
}

// Set is one logged working set; a nil field was not logged.
type Set struct {
	Weight    *decimal.Decimal
	Reps      *int32
	DurationS *int32
	DistanceM *decimal.Decimal
}

// Point is one session (workout) of an exercise. A nil field does not apply to the exercise's kind.
type Point struct {
	Date           time.Time
	WorkoutID      uuid.UUID
	Sets           int64
	VolumeKg       *decimal.Decimal // rounded to 2 decimals
	TopWeightKg    *decimal.Decimal // 3
	BestE1RMKg     *decimal.Decimal // 2
	TopReps        *int32
	TotalReps      *int64
	BestDurationS  *int32
	TotalDurationS *int64
	BestDistanceM  *decimal.Decimal // 2
	TotalDistanceM *decimal.Decimal // 2
}

func roundPtr(v *decimal.Decimal, places int32) *decimal.Decimal {
	if v == nil {
		return nil
	}
	r := v.Round(places)
	return &r
}

func maxDec(cur *decimal.Decimal, v decimal.Decimal) *decimal.Decimal {
	if cur == nil || v.GreaterThan(*cur) {
		return &v
	}
	return cur
}

func sumDec(cur *decimal.Decimal, v decimal.Decimal) *decimal.Decimal {
	if cur == nil {
		return &v
	}
	s := cur.Add(v)
	return &s
}

// PointOf folds the sets of one session into its point. Volume needs both weight and reps on a set; e1RM only
// counts sets eligible for the estimate; every other figure is nil when no set logged the underlying value.
func PointOf(day time.Time, workoutID uuid.UUID, sets []Set) Point {
	p := Point{Date: date(day), WorkoutID: workoutID, Sets: int64(len(sets))}
	var maxW, maxE, maxD, vol, dist *decimal.Decimal
	for _, s := range sets {
		if s.Weight != nil {
			maxW = maxDec(maxW, *s.Weight)
		}
		if s.Weight != nil && s.Reps != nil {
			vol = sumDec(vol, s.Weight.Mul(decimal.NewFromInt32(*s.Reps)))
			if e, ok := E1RM(*s.Weight, *s.Reps); ok {
				maxE = maxDec(maxE, e)
			}
		}
		if s.Reps != nil {
			if p.TopReps == nil || *s.Reps > *p.TopReps {
				v := *s.Reps
				p.TopReps = &v
			}
			if p.TotalReps == nil {
				p.TotalReps = new(int64)
			}
			*p.TotalReps += int64(*s.Reps)
		}
		if s.DurationS != nil {
			if p.BestDurationS == nil || *s.DurationS > *p.BestDurationS {
				v := *s.DurationS
				p.BestDurationS = &v
			}
			if p.TotalDurationS == nil {
				p.TotalDurationS = new(int64)
			}
			*p.TotalDurationS += int64(*s.DurationS)
		}
		if s.DistanceM != nil {
			maxD = maxDec(maxD, *s.DistanceM)
			dist = sumDec(dist, *s.DistanceM)
		}
	}
	p.VolumeKg = roundPtr(vol, 2)
	p.TopWeightKg = roundPtr(maxW, 3)
	p.BestE1RMKg = roundPtr(maxE, 2)
	p.BestDistanceM = roundPtr(maxD, 2)
	p.TotalDistanceM = roundPtr(dist, 2)
	return p
}

// Record is the best value of a figure and the day it was first reached.
type Record[T any] struct {
	Value T
	Date  time.Time
}

// Records are the bests across all points; a nil record means no point had the figure.
type Records struct {
	TopWeightKg   *Record[decimal.Decimal]
	BestE1RMKg    *Record[decimal.Decimal]
	BestVolumeKg  *Record[decimal.Decimal]
	TopReps       *Record[int32]
	BestDurationS *Record[int32]
	BestDistanceM *Record[decimal.Decimal]
}

// recordOf is the highest value across points in order; a tie goes to the earliest session.
func recordOf[T any](points []Point, pick func(*Point) *T, greater func(a, b T) bool) *Record[T] {
	var best *Record[T]
	for i := range points {
		if v := pick(&points[i]); v != nil && (best == nil || greater(*v, best.Value)) {
			best = &Record[T]{Value: *v, Date: points[i].Date}
		}
	}
	return best
}

// RecordsOf computes the records of points, which must be in chronological order.
func RecordsOf(points []Point) Records {
	decGT := func(a, b decimal.Decimal) bool { return a.GreaterThan(b) }
	intGT := func(a, b int32) bool { return a > b }
	return Records{
		TopWeightKg:   recordOf(points, func(p *Point) *decimal.Decimal { return p.TopWeightKg }, decGT),
		BestE1RMKg:    recordOf(points, func(p *Point) *decimal.Decimal { return p.BestE1RMKg }, decGT),
		BestVolumeKg:  recordOf(points, func(p *Point) *decimal.Decimal { return p.VolumeKg }, decGT),
		TopReps:       recordOf(points, func(p *Point) *int32 { return p.TopReps }, intGT),
		BestDurationS: recordOf(points, func(p *Point) *int32 { return p.BestDurationS }, intGT),
		BestDistanceM: recordOf(points, func(p *Point) *decimal.Decimal { return p.BestDistanceM }, decGT),
	}
}
