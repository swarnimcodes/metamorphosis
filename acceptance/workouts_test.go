package acceptance

import (
	"testing"
	"time"
)

// assertWorkoutShape pins the exact field set of a full workout.
func assertWorkoutShape(t *testing.T, w Val) {
	t.Helper()
	eq(t, "workout field count", w.Len(), 9)
	for _, k := range []string{"id", "title", "gym_id", "gym", "started_at", "ended_at", "timezone", "notes", "exercises"} {
		isTrue(t, "workout has "+k, w.Has(k))
	}
	isTrue(t, "id is a uuid", uuidish(w.Get("id").Str()))
	isTrue(t, "started_at is an RFC 3339 string", w.Get("started_at").IsString())
	if _, err := time.Parse(time.RFC3339Nano, w.Get("started_at").Str()); err != nil {
		t.Fatalf("started_at %s: %v", w.Get("started_at"), err)
	}
	isTrue(t, "timezone is a string", w.Get("timezone").IsString())
	isTrue(t, "exercises is an array", w.Get("exercises").IsArray())
}

func assertSetShape(t *testing.T, s Val) {
	t.Helper()
	eq(t, "set field count", s.Len(), 10)
	for _, k := range []string{"id", "position", "set_type", "weight_kg", "reps", "duration_s", "distance_m", "rir", "note", "completed_at"} {
		isTrue(t, "set has "+k, s.Has(k))
	}
	isTrue(t, "completed_at is always set (defaults to now)", s.Get("completed_at").IsString())
}

