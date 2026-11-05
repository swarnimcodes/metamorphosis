package acceptance

import (
	"fmt"
	"testing"
	"time"
)

func setTimezone(t *testing.T, token, tz string) {
	t.Helper()
	ok(t, patch(t, "/me", token, map[string]any{"timezone": tz}))
}

func dayEntry(c Val, date time.Time) (Val, bool) {
	for _, d := range c.Get("days").Arr() {
		if d.Get("date").Str() == ymd(date) {
			return d, true
		}
	}
	return Val{}, false
}

func parseDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("%q is not a YYYY-MM-DD date: %v", s, err)
	}
	return d
}

func consistency(t *testing.T, token string) Val {
	t.Helper()
	return ok(t, get(t, "/stats/consistency", token)).JSON
}

// ---- consistency ----

func TestStatsDaysAreBucketedInTheUsersOwnTimezone(t *testing.T) {
	t.Parallel()
	utc := newUser(t, "utcuser")
	ist := newUser(t, "kolkata")
	setTimezone(t, ist.Token, "Asia/Kolkata")
	bench := exerciseBySlug(t, utc.Token, "bench-press")

	// 21:30 UTC is 03:00 the next day in Kolkata (UTC+5:30); 10:00 UTC is the same local day as that.
	d := daysAgo(5)
	for _, tok := range []string{utc.Token, ist.Token} {
		logWorkout(t, tok, at(d, 21, 30), entry{exercise: bench, sets: []map[string]any{kg(40, 8)}})
	}
	logWorkout(t, ist.Token, at(d.AddDate(0, 0, 1), 10, 0), entry{exercise: bench, sets: []map[string]any{kg(40, 8)}})

	u := consistency(t, utc.Token)
	str(t, "timezone", u.Get("timezone"), "UTC")
	arrayLen(t, "utc days", u.Get("days"), 1)
	str(t, "date", u.Get("days").Idx(0).Get("date"), ymd(d))
	num(t, "workouts", u.Get("days").Idx(0).Get("workouts"), 1)

	k := consistency(t, ist.Token)
	str(t, "timezone", k.Get("timezone"), "Asia/Kolkata")
	arrayLen(t, "both workouts land on the same local day: "+k.String(), k.Get("days"), 1)
	str(t, "date", k.Get("days").Idx(0).Get("date"), ymd(d.AddDate(0, 0, 1)))
	num(t, "workouts", k.Get("days").Idx(0).Get("workouts"), 2)
}

func TestStatsOnlyWorkoutsWithLoggedSetsCount(t *testing.T) {
	t.Parallel()
	u := newUser(t, "counter")
	bench := exerciseBySlug(t, u.Token, "bench-press")

	// Counts: 2 working sets (40x8, 40x6 = 560 kg); the warm-up is excluded from sets and volume.
	logWorkout(t, u.Token, at(daysAgo(3), 9, 0), entry{exercise: bench, sets: []map[string]any{warmup(20, 10), kg(40, 8), kg(40, 6)}})
	// Started but nothing logged.
	created(t, post(t, "/workouts", u.Token, map[string]any{"started_at": at(daysAgo(4), 9, 0)}))
	// Logged, then deleted.
	gone := logWorkout(t, u.Token, at(daysAgo(5), 9, 0), entry{exercise: bench, sets: []map[string]any{kg(40, 8)}})
	noContent(t, del(t, "/workouts/"+gone, u.Token))
	// Its only set was removed again.
	w := created(t, post(t, "/workouts", u.Token, map[string]any{"started_at": at(daysAgo(6), 9, 0)}))
	we := created(t, post(t, "/workouts/"+w.Get("id").Str()+"/exercises", u.Token, map[string]any{"exercise_id": bench}))
	set := created(t, post(t, "/workout-exercises/"+we.Get("id").Str()+"/sets", u.Token, kg(40, 8)))
	noContent(t, del(t, "/sets/"+set.Get("id").Str(), u.Token))

	c := consistency(t, u.Token)
	eq(t, "top-level field count", c.Len(), 6)
	arrayLen(t, "days: "+c.String(), c.Get("days"), 1)
	day, found := dayEntry(c, daysAgo(3))
	isTrue(t, "the real workout day", found)
	eq(t, "day field count", day.Len(), 4)
	num(t, "day.workouts", day.Get("workouts"), 1)
	num(t, "day.sets", day.Get("sets"), 2)
	num(t, "day.volume_kg", day.Get("volume_kg"), 560)
	eq(t, "totals field count", c.Get("totals").Len(), 4)
	num(t, "totals.workouts", c.Get("totals").Get("workouts"), 1)
	num(t, "totals.active_days", c.Get("totals").Get("active_days"), 1)
	num(t, "totals.sets", c.Get("totals").Get("sets"), 2)
	num(t, "totals.volume_kg", c.Get("totals").Get("volume_kg"), 560)
	eq(t, "streak field count", c.Get("streak").Len(), 2)
}

