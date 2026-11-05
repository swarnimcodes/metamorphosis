package acceptance

import (
	"net/url"
	"sort"
	"testing"
)

func search(t *testing.T, token, q string) Val {
	t.Helper()
	return ok(t, get(t, "/exercises?q="+q, token)).JSON
}

func TestExerciseSearchResolvesAliasesAndPunctuation(t *testing.T) {
	t.Parallel()
	u := newUser(t, "searcher")
	for _, c := range []struct{ q, slug string }{
		{"lat%20pulldowns", "lat-pulldown"},
		{"pulldown", "lat-pulldown"},
		{"Lat-Pull-Down", "lat-pulldown"},
		{"RDL", "romanian-deadlift"},
		{"pull-up", "pull-up"},
		{"triceps%20extension", "tricep-extension"},
	} {
		slugs := strs(search(t, u.Token, c.q), "slug")
		isTrue(t, "q="+c.q+" expected "+c.slug+", got "+join(slugs), contains(slugs, c.slug))
	}
	all := ok(t, get(t, "/exercises?limit=5", u.Token)).JSON
	arrayLen(t, "limit=5", all, 5)
}

func join(s []string) string {
	out := ""
	for _, e := range s {
		out += e + ","
	}
	return out
}

func TestExerciseSearchShapeAndLimits(t *testing.T) {
	t.Parallel()
	u := newUser(t, "shape")
	list := ok(t, get(t, "/exercises", u.Token)).JSON
	arrayLen(t, "default limit is 20", list, 20)
	for _, e := range list.Arr() {
		eq(t, "field count", e.Len(), 6)
		isTrue(t, "id is a uuid", uuidish(e.Get("id").Str()))
		isTrue(t, "slug is a string", e.Get("slug").IsString())
		isTrue(t, "name is a string", e.Get("name").IsString() && e.Get("name").Str() != "")
		isTrue(t, "kind is one of the exercise kinds",
			contains([]string{"weight_reps", "bodyweight_reps", "duration", "distance"}, e.Get("kind").Str()))
		str(t, "status", e.Get("status"), "official")
		emptyArray(t, "suggested_tag_ids", e.Get("suggested_tag_ids"))
	}

	// limit is clamped to 1..=50.
	for _, q := range []string{"limit=0", "limit=-3"} {
		arrayLen(t, q, ok(t, get(t, "/exercises?"+q, u.Token)).JSON, 1)
	}
	n := ok(t, get(t, "/exercises?limit=500", u.Token)).JSON.Len()
	isTrue(t, "limit=500 returns the whole catalog, at most 50", n >= 34 && n <= 50)

	// Blank or punctuation-only queries behave like no query.
	for _, q := range []string{"", "%20%20", "!!!"} {
		arrayLen(t, "q="+q, search(t, u.Token, q), 20)
	}
	// Nothing matches gibberish.
	emptyArray(t, "gibberish", search(t, u.Token, "zzqqxxjjkkvv"))

	// A kind per catalog entry: spot-check each kind.
	for slug, kind := range map[string]string{"bench-press": "weight_reps", "plank": "duration", "running": "distance"} {
		for _, e := range search(t, u.Token, slug).Arr() {
			if e.Get("slug").Str() == slug {
				str(t, slug+" kind", e.Get("kind"), kind)
			}
		}
	}
}

func TestTagsList(t *testing.T) {
	t.Parallel()
	u := newUser(t, "tags")
	list := ok(t, get(t, "/tags", u.Token)).JSON
	isTrue(t, "the tag catalog has entries", list.Len() >= 27)
	var order []string
	for _, tag := range list.Arr() {
		eq(t, "field count", tag.Len(), 4)
		isTrue(t, "id is a uuid", uuidish(tag.Get("id").Str()))
		isTrue(t, "slug", tag.Get("slug").Str() != "")
		isTrue(t, "name", tag.Get("name").Str() != "")
		isTrue(t, "category is a known category",
			contains([]string{"equipment", "grip", "angle", "stance", "other"}, tag.Get("category").Str()))
		order = append(order, tag.Get("category").Str())
	}
	// Ordered by category (enum declaration order: equipment, grip, angle, stance, other), then name.
	rank := map[string]int{"equipment": 0, "grip": 1, "angle": 2, "stance": 3, "other": 4}
	isTrue(t, "tags are ordered by category", sort.SliceIsSorted(order, func(i, j int) bool { return rank[order[i]] < rank[order[j]] }))
	byName := map[string][]string{}
	for _, tag := range list.Arr() {
		c := tag.Get("category").Str()
		byName[c] = append(byName[c], tag.Get("name").Str())
	}
	for c, names := range byName {
		isTrue(t, "tags within "+c+" are ordered by name", sort.StringsAreSorted(names))
	}
	// Spot-check known entries.
	for _, tag := range list.Arr() {
		if tag.Get("slug").Str() == "ez-bar" {
			str(t, "ez-bar name", tag.Get("name"), "EZ Bar")
			str(t, "ez-bar category", tag.Get("category"), "equipment")
		}
		if tag.Get("slug").Str() == "preacher" {
			str(t, "preacher category", tag.Get("category"), "equipment")
		}
	}
}