func TestWorkoutLoggingFlow(t *testing.T) {
	t.Parallel()
	u := newUser(t, "lifter")
	bench := exerciseBySlug(t, u.Token, "bench-press")
	incline := tagBySlug(t, u.Token, "incline")
	dumbbell := tagBySlug(t, u.Token, "dumbbell")

	w := created(t, post(t, "/workouts", u.Token, map[string]any{"title": "Push"}))
	assertWorkoutShape(t, w.JSON)
	wid := w.Get("id").Str()
	null(t, "ended_at", w.Get("ended_at"))
	str(t, "title", w.Get("title"), "Push")
	null(t, "gym_id", w.Get("gym_id"))
	null(t, "gym", w.Get("gym"))
	null(t, "notes", w.Get("notes"))
	str(t, "timezone defaults to the account's", w.Get("timezone"), "UTC")
	emptyArray(t, "exercises", w.Get("exercises"))

	// Tag order in the request must not change the variant key.
	we := created(t, post(t, "/workouts/"+wid+"/exercises", u.Token, map[string]any{"exercise_id": bench, "tag_ids": []string{incline, dumbbell}}))
	eq(t, "exercise field count", we.JSON.Len(), 10)
	str(t, "variant_key", we.Get("variant_key"), "dumbbell+incline")
	num(t, "position", we.Get("position"), 1)
	str(t, "exercise_id", we.Get("exercise_id"), bench)
	str(t, "exercise_name", we.Get("exercise_name"), "Bench Press")
	str(t, "kind", we.Get("kind"), "weight_reps")
	null(t, "superset_group", we.Get("superset_group"))
	null(t, "notes", we.Get("notes"))
	emptyArray(t, "sets", we.Get("sets"))
	arrayLen(t, "tags", we.Get("tags"), 2)
	// Tags come back ordered by slug.
	sameStrings(t, "tag slugs", strs(we.Get("tags"), "slug"), []string{"dumbbell", "incline"})
	tag0 := we.Get("tags").Idx(0)
	eq(t, "tag field count", tag0.Len(), 4)
	str(t, "tag id", tag0.Get("id"), dumbbell)
	str(t, "tag name", tag0.Get("name"), "Dumbbell")
	str(t, "tag category", tag0.Get("category"), "equipment")
	str(t, "incline category", we.Get("tags").Idx(1).Get("category"), "angle")
	weID := we.Get("id").Str()

	we2 := created(t, post(t, "/workouts/"+wid+"/exercises", u.Token, map[string]any{"exercise_id": bench}))
	num(t, "second position", we2.Get("position"), 2)
	str(t, "untagged variant_key", we2.Get("variant_key"), "")
	emptyArray(t, "tags", we2.Get("tags"))

	set := created(t, post(t, "/workout-exercises/"+weID+"/sets", u.Token, map[string]any{"weight_kg": 40.5, "reps": 8, "rir": 2}))
	assertSetShape(t, set.JSON)
	num(t, "weight_kg", set.Get("weight_kg"), 40.5)
	num(t, "reps", set.Get("reps"), 8)
	num(t, "rir", set.Get("rir"), 2)
	str(t, "set_type", set.Get("set_type"), "working")
	num(t, "position", set.Get("position"), 1)
	null(t, "duration_s", set.Get("duration_s"))
	null(t, "distance_m", set.Get("distance_m"))
	null(t, "note", set.Get("note"))
	setID := set.Get("id").Str()

	set2 := created(t, post(t, "/workout-exercises/"+weID+"/sets", u.Token, map[string]any{"reps": 6}))
	num(t, "position", set2.Get("position"), 2)
	null(t, "weight_kg", set2.Get("weight_kg"))
	null(t, "rir", set2.Get("rir"))

	// Edit a set; omitted fields stay as they were.
	edited := ok(t, patch(t, "/sets/"+setID, u.Token, map[string]any{"reps": 9}))
	assertSetShape(t, edited.JSON)
	num(t, "reps", edited.Get("reps"), 9)
	num(t, "weight_kg", edited.Get("weight_kg"), 40.5)
	num(t, "an edit that omits rir leaves it alone", edited.Get("rir"), 2)
	str(t, "id", edited.Get("id"), setID)
	num(t, "position", edited.Get("position"), 1)

	// Explicit null clears RIR; a new value replaces it.
	cleared := ok(t, patch(t, "/sets/"+setID, u.Token, map[string]any{"rir": nil}))
	null(t, "rir", cleared.Get("rir"))
	num(t, "reps kept", cleared.Get("reps"), 9)
	reset := ok(t, patch(t, "/sets/"+setID, u.Token, map[string]any{"rir": 0}))
	num(t, "rir", reset.Get("rir"), 0)
	// null on any other field leaves it unchanged (only rir can be cleared).
	same := ok(t, patch(t, "/sets/"+setID, u.Token, map[string]any{"reps": nil, "weight_kg": nil}))
	num(t, "reps unchanged by null", same.Get("reps"), 9)
	num(t, "weight unchanged by null", same.Get("weight_kg"), 40.5)

	// The database rejects out-of-range values, surfaced as 400.
	errBody(t, post(t, "/workout-exercises/"+weID+"/sets", u.Token, map[string]any{"rir": 9}), 400, "invalid value (sets_rir_check)", "")
	errBody(t, patch(t, "/sets/"+setID, u.Token, map[string]any{"rir": 6}), 400, "invalid value (sets_rir_check)", "")

	detail := ok(t, get(t, "/workouts/"+wid, u.Token))
	assertWorkoutShape(t, detail.JSON)
	arrayLen(t, "exercises", detail.Get("exercises"), 2)
	arrayLen(t, "sets of the first exercise", detail.Get("exercises").Idx(0).Get("sets"), 2)
	assertSetShape(t, detail.Get("exercises").Idx(0).Get("sets").Idx(0))

	list := ok(t, get(t, "/workouts", u.Token))
	arrayLen(t, "list", list.JSON, 1)
	assertSummaryShape(t, list.Idx(0))
	num(t, "exercise_count", list.Idx(0).Get("exercise_count"), 2)
	num(t, "set_count", list.Idx(0).Get("set_count"), 2)

	// Removing a set and an exercise soft-deletes them out of the detail view.
	noContent(t, del(t, "/sets/"+setID, u.Token))
	noContent(t, del(t, "/workout-exercises/"+we2.Get("id").Str(), u.Token))
	detail = ok(t, get(t, "/workouts/"+wid, u.Token))
	arrayLen(t, "exercises", detail.Get("exercises"), 1)
	arrayLen(t, "sets", detail.Get("exercises").Idx(0).Get("sets"), 1)
	list = ok(t, get(t, "/workouts", u.Token))
	num(t, "exercise_count after deletes", list.Idx(0).Get("exercise_count"), 1)
	num(t, "set_count after deletes", list.Idx(0).Get("set_count"), 1)
	// Deleting twice is a 404, not a second success.
	notFound(t, del(t, "/sets/"+setID, u.Token))
	notFound(t, del(t, "/workout-exercises/"+we2.Get("id").Str(), u.Token))
	// Editing a deleted set is a 404.
	notFound(t, patch(t, "/sets/"+setID, u.Token, map[string]any{"reps": 1}))

	done := ok(t, post(t, "/workouts/"+wid+"/finish", u.Token, map[string]any{}))
	assertWorkoutShape(t, done.JSON)
	isTrue(t, "ended_at is set", done.Get("ended_at").IsString())
	ended := done.Get("ended_at").Str()
	again := ok(t, post(t, "/workouts/"+wid+"/finish", u.Token, map[string]any{}))
	str(t, "finishing twice must keep the first end time", again.Get("ended_at"), ended)

	noContent(t, del(t, "/workouts/"+wid, u.Token))
	notFound(t, get(t, "/workouts/"+wid, u.Token))
	notFound(t, del(t, "/workouts/"+wid, u.Token))
	notFound(t, post(t, "/workouts/"+wid+"/finish", u.Token, map[string]any{}))
	emptyArray(t, "list after delete", ok(t, get(t, "/workouts", u.Token)).JSON)
}