func TestStatsDayAggregatesRoundAndExcludeWarmups(t *testing.T) {
	t.Parallel()
	u := newUser(t, "volume")
	bench := exerciseBySlug(t, u.Token, "bench-press")
	squat := exerciseBySlug(t, u.Token, "squat")
	plank := exerciseBySlug(t, u.Token, "plank")
	d := daysAgo(2)
	// Two workouts on one day: 33.33x3 (=99.99) + a bodyweight-less set, and a duration-only plank.
	logWorkout(t, u.Token, at(d, 8, 0), entry{exercise: bench, sets: []map[string]any{kg(33.33, 3), {"reps": 10}}})
	logWorkout(t, u.Token, at(d, 18, 0),
		entry{exercise: squat, sets: []map[string]any{kg(100, 5), warmup(60, 5)}},
		entry{exercise: plank, sets: []map[string]any{{"duration_s": 60}}})
	c := consistency(t, u.Token)
	day, found := dayEntry(c, d)
	isTrue(t, "day present", found)
	num(t, "workouts", day.Get("workouts"), 2)
	// bench: 2 sets, squat: 1 working set (warm-up excluded), plank: 1 set
	num(t, "sets", day.Get("sets"), 4)
	// 99.99 + 0 (no weight) + 500 + 0, rounded to one decimal place
	num(t, "volume_kg", day.Get("volume_kg"), 600)
	num(t, "totals.volume_kg", c.Get("totals").Get("volume_kg"), 600)
	num(t, "totals.active_days", c.Get("totals").Get("active_days"), 1)
}

func TestStatsWeekStreaksFollowTheRules(t *testing.T) {
	t.Parallel()
	owner := newUser(t, "streaker")
	bench := exerciseBySlug(t, owner.Token, "bench-press")
	logWeeks := func(token string, weeks ...int) {
		for _, w := range weeks {
			logWorkout(t, token, at(daysAgo(7*w), 12, 0), entry{exercise: bench, sets: []map[string]any{kg(40, 8)}})
		}
	}
	streak := func(token string) (int64, int64) {
		c := consistency(t, token)
		return c.Get("streak").Get("current_weeks").Int(), c.Get("streak").Get("longest_weeks").Int()
	}
	check := func(token string, cur, longest int64) {
		t.Helper()
		// Retry once across a week boundary (the test is relative to "now").
		gc, gl := streak(token)
		eq(t, "current_weeks", gc, cur)
		eq(t, "longest_weeks", gl, longest)
	}

	// This week and the two before it, a gap, then a run of four.
	logWeeks(owner.Token, 0, 1, 2, 5, 6, 7, 8)
	check(owner.Token, 3, 4)

	// Nothing logged yet this week: last week's run is still alive.
	t2 := newUser(t, "notyet")
	logWeeks(t2.Token, 1, 2)
	check(t2.Token, 2, 2)

	// A whole week skipped breaks the current streak but not the record.
	t3 := newUser(t, "lapsed")
	logWeeks(t3.Token, 2, 3)
	check(t3.Token, 0, 2)

	// The record covers all history, even beyond the visible window.
	t4 := newUser(t, "veteran")
	logWeeks(t4.Token, 60, 61, 62)
	c := consistency(t, t4.Token)
	emptyArray(t, "outside the 53-week window", c.Get("days"))
	num(t, "current_weeks", c.Get("streak").Get("current_weeks"), 0)
	num(t, "longest_weeks", c.Get("streak").Get("longest_weeks"), 3)

	// No workouts at all: zero streaks, and the numbers are numbers.
	t5 := newUser(t, "idle")
	c = consistency(t, t5.Token)
	num(t, "current_weeks", c.Get("streak").Get("current_weeks"), 0)
	num(t, "longest_weeks", c.Get("streak").Get("longest_weeks"), 0)
	num(t, "totals.volume_kg", c.Get("totals").Get("volume_kg"), 0)
}