func TestExerciseVisibilityHidesOtherUsersAndMergedRows(t *testing.T) {
	t.Parallel()
	requireDB(t)
	a := newUser(t, "alice")
	b := newUser(t, "bobby")
	bench := exerciseBySlug(t, a.Token, "bench-press")
	tok := randLetters(10)

	private := sql(t, "insert into exercises (name, kind, status, created_by) values ('Zottman "+tok+"', 'weight_reps', 'user', '"+
		a.ID+"') returning id")
	merged := sql(t, "insert into exercises (slug, name, kind, status, merged_into_id) values ('old-dup-"+tok+"', 'Old Dup Press "+tok+
		"', 'weight_reps', 'official', '"+bench+"') returning id")

	mine := search(t, a.Token, "zottman%20"+tok)
	arrayLen(t, "owner sees their private exercise", mine, 1)
	str(t, "private name", mine.Idx(0).Get("name"), "Zottman "+tok)
	str(t, "private status", mine.Idx(0).Get("status"), "user")
	null(t, "private slug", mine.Idx(0).Get("slug"))
	str(t, "private id", mine.Idx(0).Get("id"), private)
	emptyArray(t, "other user's private exercise leaked", search(t, b.Token, "zottman%20"+tok))
	isTrue(t, "merged exercise should be hidden", !contains(strs(search(t, a.Token, "old%20dup%20press%20"+tok), "id"), merged))
	isTrue(t, "merged exercise should be hidden from other users too", !contains(strs(search(t, b.Token, "old%20dup%20press%20"+tok), "id"), merged))

	// Bob cannot log someone else's private exercise either.
	w := created(t, post(t, "/workouts", b.Token, map[string]any{}))
	r := post(t, "/workouts/"+w.Get("id").Str()+"/exercises", b.Token, map[string]any{"exercise_id": private})
	errBody(t, r, 400, "unknown exercise", "")
	// The owner can.
	wa := created(t, post(t, "/workouts", a.Token, map[string]any{}))
	we := created(t, post(t, "/workouts/"+wa.Get("id").Str()+"/exercises", a.Token, map[string]any{"exercise_id": private}))
	str(t, "exercise_name", we.Get("exercise_name"), "Zottman "+tok)
}

func TestSearchTurnsTagWordsIntoSuggestedTags(t *testing.T) {
	t.Parallel()
	u := newUser(t, "tagger")
	preacher := tagBySlug(t, u.Token, "preacher")
	incline := tagBySlug(t, u.Token, "incline")
	dumbbell := tagBySlug(t, u.Token, "dumbbell")
	cable := tagBySlug(t, u.Token, "cable")

	ids := func(hit Val) []string {
		var v []string
		for _, e := range hit.Get("suggested_tag_ids").Arr() {
			v = append(v, e.Str())
		}
		sort.Strings(v)
		return v
	}
	sorted := func(tags ...string) []string {
		v := append([]string{}, tags...)
		sort.Strings(v)
		return v
	}

	// The motivating case: "preacher curl" is Bicep Curl with the preacher tag.
	for _, q := range []string{"preacher%20curl", "preacher%20curls", "Preacher-Curl"} {
		r := search(t, u.Token, q)
		str(t, "q="+q+" slug", r.Idx(0).Get("slug"), "bicep-curl")
		sameStrings(t, "q="+q+" tags", ids(r.Idx(0)), sorted(preacher))
	}

	// Several tags at once, in any order, around the exercise name.
	r := search(t, u.Token, "incline%20dumbbell%20bench%20press")
	str(t, "slug", r.Idx(0).Get("slug"), "bench-press")
	sameStrings(t, "tags", ids(r.Idx(0)), sorted(incline, dumbbell))
	r = search(t, u.Token, "cable%20lateral%20raise")
	str(t, "slug", r.Idx(0).Get("slug"), "lateral-raise")
	sameStrings(t, "tags", ids(r.Idx(0)), sorted(cable))

	// A lone tag word lists the exercises that suggest it.
	r = search(t, u.Token, "preacher")
	str(t, "slug", r.Idx(0).Get("slug"), "bicep-curl")
	sameStrings(t, "tags", ids(r.Idx(0)), sorted(preacher))

	// Exact names and aliases are never reinterpreted, even if they contain a tag word.
	for _, c := range []struct{ q, slug string }{
		{"overhead%20press", "overhead-press"}, {"bicep%20curl", "bicep-curl"}, {"lat%20pulldowns", "lat-pulldown"},
	} {
		r := search(t, u.Token, c.q)
		str(t, "q="+c.q, r.Idx(0).Get("slug"), c.slug)
		eq(t, "q="+c.q+" must not suggest tags", len(ids(r.Idx(0))), 0)
	}

	// Plain results always carry the field, empty (never null or omitted).
	all := ok(t, get(t, "/exercises?limit=3", u.Token)).JSON
	for _, e := range all.Arr() {
		emptyArray(t, "suggested_tag_ids", e.Get("suggested_tag_ids"))
	}
}