func assertSummaryShape(t *testing.T, s Val) {
	t.Helper()
	eq(t, "summary field count", s.Len(), 9)
	for _, k := range []string{"id", "title", "gym_id", "gym", "started_at", "ended_at", "timezone", "exercise_count", "set_count"} {
		isTrue(t, "summary has "+k, s.Has(k))
	}
	isTrue(t, "exercise_count is a number", s.Get("exercise_count").IsNumber())
	isTrue(t, "set_count is a number", s.Get("set_count").IsNumber())
}

func TestSetFieldsAndPositions(t *testing.T) {
	t.Parallel()
	u := newUser(t, "setter")
	bench := exerciseBySlug(t, u.Token, "bench-press")
	w := created(t, post(t, "/workouts", u.Token, map[string]any{}))
	wid := w.Get("id").Str()
	we := created(t, post(t, "/workouts/"+wid+"/exercises", u.Token, map[string]any{
		"exercise_id": bench, "superset_group": 2, "notes": "tempo 3-1-1",
	}))
	num(t, "superset_group", we.Get("superset_group"), 2)
	str(t, "notes", we.Get("notes"), "tempo 3-1-1")
	weID := we.Get("id").Str()
	url := "/workout-exercises/" + weID + "/sets"

	// Every field, with a completed_at given in a non-UTC offset (returned normalised to UTC).
	s := created(t, post(t, url, u.Token, map[string]any{
		"set_type": "drop", "weight_kg": 12.345, "reps": 3, "duration_s": 10, "distance_m": 12.5, "rir": 1,
		"note": "hi", "completed_at": "2026-01-01T10:05:00+05:30",
	}))
	assertSetShape(t, s.JSON)
	str(t, "set_type", s.Get("set_type"), "drop")
	num(t, "weight_kg keeps three decimals", s.Get("weight_kg"), 12.345)
	num(t, "reps", s.Get("reps"), 3)
	num(t, "duration_s", s.Get("duration_s"), 10)
	num(t, "distance_m", s.Get("distance_m"), 12.5)
	num(t, "rir", s.Get("rir"), 1)
	str(t, "note", s.Get("note"), "hi")
	str(t, "completed_at", s.Get("completed_at"), "2026-01-01T04:35:00Z")
	first := s.Get("id").Str()

	// An empty set is allowed: working, everything else null, completed now.
	e := created(t, post(t, url, u.Token, map[string]any{}))
	str(t, "set_type default", e.Get("set_type"), "working")
	for _, k := range []string{"weight_kg", "reps", "duration_s", "distance_m", "rir", "note"} {
		null(t, k, e.Get(k))
	}
	num(t, "position", e.Get("position"), 2)
	ts, err := time.Parse(time.RFC3339Nano, e.Get("completed_at").Str())
	if err != nil || time.Since(ts) > time.Minute || time.Until(ts) > time.Minute {
		t.Fatalf("completed_at should be about now, got %s (%v)", e.Get("completed_at"), err)
	}

	for _, st := range []string{"working", "warmup", "drop", "failure"} {
		got := created(t, post(t, url, u.Token, map[string]any{"set_type": st}))
		str(t, "set_type", got.Get("set_type"), st)
	}
	errBody(t, post(t, url, u.Token, map[string]any{"set_type": "bogus"}), 400, "", "")
	errBody(t, post(t, url, u.Token, map[string]any{"reps": -1}), 400, "invalid value (sets_reps_check)", "")
	errBody(t, post(t, url, u.Token, map[string]any{"weight_kg": -1}), 400, "invalid value (sets_weight_kg_check)", "")
	errBody(t, post(t, url, u.Token, map[string]any{"duration_s": -1}), 400, "invalid value (sets_duration_s_check)", "")
	errBody(t, post(t, url, u.Token, map[string]any{"reps": "many"}), 400, "", "")
	errBody(t, post(t, url, u.Token, RawBody("{nope")), 400, "", "")
	notFound(t, post(t, "/workout-exercises/"+newUUID()+"/sets", u.Token, map[string]any{}))

	// Patch every field.
	p := ok(t, patch(t, "/sets/"+first, u.Token, map[string]any{
		"set_type": "failure", "weight_kg": 50, "reps": 1, "duration_s": 5, "distance_m": 1, "note": "n2",
		"completed_at": "2026-01-02T00:00:00Z",
	}))
	str(t, "set_type", p.Get("set_type"), "failure")
	num(t, "weight_kg", p.Get("weight_kg"), 50)
	num(t, "reps", p.Get("reps"), 1)
	num(t, "duration_s", p.Get("duration_s"), 5)
	num(t, "distance_m", p.Get("distance_m"), 1)
	str(t, "note", p.Get("note"), "n2")
	str(t, "completed_at", p.Get("completed_at"), "2026-01-02T00:00:00Z")
	num(t, "rir kept", p.Get("rir"), 1)
	num(t, "position kept", p.Get("position"), 1)
	errBody(t, patch(t, "/sets/"+first, u.Token, map[string]any{"set_type": "bogus"}), 400, "", "")
	errBody(t, patch(t, "/sets/"+first, u.Token, map[string]any{"reps": -5}), 400, "invalid value (sets_reps_check)", "")
	notFound(t, patch(t, "/sets/"+newUUID(), u.Token, map[string]any{"reps": 1}))

	// Positions: the next set goes after the highest remaining one, so removing the last set frees its slot.
	w2 := created(t, post(t, "/workouts", u.Token, map[string]any{}))
	we3 := created(t, post(t, "/workouts/"+w2.Get("id").Str()+"/exercises", u.Token, map[string]any{"exercise_id": bench}))
	u3 := "/workout-exercises/" + we3.Get("id").Str() + "/sets"
	s1 := created(t, post(t, u3, u.Token, map[string]any{"reps": 1}))
	s2 := created(t, post(t, u3, u.Token, map[string]any{"reps": 2}))
	noContent(t, del(t, "/sets/"+s2.Get("id").Str(), u.Token))
	num(t, "slot of the removed last set is reused", created(t, post(t, u3, u.Token, map[string]any{"reps": 3})).Get("position"), 2)
	noContent(t, del(t, "/sets/"+s1.Get("id").Str(), u.Token))
	num(t, "position after the highest remaining", created(t, post(t, u3, u.Token, map[string]any{"reps": 4})).Get("position"), 3)
	d := ok(t, get(t, "/workouts/"+w2.Get("id").Str(), u.Token))
	var reps []float64
	for _, s := range d.Get("exercises").Idx(0).Get("sets").Arr() {
		reps = append(reps, s.Get("reps").Float())
	}
	isTrue(t, "sets are ordered by position", len(reps) == 2 && reps[0] == 3 && reps[1] == 4)

	// Exercise positions follow the same rule.
	a := created(t, post(t, "/workouts/"+w2.Get("id").Str()+"/exercises", u.Token, map[string]any{"exercise_id": bench}))
	num(t, "second exercise", a.Get("position"), 2)
	noContent(t, del(t, "/workout-exercises/"+a.Get("id").Str(), u.Token))
	b := created(t, post(t, "/workouts/"+w2.Get("id").Str()+"/exercises", u.Token, map[string]any{"exercise_id": bench}))
	num(t, "freed exercise slot is reused", b.Get("position"), 2)

	// Removing an exercise removes its sets from the workout's counts.
	noContent(t, del(t, "/workout-exercises/"+we3.Get("id").Str(), u.Token))
	l := ok(t, get(t, "/workouts?limit=100", u.Token))
	for _, s := range l.JSON.Arr() {
		if s.Get("id").Str() == w2.Get("id").Str() {
			num(t, "set_count", s.Get("set_count"), 0)
			num(t, "exercise_count", s.Get("exercise_count"), 1)
		}
	}
}