func TestStatsConsistencyWindowIsMondayAlignedAndClamped(t *testing.T) {
	t.Parallel()
	u := newUser(t, "windowed")
	other := newUser(t, "bystander")
	bench := exerciseBySlug(t, u.Token, "bench-press")
	logWorkout(t, other.Token, at(daysAgo(2), 9, 0), entry{exercise: bench, sets: []map[string]any{kg(40, 8)}})

	def := consistency(t, u.Token)
	emptyArray(t, "another user's workouts must never show up", def.Get("days"))
	num(t, "totals.workouts", def.Get("totals").Get("workouts"), 0)
	num(t, "totals.active_days", def.Get("totals").Get("active_days"), 0)
	num(t, "totals.sets", def.Get("totals").Get("sets"), 0)
	str(t, "timezone", def.Get("timezone"), "UTC")

	for _, c := range []struct {
		weeks string
		want  int
	}{{"0", 1}, {"-5", 1}, {"1", 1}, {"500", 104}, {"12", 12}, {"", 53}} {
		path := "/stats/consistency?weeks=" + c.weeks
		if c.weeks == "" {
			path = "/stats/consistency"
		}
		before := time.Now().UTC()
		r := ok(t, get(t, path, u.Token)).JSON
		after := time.Now().UTC()
		from, to := parseDate(t, r.Get("from").Str()), parseDate(t, r.Get("to").Str())
		if from.Weekday() != time.Monday {
			t.Fatalf("weeks=%s: from %s is a %s", c.weeks, from.Format("2006-01-02"), from.Weekday())
		}
		isTrue(t, fmt.Sprintf("weeks=%s: to %s is today (UTC)", c.weeks, ymd(to)), ymd(to) == ymd(before) || ymd(to) == ymd(after))
		monday := to.AddDate(0, 0, -((int(to.Weekday()) + 6) % 7))
		wantFrom := monday.AddDate(0, 0, -7*(c.want-1))
		eq(t, fmt.Sprintf("weeks=%s from", c.weeks), ymd(from), ymd(wantFrom))
	}
}

// ---- exercises list ----