func TestSearchHandlesPartiallyTypedTagWords(t *testing.T) {
	t.Parallel()
	u := newUser(t, "typist")
	preacher := tagBySlug(t, u.Token, "preacher")
	dumbbell := tagBySlug(t, u.Token, "dumbbell")
	ezBar := tagBySlug(t, u.Token, "ez-bar")

	suggested := func(hit Val) []string {
		var v []string
		for _, e := range hit.Get("suggested_tag_ids").Arr() {
			v = append(v, e.Str())
		}
		return v
	}
	only := func(tag string) []string { return []string{tag} }

	// Every prefix from three letters on finds the preacher curl, as you type.
	for _, q := range []string{"pre", "prea", "preac", "preach", "preache", "preacher"} {
		r := search(t, u.Token, q)
		var hit *Val
		for _, e := range r.Arr() {
			if e.Get("slug").Str() == "bicep-curl" {
				e := e
				hit = &e
			}
		}
		if hit == nil {
			t.Fatalf("q=%s: bicep-curl missing from %s", q, r)
		}
		sameStrings(t, "q="+q, suggested(*hit), only(preacher))
	}
	// ...also with the exercise word following a partial tag word.
	r := search(t, u.Token, "preach%20curl")
	str(t, "slug", r.Idx(0).Get("slug"), "bicep-curl")
	sameStrings(t, "preach curl", suggested(r.Idx(0)), only(preacher))
	r = search(t, u.Token, "dumbb%20curl")
	str(t, "slug", r.Idx(0).Get("slug"), "bicep-curl")
	sameStrings(t, "dumbb curl", suggested(r.Idx(0)), only(dumbbell))
	r = search(t, u.Token, "preacher%20cu")
	str(t, "slug", r.Idx(0).Get("slug"), "bicep-curl")
	sameStrings(t, "preacher cu", suggested(r.Idx(0)), only(preacher))

	// A multi-word tag is anchored by its first word, so one more letter is enough.
	r = search(t, u.Token, "ez%20b")
	found := false
	for _, e := range r.Arr() {
		if e.Get("slug").Str() == "bicep-curl" {
			s := suggested(e)
			found = found || (len(s) == 1 && s[0] == ezBar)
		}
	}
	isTrue(t, "ez b suggests ez-bar on bicep-curl: "+r.String(), found)

	// Mid-way through an exercise name, name matches stay first.
	r = search(t, u.Token, "overhead")
	str(t, "slug", r.Idx(0).Get("slug"), "overhead-press")
	eq(t, "no suggestions", len(suggested(r.Idx(0))), 0)

	// Too short to guess: two letters never become a tag.
	for _, e := range search(t, u.Token, "pr").Arr() {
		eq(t, "pr suggests nothing for "+e.Get("slug").Str(), len(suggested(e)), 0)
	}
}

// ---- last performance ---------------------------------------------------------------------

// logBench logs a workout with two sets of `reps` and `reps + 1`; returns the workout id.
func logBench(t *testing.T, token, bench, startedAt string, tag string, reps int) string {
	t.Helper()
	w := created(t, post(t, "/workouts", token, map[string]any{"started_at": startedAt}))
	wid := w.Get("id").Str()
	body := map[string]any{"exercise_id": bench}
	if tag != "" {
		body["tag_ids"] = []string{tag}
	}
	we := created(t, post(t, "/workouts/"+wid+"/exercises", token, body))
	for _, r := range []int{reps, reps + 1} {
		created(t, post(t, "/workout-exercises/"+we.Get("id").Str()+"/sets", token, map[string]any{"reps": r, "weight_kg": 60}))
	}
	return wid
}