func TestAddExerciseErrors(t *testing.T) {
	t.Parallel()
	u := newUser(t, "adder")
	bench := exerciseBySlug(t, u.Token, "bench-press")
	w := created(t, post(t, "/workouts", u.Token, map[string]any{}))
	url := "/workouts/" + w.Get("id").Str() + "/exercises"
	errBody(t, post(t, url, u.Token, map[string]any{"exercise_id": newUUID()}), 400, "unknown exercise", "")
	errBody(t, post(t, url, u.Token, map[string]any{"exercise_id": bench, "tag_ids": []string{newUUID()}}), 400,
		"referenced record does not exist (workout_exercise_tags_tag_id_fkey)", "")
	errBody(t, post(t, url, u.Token, map[string]any{}), 400, "", "")
	errBody(t, post(t, url, u.Token, map[string]any{"exercise_id": "nope"}), 400, "", "")
	notFound(t, post(t, "/workouts/"+newUUID()+"/exercises", u.Token, map[string]any{"exercise_id": bench}))
	// Nothing was added by the failures above.
	emptyArray(t, "exercises", ok(t, get(t, "/workouts/"+w.Get("id").Str(), u.Token)).Get("exercises"))

	// Repeating a tag in the request is harmless, and the same tag set always gives the same key.
	incline := tagBySlug(t, u.Token, "incline")
	we := created(t, post(t, url, u.Token, map[string]any{"exercise_id": bench, "tag_ids": []string{incline, incline}}))
	arrayLen(t, "tags", we.Get("tags"), 1)
	str(t, "variant_key", we.Get("variant_key"), "incline")

	// Malformed ids in the path are rejected before reaching the handler.
	status(t, get(t, "/workouts/not-a-uuid", u.Token), 400)
	status(t, get(t, "/exercises/not-a-uuid/last", u.Token), 400)
	status(t, del(t, "/sets/not-a-uuid", u.Token), 400)
}