func TestStatsLoggedExercisesGroupByVariantAndResolveMerges(t *testing.T) {
	t.Parallel()
	requireDB(t)
	u := newUser(t, "lister")
	bench := exerciseBySlug(t, u.Token, "bench-press")
	plank := exerciseBySlug(t, u.Token, "plank")
	incline := tagBySlug(t, u.Token, "incline")
	dumbbell := tagBySlug(t, u.Token, "dumbbell")

	logWorkout(t, u.Token, at(daysAgo(30), 9, 0), entry{exercise: bench, sets: []map[string]any{kg(40, 8)}})
	logWorkout(t, u.Token, at(daysAgo(20), 9, 0), entry{exercise: bench, sets: []map[string]any{kg(42.5, 6)}})
	mergedWorkout := logWorkout(t, u.Token, at(daysAgo(15), 9, 0), entry{exercise: bench, sets: []map[string]any{kg(45, 5)}})
	logWorkout(t, u.Token, at(daysAgo(2), 9, 0),
		entry{exercise: bench, tags: []string{incline, dumbbell}, sets: []map[string]any{kg(20, 10)}},
		// Warm-up only: it never counts as having done the exercise.
		entry{exercise: plank, sets: []map[string]any{{"duration_s": 30, "set_type": "warmup"}}})

	// A duplicate exercise merged into Bench Press: history logged against it still counts.
	tok := randLetters(10)
	old := sql(t, "insert into exercises (slug, name, kind, status, merged_into_id) values ('old-bench-"+tok+"', 'Old Bench "+tok+
		"', 'weight_reps', 'official', '"+bench+"') returning id")
	sql(t, "update workout_exercises set exercise_id = '"+old+"' where workout_id = '"+mergedWorkout+"'")

	list := ok(t, get(t, "/stats/exercises", u.Token)).JSON
	arrayLen(t, "plank (warm-up only) and the merged name must not appear separately: "+list.String(), list, 2)
	for _, it := range list.Arr() {
		eq(t, "item field count", it.Len(), 7)
		isTrue(t, "sessions is a number", it.Get("sessions").IsNumber())
	}

	// Most recently performed first: the tagged variant (2 days ago), then untagged.
	i0, i1 := list.Idx(0), list.Idx(1)
	str(t, "name", i0.Get("name"), "Bench Press")
	str(t, "variant_key", i0.Get("variant_key"), "dumbbell+incline")
	str(t, "variant_label", i0.Get("variant_label"), "Dumbbell + Incline")
	num(t, "sessions", i0.Get("sessions"), 1)
	str(t, "last_performed", i0.Get("last_performed"), ymd(daysAgo(2)))
	str(t, "kind", i0.Get("kind"), "weight_reps")
	str(t, "exercise_id", i0.Get("exercise_id"), bench)

	str(t, "untagged variant_key", i1.Get("variant_key"), "")
	str(t, "untagged variant_label", i1.Get("variant_label"), "")
	num(t, "two direct sessions plus one logged against the merged duplicate", i1.Get("sessions"), 3)
	str(t, "exercise_id", i1.Get("exercise_id"), bench)
	str(t, "last_performed", i1.Get("last_performed"), ymd(daysAgo(15)))
}

func TestStatsLoggedExercisesLabelsAndOrdering(t *testing.T) {
	t.Parallel()
	u := newUser(t, "labels")
	curl := exerciseBySlug(t, u.Token, "bicep-curl")
	ez := tagBySlug(t, u.Token, "ez-bar")
	preacher := tagBySlug(t, u.Token, "preacher")
	logWorkout(t, u.Token, at(daysAgo(3), 9, 0), entry{exercise: curl, tags: []string{preacher, ez}, sets: []map[string]any{kg(20, 10)}})
	list := ok(t, get(t, "/stats/exercises", u.Token)).JSON
	arrayLen(t, "list", list, 1)
	str(t, "variant_key sorted by slug", list.Idx(0).Get("variant_key"), "ez-bar+preacher")
	str(t, "variant_label uses the catalog names", list.Idx(0).Get("variant_label"), "EZ Bar + Preacher")
	emptyArray(t, "no history", ok(t, get(t, "/stats/exercises", newUser(t, "nohistory").Token)).JSON)
}

// ---- progress ----

