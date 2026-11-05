package acceptance

import (
	"testing"
	"time"
)

func putWeight(t *testing.T, token, date string, kg any) Resp {
	t.Helper()
	return put(t, "/body-weights/"+date, token, map[string]any{"weight_kg": kg})
}

func TestBodyWeightsAreOnePerDayPrivateAndValidated(t *testing.T) {
	t.Parallel()
	u := newUser(t, "lifter")
	other := newUser(t, "someone_else")

	first := ok(t, putWeight(t, u.Token, "2026-01-05", 81.4))
	eq(t, "field count", first.JSON.Len(), 2)
	str(t, "date", first.Get("date"), "2026-01-05")
	num(t, "weight_kg", first.Get("weight_kg"), 81.4)
	ok(t, putWeight(t, u.Token, "2026-01-03", 82))
	// Logging the same day again replaces it instead of adding a second entry.
	again := ok(t, putWeight(t, u.Token, "2026-01-05", 81.0))
	num(t, "weight_kg", again.Get("weight_kg"), 81)

	// Oldest first, which is the order a chart wants.
	list := ok(t, get(t, "/body-weights", u.Token)).JSON
	arrayLen(t, "list", list, 2)
	str(t, "list[0].date", list.Idx(0).Get("date"), "2026-01-03")
	num(t, "list[0].weight_kg", list.Idx(0).Get("weight_kg"), 82)
	str(t, "list[1].date", list.Idx(1).Get("date"), "2026-01-05")
	num(t, "list[1].weight_kg", list.Idx(1).Get("weight_kg"), 81)
	eq(t, "entry field count", list.Idx(0).Len(), 2)

	// from and to are inclusive.
	arrayLen(t, "from", ok(t, get(t, "/body-weights?from=2026-01-04", u.Token)).JSON, 1)
	arrayLen(t, "from inclusive", ok(t, get(t, "/body-weights?from=2026-01-05", u.Token)).JSON, 1)
	arrayLen(t, "to", ok(t, get(t, "/body-weights?to=2026-01-04", u.Token)).JSON, 1)
	arrayLen(t, "to inclusive", ok(t, get(t, "/body-weights?to=2026-01-03", u.Token)).JSON, 1)
	arrayLen(t, "from and to", ok(t, get(t, "/body-weights?from=2026-01-03&to=2026-01-05", u.Token)).JSON, 2)
	emptyArray(t, "empty range", ok(t, get(t, "/body-weights?from=2026-02-01", u.Token)).JSON)
	errBody(t, get(t, "/body-weights?from=bad", u.Token), 400, "date must be YYYY-MM-DD", "")
	errBody(t, get(t, "/body-weights?to=2026-13-01", u.Token), 400, "date must be YYYY-MM-DD", "")

	emptyArray(t, "entries are private", ok(t, get(t, "/body-weights", other.Token)).JSON)
	notFound(t, del(t, "/body-weights/2026-01-05", other.Token))

	// Weights are rounded to two decimals; the open interval (0, 500) is allowed.
	r := ok(t, putWeight(t, u.Token, "2026-01-06", 81.456))
	num(t, "rounded", r.Get("weight_kg"), 81.46)
	num(t, "tiny positive", ok(t, putWeight(t, u.Token, "2026-01-07", 0.01)).Get("weight_kg"), 0.01)
	num(t, "just under 500", ok(t, putWeight(t, u.Token, "2026-01-08", 499.99)).Get("weight_kg"), 499.99)
	for i, bad := range []any{0, -3, 500, 1000, 500.001, 0.001} {
		_ = i
		errBody(t, putWeight(t, u.Token, "2026-01-09", bad), 400, "weight_kg must be between 0 and 500", "")
	}
	errBody(t, put(t, "/body-weights/2026-01-09", u.Token, map[string]any{}), 400, "", "")
	errBody(t, put(t, "/body-weights/2026-01-09", u.Token, RawBody("{nope")), 400, "", "")
	errBody(t, putWeight(t, u.Token, "2999-01-01", 80), 400, "date cannot be in the future", "")
	for _, bad := range []string{"not-a-date", "2026-13-05", "2026-02-30", "2026-1-5", "20260105"} {
		errBody(t, putWeight(t, u.Token, bad, 80), 400, "date must be YYYY-MM-DD", "")
	}
	unauthorized(t, get(t, "/body-weights", ""))
	unauthorized(t, put(t, "/body-weights/2026-01-05", "", map[string]any{"weight_kg": 80}))
	unauthorized(t, del(t, "/body-weights/2026-01-05", ""))

	noContent(t, del(t, "/body-weights/2026-01-05", u.Token))
	notFound(t, del(t, "/body-weights/2026-01-05", u.Token))
	errBody(t, del(t, "/body-weights/xx", u.Token), 400, "date must be YYYY-MM-DD", "")
	// Of 2026-01-03, -06, -07 and -08 remain.
	arrayLen(t, "list after delete", ok(t, get(t, "/body-weights", u.Token)).JSON, 4)
}

func TestBodyWeightListKeepsTheNewestEntriesWhenCapped(t *testing.T) {
	t.Parallel()
	requireDB(t)
	u := newUser(t, "longtime")
	sql(t, "insert into body_weights (user_id, measured_on, weight_kg) select '"+u.ID+
		"', d::date, 80 from generate_series('2010-01-01'::date, '2010-01-01'::date + 5001, '1 day') d")

	list := ok(t, get(t, "/body-weights", u.Token)).JSON
	arrayLen(t, "capped list", list, 5000)
	str(t, "the two oldest days are the ones dropped", list.Idx(0).Get("date"), "2010-01-03")
	str(t, "newest", list.Idx(4999).Get("date"), "2023-09-11")
}

func TestABodyWeightDatedAheadOfTheServersClockIsFineForATraveller(t *testing.T) {
	t.Parallel()
	u := newUser(t, "eastbound")
	// The furthest-ahead local date anywhere is UTC+14. A phone there is on that date; one day later cannot exist yet.
	// Compute the date on both sides of the request in case midnight UTC+14 passes mid-test.
	ahead := time.Now().UTC().Add(14 * time.Hour)
	r := putWeight(t, u.Token, ymd(ahead), 80)
	if r.Status != 200 && ymd(time.Now().UTC().Add(14*time.Hour)) == ymd(ahead) {
		t.Fatalf("a date on UTC+14 must be accepted: %s", r)
	}
	if r.Status == 200 {
		errBody(t, putWeight(t, u.Token, ymd(ahead.AddDate(0, 0, 1)), 80), 400, "date cannot be in the future", "")
	}
}