func TestUsersCannotTouchEachOthersWorkouts(t *testing.T) {
	t.Parallel()
	a := newUser(t, "owner")
	b := newUser(t, "intruder")
	bench := exerciseBySlug(t, a.Token, "bench-press")

	w := created(t, post(t, "/workouts", a.Token, map[string]any{"title": "mine"}))
	wid := w.Get("id").Str()
	we := created(t, post(t, "/workouts/"+wid+"/exercises", a.Token, map[string]any{"exercise_id": bench}))
	weID := we.Get("id").Str()
	set := created(t, post(t, "/workout-exercises/"+weID+"/sets", a.Token, map[string]any{"reps": 5}))
	setID := set.Get("id").Str()

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/workouts/" + wid, nil},
		{"PATCH", "/workouts/" + wid, map[string]any{"title": "pwned"}},
		{"POST", "/workouts/" + wid + "/finish", map[string]any{}},
		{"POST", "/workouts/" + wid + "/exercises", map[string]any{"exercise_id": bench}},
		{"POST", "/workout-exercises/" + weID + "/sets", map[string]any{"reps": 99}},
		{"PATCH", "/sets/" + setID, map[string]any{"reps": 99}},
		{"DELETE", "/sets/" + setID, nil},
		{"DELETE", "/workout-exercises/" + weID, nil},
		{"DELETE", "/workouts/" + wid, nil},
	} {
		r := req(t, c.method, c.path, b.Token, c.body)
		if r.Status != 404 {
			t.Fatalf("%s %s should be 404 for another user, got %s", c.method, c.path, r)
		}
		errBody(t, r, 404, "not found", "")
	}
	emptyArray(t, "intruder's workouts", ok(t, get(t, "/workouts", b.Token)).JSON)

	// The owner's data is untouched.
	d := ok(t, get(t, "/workouts/"+wid, a.Token))
	str(t, "title", d.Get("title"), "mine")
	arrayLen(t, "exercises", d.Get("exercises"), 1)
	num(t, "reps", d.Get("exercises").Idx(0).Get("sets").Idx(0).Get("reps"), 5)
	null(t, "ended_at", d.Get("ended_at"))
}