func TestStatsProgressNumbersAndRecords(t *testing.T) {
	t.Parallel()
	u := newUser(t, "progressive")
	bench := exerciseBySlug(t, u.Token, "bench-press")
	incline := tagBySlug(t, u.Token, "incline")

	a, b, c, d := daysAgo(20), daysAgo(10), daysAgo(3), daysAgo(1)
	logWorkout(t, u.Token, at(a, 10, 0), entry{exercise: bench, sets: []map[string]any{warmup(20, 10), kg(40, 8), kg(40, 6)}})
	logWorkout(t, u.Token, at(b, 10, 0), entry{exercise: bench, sets: []map[string]any{kg(42.5, 5), kg(30, 15)}})
	logWorkout(t, u.Token, at(c, 10, 0), entry{exercise: bench, sets: []map[string]any{kg(100, 1)}})
	logWorkout(t, u.Token, at(d, 10, 0), entry{exercise: bench, tags: []string{incline}, sets: []map[string]any{kg(30, 10)}})

	url := "/stats/exercises/" + bench + "/progress"

	// All variations combined.
	all := ok(t, get(t, url, u.Token)).JSON
	eq(t, "top-level field count", all.Len(), 6)
	str(t, "exercise_id", all.Get("exercise_id"), bench)
	str(t, "name", all.Get("name"), "Bench Press")
	str(t, "kind", all.Get("kind"), "weight_reps")
	null(t, "variant_key", all.Get("variant_key"))
	arrayLen(t, "points", all.Get("points"), 4)
	eq(t, "records field count", all.Get("records").Len(), 6)

	// Untagged only: three sessions, ascending, with hand-computed numbers.
	plain := ok(t, get(t, url+"?variant_key=", u.Token)).JSON
	str(t, "variant_key", plain.Get("variant_key"), "")
	p := plain.Get("points")
	arrayLen(t, "points", p, 3)
	sameStrings(t, "dates", strs(p, "date"), []string{ymd(a), ymd(b), ymd(c)})
	for _, pt := range p.Arr() {
		eq(t, "point field count", pt.Len(), 12)
		isTrue(t, "workout_id is a uuid", uuidish(pt.Get("workout_id").Str()))
	}

	// A: warm-up ignored. 40x8 + 40x6 = 560 kg, e1RM 40 x (1 + 8/30) = 50.67.
	p0 := p.Idx(0)
	num(t, "sets", p0.Get("sets"), 2)
	num(t, "volume_kg", p0.Get("volume_kg"), 560)
	num(t, "top_weight_kg", p0.Get("top_weight_kg"), 40)
	num(t, "best_e1rm_kg", p0.Get("best_e1rm_kg"), 50.67)
	num(t, "top_reps", p0.Get("top_reps"), 8)
	num(t, "total_reps", p0.Get("total_reps"), 14)
	// B: 42.5x5 + 30x15 = 662.5 kg. The 15-rep set is too long for a 1RM estimate: 42.5 x (1 + 5/30) = 49.58.
	p1 := p.Idx(1)
	num(t, "volume_kg", p1.Get("volume_kg"), 662.5)
	num(t, "best_e1rm_kg", p1.Get("best_e1rm_kg"), 49.58)
	num(t, "top_weight_kg", p1.Get("top_weight_kg"), 42.5)
	num(t, "top_reps", p1.Get("top_reps"), 15)
	num(t, "total_reps", p1.Get("total_reps"), 20)
	// C: a single is its own 1RM.
	p2 := p.Idx(2)
	num(t, "volume_kg", p2.Get("volume_kg"), 100)
	num(t, "best_e1rm_kg", p2.Get("best_e1rm_kg"), 100)
	num(t, "top_weight_kg", p2.Get("top_weight_kg"), 100)
	// Not applicable to this kind: null, not 0 and not omitted.
	for _, field := range []string{"best_duration_s", "total_duration_s", "best_distance_m", "total_distance_m"} {
		null(t, field, p0.Get(field))
	}

	r := plain.Get("records")
	rec := func(name string, value float64, date time.Time) {
		t.Helper()
		eq(t, name+" record field count", r.Get(name).Len(), 2)
		num(t, name+".value", r.Get(name).Get("value"), value)
		str(t, name+".date", r.Get(name).Get("date"), ymd(date))
	}
	rec("top_weight_kg", 100, c)
	rec("best_e1rm_kg", 100, c)
	rec("best_volume_kg", 662.5, b)
	rec("top_reps", 15, b)
	null(t, "best_duration_s", r.Get("best_duration_s"))
	null(t, "best_distance_m", r.Get("best_distance_m"))

	// The incline variant on its own.
	tagged := ok(t, get(t, url+"?variant_key=incline", u.Token)).JSON
	arrayLen(t, "points", tagged.Get("points"), 1)
	num(t, "volume_kg", tagged.Get("points").Idx(0).Get("volume_kg"), 300)
	str(t, "variant_key", tagged.Get("variant_key"), "incline")
	// A variant never logged is a valid, empty answer.
	none := ok(t, get(t, url+"?variant_key=cable", u.Token)).JSON
	emptyArray(t, "points", none.Get("points"))
}