func TestLastPerformanceMatchesVariantAndOwner(t *testing.T) {
	t.Parallel()
	a := newUser(t, "lifter")
	b := newUser(t, "other")
	bench := exerciseBySlug(t, a.Token, "bench-press")
	cable := tagBySlug(t, a.Token, "cable")
	last := "/exercises/" + bench + "/last"

	notFound(t, get(t, last, a.Token))

	oldPlain := logBench(t, a.Token, bench, "2026-01-01T10:00:00Z", "", 5)
	r := ok(t, get(t, last, a.Token))
	eq(t, "field count", r.JSON.Len(), 3)
	str(t, "workout_id", r.Get("workout_id"), oldPlain)
	str(t, "started_at", r.Get("started_at"), "2026-01-01T10:00:00Z")
	arrayLen(t, "sets", r.Get("sets"), 2)
	num(t, "sets[0].reps", r.Get("sets").Idx(0).Get("reps"), 5)
	num(t, "sets[1].reps", r.Get("sets").Idx(1).Get("reps"), 6)
	num(t, "sets[0].weight_kg", r.Get("sets").Idx(0).Get("weight_kg"), 60)

	// A newer cable workout does not leak into the untagged lookup, and vice versa.
	cableW := logBench(t, a.Token, bench, "2026-02-01T10:00:00Z", cable, 8)
	str(t, "untagged still the old one", ok(t, get(t, last, a.Token)).Get("workout_id"), oldPlain)
	r = ok(t, get(t, last+"?variant_key=cable", a.Token))
	str(t, "cable workout", r.Get("workout_id"), cableW)
	num(t, "cable reps", r.Get("sets").Idx(0).Get("reps"), 8)
	notFound(t, get(t, last+"?variant_key=v-bar", a.Token))

	// Another user's newer workout is invisible.
	logBench(t, b.Token, bench, "2026-03-01T10:00:00Z", "", 20)
	str(t, "other user's data invisible", ok(t, get(t, last, a.Token)).Get("workout_id"), oldPlain)

	// Soft-deleted workouts are skipped, falling back to the older one.
	newer := logBench(t, a.Token, bench, "2026-04-01T10:00:00Z", "", 10)
	str(t, "newer", ok(t, get(t, last, a.Token)).Get("workout_id"), newer)
	noContent(t, del(t, "/workouts/"+newer, a.Token))
	str(t, "fallback", ok(t, get(t, last, a.Token)).Get("workout_id"), oldPlain)

	// The other user sees only their own history.
	num(t, "b reps", ok(t, get(t, last, b.Token)).Get("sets").Idx(0).Get("reps"), 20)

	// Sets that were removed no longer show.
	unauthorized(t, get(t, last, ""))
}

func TestLastPerformanceFollowsMergedExercises(t *testing.T) {
	t.Parallel()
	requireDB(t)
	a := newUser(t, "lifter")
	bench := exerciseBySlug(t, a.Token, "bench-press")
	// A throwaway exercise merged into bench, so no shared state changes.
	tok := randLetters(10)
	dup := sql(t, "insert into exercises (slug, name, kind, status, merged_into_id) values ('merged-"+tok+"', 'Merged "+tok+
		"', 'weight_reps', 'official', '"+bench+"') returning id")
	wid := logBench(t, a.Token, bench, "2026-01-01T10:00:00Z", "", 5)

	r := ok(t, get(t, "/exercises/"+dup+"/last", a.Token))
	str(t, "workout via the merged-away id", r.Get("workout_id"), wid)
}

func TestAddingAMergedExerciseLogsTheCanonicalOne(t *testing.T) {
	t.Parallel()
	requireDB(t)
	a := newUser(t, "merger")
	bench := exerciseBySlug(t, a.Token, "bench-press")
	tok := randLetters(10)
	dup := sql(t, "insert into exercises (slug, name, kind, status, merged_into_id) values ('mergedadd-"+tok+"', 'Merged Add "+tok+
		"', 'weight_reps', 'official', '"+bench+"') returning id")
	w := created(t, post(t, "/workouts", a.Token, map[string]any{}))
	we := created(t, post(t, "/workouts/"+w.Get("id").Str()+"/exercises", a.Token, map[string]any{"exercise_id": dup}))
	str(t, "history lands on the canonical exercise", we.Get("exercise_id"), bench)
	str(t, "exercise_name", we.Get("exercise_name"), "Bench Press")
}

var _ = url.QueryEscape