func TestWorkoutPatchAndListing(t *testing.T) {
	t.Parallel()
	u := newUser(t, "lister")
	bench := exerciseBySlug(t, u.Token, "bench-press")

	// started_at is accepted in any offset and returned in UTC.
	w := created(t, post(t, "/workouts", u.Token, map[string]any{
		"title": "T", "notes": "N", "started_at": "2026-01-02T15:30:00+05:30",
	}))
	str(t, "started_at normalised to UTC", w.Get("started_at"), "2026-01-02T10:00:00Z")
	str(t, "notes", w.Get("notes"), "N")
	wid := w.Get("id").Str()
	errBody(t, post(t, "/workouts", u.Token, map[string]any{"started_at": "not a time"}), 400, "", "")
	errBody(t, post(t, "/workouts", u.Token, map[string]any{"gym_id": newUUID()}), 400,
		"referenced record does not exist (workouts_gym_id_fkey)", "")

	// Omitted fields are unchanged; a field cannot be cleared back to null.
	p := ok(t, patch(t, "/workouts/"+wid, u.Token, map[string]any{"title": "T2"}))
	assertWorkoutShape(t, p.JSON)
	str(t, "title", p.Get("title"), "T2")
	str(t, "notes kept", p.Get("notes"), "N")
	p = ok(t, patch(t, "/workouts/"+wid, u.Token, map[string]any{"title": nil, "notes": nil}))
	str(t, "null does not clear the title", p.Get("title"), "T2")
	str(t, "null does not clear the notes", p.Get("notes"), "N")
	p = ok(t, patch(t, "/workouts/"+wid, u.Token, map[string]any{
		"notes": "N2", "started_at": "2026-01-02T09:00:00Z", "ended_at": "2026-01-02T10:30:00Z",
	}))
	str(t, "notes", p.Get("notes"), "N2")
	str(t, "started_at", p.Get("started_at"), "2026-01-02T09:00:00Z")
	str(t, "ended_at", p.Get("ended_at"), "2026-01-02T10:30:00Z")
	// The end cannot precede the start.
	errBody(t, patch(t, "/workouts/"+wid, u.Token, map[string]any{"ended_at": "2026-01-01T00:00:00Z"}), 400,
		"invalid value (workouts_check)", "")
	errBody(t, patch(t, "/workouts/"+wid, u.Token, map[string]any{"gym_id": newUUID()}), 400,
		"referenced record does not exist (workouts_gym_id_fkey)", "")
	errBody(t, patch(t, "/workouts/"+wid, u.Token, map[string]any{"ended_at": "garbage"}), 400, "", "")
	// Finishing an already-ended workout keeps its recorded end.
	f := ok(t, post(t, "/workouts/"+wid+"/finish", u.Token, map[string]any{}))
	str(t, "ended_at kept", f.Get("ended_at"), "2026-01-02T10:30:00Z")

	// Listing: newest first, limit clamped to 1..=100, `before` is exclusive.
	logWorkout(t, u.Token, "2026-01-05T10:00:00Z", entry{exercise: bench, sets: []map[string]any{kg(40, 5)}})
	logWorkout(t, u.Token, "2026-01-03T10:00:00Z")
	list := ok(t, get(t, "/workouts", u.Token)).JSON
	arrayLen(t, "list", list, 3)
	sameStrings(t, "started_at order", strs(list, "started_at"),
		[]string{"2026-01-05T10:00:00Z", "2026-01-03T10:00:00Z", "2026-01-02T09:00:00Z"})
	num(t, "set_count", list.Idx(0).Get("set_count"), 1)
	num(t, "exercise_count", list.Idx(0).Get("exercise_count"), 1)
	num(t, "empty workout exercise_count", list.Idx(1).Get("exercise_count"), 0)
	num(t, "empty workout set_count", list.Idx(1).Get("set_count"), 0)
	arrayLen(t, "limit=0 clamps to 1", ok(t, get(t, "/workouts?limit=0", u.Token)).JSON, 1)
	arrayLen(t, "limit=-4 clamps to 1", ok(t, get(t, "/workouts?limit=-4", u.Token)).JSON, 1)
	arrayLen(t, "limit=2", ok(t, get(t, "/workouts?limit=2", u.Token)).JSON, 2)
	arrayLen(t, "limit=1000 is allowed", ok(t, get(t, "/workouts?limit=1000", u.Token)).JSON, 3)
	before := ok(t, get(t, "/workouts?before=2026-01-03T10:00:00Z", u.Token)).JSON
	sameStrings(t, "before is exclusive", strs(before, "started_at"), []string{"2026-01-02T09:00:00Z"})
	// An offset in `before` works when URL-encoded (+ -> %2B).
	before = ok(t, get(t, "/workouts?before=2026-01-03T15%3A30%3A00%2B05%3A30", u.Token)).JSON
	sameStrings(t, "before with offset", strs(before, "started_at"), []string{"2026-01-02T09:00:00Z"})
	emptyArray(t, "before the first", ok(t, get(t, "/workouts?before=2000-01-01T00:00:00Z", u.Token)).JSON)
	status(t, get(t, "/workouts?before=bad", u.Token), 400)
}