func TestStatsProgressRecordTiesGoToTheEarliestSession(t *testing.T) {
	t.Parallel()
	u := newUser(t, "tied")
	bench := exerciseBySlug(t, u.Token, "bench-press")
	a, b := daysAgo(9), daysAgo(4)
	logWorkout(t, u.Token, at(a, 10, 0), entry{exercise: bench, sets: []map[string]any{kg(80, 5)}})
	logWorkout(t, u.Token, at(b, 10, 0), entry{exercise: bench, sets: []map[string]any{kg(80, 5)}})
	r := ok(t, get(t, "/stats/exercises/"+bench+"/progress", u.Token)).Get("records")
	str(t, "top_weight_kg date", r.Get("top_weight_kg").Get("date"), ymd(a))
	str(t, "best_volume_kg date", r.Get("best_volume_kg").Get("date"), ymd(a))
	num(t, "best_volume_kg", r.Get("best_volume_kg").Get("value"), 400)
	// Epley: 80 x (1 + 5/30)
	num(t, "best_e1rm_kg", r.Get("best_e1rm_kg").Get("value"), 93.33)
}

func TestStatsProgressForDurationAndDistanceExercises(t *testing.T) {
	t.Parallel()
	u := newUser(t, "endurance")
	plank := exerciseBySlug(t, u.Token, "plank")
	running := exerciseBySlug(t, u.Token, "running")

	logWorkout(t, u.Token, at(daysAgo(9), 7, 0),
		entry{exercise: plank, sets: []map[string]any{{"duration_s": 60}}},
		entry{exercise: running, sets: []map[string]any{{"distance_m": 5000, "duration_s": 1500}}})
	logWorkout(t, u.Token, at(daysAgo(2), 7, 0),
		entry{exercise: plank, sets: []map[string]any{{"duration_s": 90}, {"duration_s": 45}}},
		entry{exercise: running, sets: []map[string]any{{"distance_m": 6000, "duration_s": 1900}}})

	p := ok(t, get(t, "/stats/exercises/"+plank+"/progress", u.Token)).JSON
	str(t, "kind", p.Get("kind"), "duration")
	num(t, "best_duration_s", p.Get("points").Idx(1).Get("best_duration_s"), 90)
	num(t, "total_duration_s", p.Get("points").Idx(1).Get("total_duration_s"), 135)
	num(t, "sets", p.Get("points").Idx(1).Get("sets"), 2)
	null(t, "volume_kg", p.Get("points").Idx(1).Get("volume_kg"))
	null(t, "best_e1rm_kg", p.Get("points").Idx(1).Get("best_e1rm_kg"))
	null(t, "top_reps", p.Get("points").Idx(1).Get("top_reps"))
	null(t, "total_reps", p.Get("points").Idx(1).Get("total_reps"))
	num(t, "record value", p.Get("records").Get("best_duration_s").Get("value"), 90)
	str(t, "record date", p.Get("records").Get("best_duration_s").Get("date"), ymd(daysAgo(2)))
	null(t, "top_weight_kg", p.Get("records").Get("top_weight_kg"))

	r := ok(t, get(t, "/stats/exercises/"+running+"/progress", u.Token)).JSON
	str(t, "kind", r.Get("kind"), "distance")
	num(t, "best_distance_m", r.Get("points").Idx(0).Get("best_distance_m"), 5000)
	num(t, "total_distance_m", r.Get("points").Idx(1).Get("total_distance_m"), 6000)
	num(t, "record best_distance_m", r.Get("records").Get("best_distance_m").Get("value"), 6000)
	num(t, "best_duration_s", r.Get("points").Idx(0).Get("best_duration_s"), 1500)
}