func TestWorkoutTimezoneRecording(t *testing.T) {
	t.Parallel()
	u := newUser(t, "typo")
	for _, bad := range []string{"Mars/Olympus", "", "UTC+5", "../etc/passwd"} {
		errBody(t, post(t, "/workouts", u.Token, map[string]any{"timezone": bad}), 400, "unknown timezone", "")
	}
	emptyArray(t, "a refused request created nothing", ok(t, get(t, "/workouts", u.Token)).JSON)

	w := created(t, post(t, "/workouts", u.Token, map[string]any{"timezone": "Europe/London"}))
	str(t, "timezone", w.Get("timezone"), "Europe/London")
	str(t, "timezone persisted", ok(t, get(t, "/workouts/"+w.Get("id").Str(), u.Token)).Get("timezone"), "Europe/London")
	str(t, "timezone in the list", ok(t, get(t, "/workouts", u.Token)).Idx(0).Get("timezone"), "Europe/London")

	// The database refuses an unknown zone too, so a script or a future code path cannot get one in either.
	requireDB(t)
	if out, err := trySQL(t, "update workouts set timezone = 'Nowhere/Land' where id = '"+w.Get("id").Str()+"'"); err == nil {
		t.Fatalf("the trigger should reject an unknown zone, got %q", out)
	}
	str(t, "timezone unchanged", ok(t, get(t, "/workouts/"+w.Get("id").Str(), u.Token)).Get("timezone"), "Europe/London")
}