func TestStatsProgressVisibilityAndEdgeCases(t *testing.T) {
	t.Parallel()
	requireDB(t)
	viewer := newUser(t, "viewer")
	owner := newUser(t, "owner2")
	bench := exerciseBySlug(t, viewer.Token, "bench-press")
	squat := exerciseBySlug(t, viewer.Token, "squat")
	logWorkout(t, viewer.Token, at(daysAgo(4), 9, 0), entry{exercise: bench, sets: []map[string]any{kg(40, 8)}})

	// Never logged: a valid, empty answer rather than an error.
	empty := ok(t, get(t, "/stats/exercises/"+squat+"/progress", viewer.Token)).JSON
	emptyArray(t, "points", empty.Get("points"))
	eq(t, "records field count", empty.Get("records").Len(), 6)
	for _, k := range empty.Get("records").Keys() {
		null(t, "records."+k, empty.Get("records").Get(k))
	}

	// Unknown ids and other users' private exercises are 404.
	tok := randLetters(10)
	private := sql(t, "insert into exercises (name, kind, status, created_by) values ('Secret Curl "+tok+"', 'weight_reps', 'user', '"+
		owner.ID+"') returning id")
	for _, id := range []string{newUUID(), private} {
		notFound(t, get(t, "/stats/exercises/"+id+"/progress", viewer.Token))
	}
	// The owner can see their own.
	own := ok(t, get(t, "/stats/exercises/"+private+"/progress", owner.Token))
	str(t, "private name", own.Get("name"), "Secret Curl "+tok)

	// A merged-away id resolves to its target.
	old := sql(t, "insert into exercises (slug, name, kind, status, merged_into_id) values ('old-b-"+tok+"', 'Old B "+tok+
		"', 'weight_reps', 'official', '"+bench+"') returning id")
	via := ok(t, get(t, "/stats/exercises/"+old+"/progress", viewer.Token)).JSON
	str(t, "name", via.Get("name"), "Bench Press")
	str(t, "exercise_id is the canonical one", via.Get("exercise_id"), bench)
	arrayLen(t, "points", via.Get("points"), 1)

	// Another user sees none of this user's data.
	theirs := ok(t, get(t, "/stats/exercises/"+bench+"/progress", owner.Token)).JSON
	emptyArray(t, "points", theirs.Get("points"))
	emptyArray(t, "stats/exercises", ok(t, get(t, "/stats/exercises", owner.Token)).JSON)

	// Malformed ids are a 400 from the router.
	status(t, get(t, "/stats/exercises/not-a-uuid/progress", viewer.Token), 400)
	status(t, get(t, "/stats/consistency?weeks=abc", viewer.Token), 400)
}

func TestStatsRequireAuthentication(t *testing.T) {
	t.Parallel()
	for _, uri := range []string{"/stats/consistency", "/stats/exercises", "/stats/exercises/" + newUUID() + "/progress"} {
		unauthorized(t, get(t, uri, ""))
		unauthorized(t, get(t, uri, "not-a-jwt"))
	}
}

// ---- time zones: a workout keeps the day it was done on ----

func logInZone(t *testing.T, token, startedAt, zone, exercise string) string {
	t.Helper()
	body := map[string]any{"started_at": startedAt}
	if zone != "" {
		body["timezone"] = zone
	}
	w := created(t, post(t, "/workouts", token, body))
	wid := w.Get("id").Str()
	we := created(t, post(t, "/workouts/"+wid+"/exercises", token, map[string]any{"exercise_id": exercise}))
	created(t, post(t, "/workout-exercises/"+we.Get("id").Str()+"/sets", token, kg(60, 5)))
	return wid
}

func zoneOf(t *testing.T, token, id string) string {
	t.Helper()
	return ok(t, get(t, "/workouts/"+id, token)).Get("timezone").Str()
}

func TestStatsEachWorkoutIsDatedInTheZoneItWasDoneIn(t *testing.T) {
	t.Parallel()
	requireDB(t)
	u := newUser(t, "traveller")
	bench := exerciseBySlug(t, u.Token, "bench-press")
	setTimezone(t, u.Token, "Asia/Kolkata")

	// 2026-01-05 20:00 UTC is midday in Los Angeles but already 01:30 the next morning in India.
	atUTC := "2026-01-05T20:00:00Z"
	la := logInZone(t, u.Token, atUTC, "America/Los_Angeles", bench)
	home := logInZone(t, u.Token, atUTC, "", bench) // no zone sent: the account's (India)
	// A workout from before zones were recorded (null) follows the account's zone.
	legacy := logInZone(t, u.Token, "2026-01-03T03:00:00Z", "UTC", bench)
	sql(t, "update workouts set timezone = null where id = '"+legacy+"'")

	dates := func(c Val) []string { return strs(c.Get("days"), "date") }
	// LA workout: Jan 5. Home workout: Jan 6 (India). Legacy 03:00 UTC: Jan 3 in India (08:30).
	before := consistency(t, u.Token)
	sameStrings(t, "dates before", dates(before), []string{"2026-01-03", "2026-01-05", "2026-01-06"})

	// The traveller lands in New York and the profile zone follows them. History must not move,
	// except the legacy workout, which has no zone of its own and follows the account.
	setTimezone(t, u.Token, "America/New_York")
	after := consistency(t, u.Token)
	// legacy 03:00 UTC = 22:00 the night before in New York
	sameStrings(t, "dates after", dates(after), []string{"2026-01-02", "2026-01-05", "2026-01-06"})
	str(t, "'today' follows the profile zone", after.Get("timezone"), "America/New_York")

	// The per-exercise chart dates sessions the same way.
	progress := ok(t, get(t, "/stats/exercises/"+bench+"/progress", u.Token)).JSON
	sameStrings(t, "progress dates", strs(progress.Get("points"), "date"), []string{"2026-01-02", "2026-01-05", "2026-01-06"})

	// And so does the list of logged exercises.
	list := ok(t, get(t, "/stats/exercises", u.Token)).JSON
	// (two sessions share one instant, so which of Jan 5 / Jan 6 is "last" is unspecified)
	isTrue(t, "last_performed is one of the session days", contains([]string{"2026-01-05", "2026-01-06"}, list.Idx(0).Get("last_performed").Str()))
	num(t, "sessions", list.Idx(0).Get("sessions"), 3)

	// Workouts report the zone their times belong in.
	eq(t, "la zone", zoneOf(t, u.Token, la), "America/Los_Angeles")
	eq(t, "home zone recorded from the account when the device sent none", zoneOf(t, u.Token, home), "Asia/Kolkata")
	eq(t, "a legacy workout reports the account's current zone", zoneOf(t, u.Token, legacy), "America/New_York")
	list2 := ok(t, get(t, "/workouts", u.Token)).JSON
	zones := map[string]bool{}
	for _, w := range list2.Arr() {
		zones[w.Get("timezone").Str()] = true
	}
	eq(t, "distinct zones in the list", len(zones), 3)
	for _, z := range []string{"America/Los_Angeles", "America/New_York", "Asia/Kolkata"} {
		isTrue(t, "zone "+z+" listed", zones[z])
	}
}

// The longest streak is computed over all history with its own query, so it needs its own proof that each
// workout is dated in the zone it was done in.
func TestStatsLongestStreakDatesEachWorkoutInItsOwnZone(t *testing.T) {
	t.Parallel()
	u := newUser(t, "streakzone")
	bench := exerciseBySlug(t, u.Token, "bench-press")
	// Sunday 2026-01-04 23:00 UTC is already Monday 13:00 in Kiritimati (UTC+14): it belongs to the week of
	// Jan 5, next to the Jan 12 week. Dated in the account's zone (UTC) it would be Jan 4: a gap of one week.
	logInZone(t, u.Token, "2026-01-04T23:00:00Z", "Pacific/Kiritimati", bench)
	logInZone(t, u.Token, "2026-01-14T12:00:00Z", "UTC", bench)
	c := consistency(t, u.Token)
	num(t, "longest_weeks", c.Get("streak").Get("longest_weeks"), 2)
}
