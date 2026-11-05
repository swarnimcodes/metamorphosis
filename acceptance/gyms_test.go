package acceptance

import (
	"net/url"
	"strings"
	"testing"
)

// gymBody is a gym creation request with a unique city so the duplicate guard only sees this test's gyms.
func createGym(t *testing.T, token string, body any) Resp {
	t.Helper()
	return post(t, "/gyms", token, body)
}

func assertGymShape(t *testing.T, g Val) {
	t.Helper()
	eq(t, "gym field count", g.Len(), 10)
	for _, k := range []string{"id", "name", "chain", "area", "address", "city", "lat", "lng", "maps_url", "verified"} {
		isTrue(t, "gym has "+k, g.Has(k))
	}
	isTrue(t, "id is a uuid", uuidish(g.Get("id").Str()))
	isTrue(t, "verified is a bool", g.Get("verified").IsBool())
}

func idsOf(v Val) []string { return strs(v, "id") }

func TestGymCreateEchoesFieldsAndTrims(t *testing.T) {
	t.Parallel()
	u := newUser(t, "gymmaker")
	tok := randLetters(10)
	city := "Cty" + randLetters(8)
	g := created(t, createGym(t, u.Token, map[string]any{
		"name": "  Plain Gym " + tok + "  ", "chain": " Chainy ", "area": "  Ar" + tok + " ", "address": " 1 Main St ",
		"city": "  " + city + "  ",
	}))
	assertGymShape(t, g.JSON)
	str(t, "name trimmed", g.Get("name"), "Plain Gym "+tok)
	str(t, "chain trimmed", g.Get("chain"), "Chainy")
	str(t, "area trimmed", g.Get("area"), "Ar"+tok)
	str(t, "address trimmed", g.Get("address"), "1 Main St")
	str(t, "city trimmed", g.Get("city"), city)
	null(t, "lat", g.Get("lat"))
	null(t, "lng", g.Get("lng"))
	null(t, "maps_url", g.Get("maps_url"))
	eq(t, "verified", g.Get("verified").Bool(), false)

	// Blank optional strings are stored as null.
	b := created(t, createGym(t, u.Token, map[string]any{
		"name": "Bare Gym " + randLetters(10), "chain": "  ", "area": "", "address": " ", "city": "\t", "force": true,
	}))
	for _, k := range []string{"chain", "area", "address", "city", "maps_url"} {
		null(t, k, b.Get(k))
	}

	errBody(t, createGym(t, u.Token, map[string]any{"name": "   "}), 400, "name must not be empty", "")
	errBody(t, createGym(t, u.Token, map[string]any{"name": ""}), 400, "name must not be empty", "")
	errBody(t, createGym(t, u.Token, map[string]any{}), 400, "", "")
	errBody(t, createGym(t, u.Token, RawBody("[]")), 400, "", "")
	errBody(t, createGym(t, u.Token, map[string]any{"name": "X", "force": "yes"}), 400, "", "")
	unauthorized(t, createGym(t, "", map[string]any{"name": "No Login"}))
	unauthorized(t, get(t, "/gyms", ""))
}

func TestGymSearchNormalisesApostrophesAndRanksRecentGymsFirst(t *testing.T) {
	t.Parallel()
	a := newUser(t, "gymrat")
	city := "Cty" + randLetters(8)
	tok1, tok2, tok3 := randLetters(10), randLetters(10), randLetters(10)

	golds := created(t, createGym(t, a.Token, map[string]any{"name": "Golds Gym Aundh " + tok1, "city": city}))
	assertGymShape(t, golds.JSON)
	eq(t, "verified", golds.Get("verified").Bool(), false)
	anytime := created(t, createGym(t, a.Token, map[string]any{"name": "Anytime Fitness Baner " + tok2, "city": city}))

	found := ok(t, get(t, "/gyms?q="+url.QueryEscape("gold's gym aundh "+tok1), a.Token)).JSON
	assertGymShape(t, found.Idx(0))
	str(t, "found[0].id", found.Idx(0).Get("id"), golds.Get("id").Str())
	str(t, "found[0].name", found.Idx(0).Get("name"), "Golds Gym Aundh "+tok1)
	// The curly apostrophe is dropped the same way.
	found = ok(t, get(t, "/gyms?q="+url.QueryEscape("gold’s gym aundh "+tok1), a.Token)).JSON
	str(t, "curly apostrophe", found.Idx(0).Get("id"), golds.Get("id").Str())
	// Every word of the query must appear in the name, area or city, in any order.
	found = ok(t, get(t, "/gyms?q="+url.QueryEscape(tok1+" AUNDH"), a.Token)).JSON
	str(t, "word order and case do not matter", found.Idx(0).Get("id"), golds.Get("id").Str())
	isTrue(t, "the other gym is not a match", !contains(idsOf(found), anytime.Get("id").Str()))
	// The city is searched too.
	found = ok(t, get(t, "/gyms?q="+url.QueryEscape(strings.ToUpper(city)+" "+tok2), a.Token)).JSON
	str(t, "city + name", found.Idx(0).Get("id"), anytime.Get("id").Str())
	emptyArray(t, "gibberish", ok(t, get(t, "/gyms?q=zzzzqqq", a.Token)).JSON)

	// Empty query: gyms the caller trained at come first, most recent first, then verified, then by name.
	b := newUser(t, "newbie")
	created(t, createGym(t, b.Token, map[string]any{"name": "Aaa Fitness " + tok3, "city": "Cty" + randLetters(8)}))
	for _, c := range []struct{ gym, at string }{
		{anytime.Get("id").Str(), "2026-01-02T09:00:00Z"},
		{golds.Get("id").Str(), "2026-01-01T09:00:00Z"},
	} {
		created(t, post(t, "/workouts", a.Token, map[string]any{"gym_id": c.gym, "started_at": c.at}))
	}
	list := ok(t, get(t, "/gyms", a.Token)).JSON
	names := strs(list, "name")
	sameStrings(t, "first gyms for the caller", names[:3],
		[]string{"Anytime Fitness Baner " + tok2, "Golds Gym Aundh " + tok1, "Cult Kalyani Nagar"})
	// Someone else's workouts do not reorder mine.
	theirs := ok(t, get(t, "/gyms", b.Token)).JSON
	str(t, "verified gyms lead for a user with no history", theirs.Idx(0).Get("name"), "Cult Kalyani Nagar")
	arrayLen(t, "limit=1", ok(t, get(t, "/gyms?limit=1", a.Token)).JSON, 1)
	arrayLen(t, "limit=0 clamps to 1", ok(t, get(t, "/gyms?limit=0", a.Token)).JSON, 1)
	arrayLen(t, "limit=-1 clamps to 1", ok(t, get(t, "/gyms?limit=-1", a.Token)).JSON, 1)
	isTrue(t, "the default limit is 20", list.Len() <= 20)
	isTrue(t, "limit is capped at 50", ok(t, get(t, "/gyms?limit=500", a.Token)).JSON.Len() <= 50)
}

func TestGymDuplicateGuardReturns409AndForceOverrides(t *testing.T) {
	t.Parallel()
	a := newUser(t, "gymrat")
	tok := randLetters(10)
	city := "Cty" + randLetters(8)
	golds := created(t, createGym(t, a.Token, map[string]any{"name": "Golds Gym Aundh " + tok, "city": city}))

	r := createGym(t, a.Token, map[string]any{"name": "Gold's Gym Aundh " + tok, "city": strings.ToLower(city)})
	status(t, r, 409)
	eq(t, "409 body keys", r.JSON.Len(), 2)
	str(t, "error", r.Get("error"), "similar gyms exist")
	isTrue(t, "no code on this conflict", !r.JSON.Has("code"))
	arrayLen(t, "similar", r.Get("similar"), 1)
	assertGymShape(t, r.Get("similar").Idx(0))
	str(t, "similar[0].id", r.Get("similar").Idx(0).Get("id"), golds.Get("id").Str())
	str(t, "similar[0].name", r.Get("similar").Idx(0).Get("name"), "Golds Gym Aundh "+tok)

	// The same name in another city is a different place; unrelated names pass.
	city2 := "Cty" + randLetters(8)
	created(t, createGym(t, a.Token, map[string]any{"name": "Golds Gym Aundh " + tok, "city": city2}))
	created(t, createGym(t, a.Token, map[string]any{"name": "Fit Kothrud " + randLetters(10), "city": city}))

	count := func() int {
		n := 0
		for _, g := range ok(t, get(t, "/gyms?limit=50&q="+tok, a.Token)).JSON.Arr() {
			if strings.HasSuffix(g.Get("name").Str(), tok) {
				n++
			}
		}
		return n
	}
	eq(t, "gyms named with the token", count(), 2)
	status(t, createGym(t, a.Token, map[string]any{"name": "Golds Gym Aundh " + tok, "city": city}), 409)
	eq(t, "a rejected create inserts nothing", count(), 2)

	// Without a city the guard compares against every city.
	status(t, createGym(t, a.Token, map[string]any{"name": "Golds Gym Aundh " + tok}), 409)
	eq(t, "still nothing inserted", count(), 2)

	forced := created(t, createGym(t, a.Token, map[string]any{"name": "Golds Gym Aundh " + tok, "city": city, "force": true}))
	isTrue(t, "forced gym is a new row", forced.Get("id").Str() != golds.Get("id").Str())
	eq(t, "gyms named with the token after force", count(), 3)
	// force: false behaves like absent.
	status(t, createGym(t, a.Token, map[string]any{"name": "Golds Gym Aundh " + tok, "city": city, "force": false}), 409)
}

func TestNewWorkoutsStartAtTheGymOfTheLastWorkout(t *testing.T) {
	t.Parallel()
	a := newUser(t, "gymrat")
	city := "Cty" + randLetters(8)
	alpha := created(t, createGym(t, a.Token, map[string]any{"name": "Alpha Gym " + randLetters(10), "city": city}))
	beta := created(t, createGym(t, a.Token, map[string]any{"name": "Beta Fitness " + randLetters(10), "city": city}))
	alphaName, betaName := alpha.Get("name").Str(), beta.Get("name").Str()

	// Nothing logged yet: no gym. Creating a gym does not pick one.
	first := created(t, post(t, "/workouts", a.Token, map[string]any{}))
	null(t, "gym", first.Get("gym"))
	null(t, "gym_id", first.Get("gym_id"))

	// An explicit gym wins and becomes the next default.
	w := created(t, post(t, "/workouts", a.Token, map[string]any{"gym_id": alpha.Get("id").Str(), "started_at": "2026-01-01T09:00:00Z"}))
	str(t, "gym name", w.Get("gym").Get("name"), alphaName)
	str(t, "gym_id", w.Get("gym_id"), alpha.Get("id").Str())
	next := created(t, post(t, "/workouts", a.Token, map[string]any{"started_at": "2026-01-02T09:00:00Z"}))
	str(t, "carried over from the last workout", next.Get("gym").Get("name"), alphaName)
	str(t, "carried gym_id", next.Get("gym_id"), alpha.Get("id").Str())

	// The most recent workout decides, and an explicit choice still overrides.
	created(t, post(t, "/workouts", a.Token, map[string]any{"gym_id": beta.Get("id").Str(), "started_at": "2026-01-03T09:00:00Z"}))
	after := created(t, post(t, "/workouts", a.Token, map[string]any{"started_at": "2026-01-04T09:00:00Z"}))
	str(t, "most recent wins", after.Get("gym").Get("name"), betaName)
	chosen := created(t, post(t, "/workouts", a.Token, map[string]any{"gym_id": alpha.Get("id").Str(), "started_at": "2026-01-05T09:00:00Z"}))
	str(t, "explicit overrides", chosen.Get("gym").Get("name"), alphaName)

	// A workout logged with a null gym inherits the last one, so it is not bare.
	bare := created(t, post(t, "/workouts", a.Token, map[string]any{"gym_id": nil, "started_at": "2026-01-05T12:00:00Z"}))
	str(t, "inherits", bare.Get("gym").Get("name"), alphaName)

	// ...and a genuinely gym-less workout in between does not reset the default.
	requireDB(t)
	sql(t, "update workouts set gym_id = null where id = '"+bare.Get("id").Str()+"'")
	afterBare := created(t, post(t, "/workouts", a.Token, map[string]any{"started_at": "2026-01-05T13:00:00Z"}))
	str(t, "skips the gym-less workout", afterBare.Get("gym").Get("name"), alphaName)

	// The gym on a workout carries what a Directions button needs.
	cult := ok(t, get(t, "/gyms?q=kalyani", a.Token)).JSON
	w = created(t, post(t, "/workouts", a.Token, map[string]any{"gym_id": cult.Idx(0).Get("id").Str(), "started_at": "2026-01-06T09:00:00Z"}))
	gym := w.Get("gym")
	eq(t, "gym ref field count", gym.Len(), 7)
	str(t, "id", gym.Get("id"), cult.Idx(0).Get("id").Str())
	str(t, "name", gym.Get("name"), "Cult Kalyani Nagar")
	str(t, "area", gym.Get("area"), "Kalyani Nagar")
	str(t, "city", gym.Get("city"), "Pune")
	str(t, "maps_url", gym.Get("maps_url"), "https://maps.app.goo.gl/9nog9QtzoJD9qtGQA")
	near(t, "lat", gym.Get("lat").Float(), 18.5492421, 1e-9)
	near(t, "lng", gym.Get("lng").Float(), 73.9060673, 1e-9)

	// Other people's workouts never leak into my default.
	b := newUser(t, "newbie")
	theirs := created(t, post(t, "/workouts", b.Token, map[string]any{}))
	null(t, "gym", theirs.Get("gym"))
}

func TestWorkoutsCarryExerciseKindAndGymName(t *testing.T) {
	t.Parallel()
	u := newUser(t, "gymrat")
	plank := exerciseBySlug(t, u.Token, "plank")
	city := "Cty" + randLetters(8)
	gymName := "Golds Gym Aundh " + randLetters(10)

	gym := created(t, post(t, "/gyms", u.Token, map[string]any{"name": gymName, "city": city}))
	w := created(t, post(t, "/workouts", u.Token, map[string]any{"gym_id": gym.Get("id").Str()}))
	str(t, "gym.name", w.Get("gym").Get("name"), gymName)
	str(t, "gym.city", w.Get("gym").Get("city"), city)
	null(t, "gym.area", w.Get("gym").Get("area"))
	null(t, "gym.lat", w.Get("gym").Get("lat"))
	null(t, "gym.lng", w.Get("gym").Get("lng"))
	null(t, "gym.maps_url", w.Get("gym").Get("maps_url"))
	wid := w.Get("id").Str()

	we := created(t, post(t, "/workouts/"+wid+"/exercises", u.Token, map[string]any{"exercise_id": plank}))
	str(t, "kind", we.Get("kind"), "duration")

	detail := ok(t, get(t, "/workouts/"+wid, u.Token))
	str(t, "detail kind", detail.Get("exercises").Idx(0).Get("kind"), "duration")
	str(t, "detail gym id", detail.Get("gym").Get("id"), gym.Get("id").Str())
	list := ok(t, get(t, "/workouts", u.Token))
	str(t, "list gym name", list.Idx(0).Get("gym").Get("name"), gymName)
	str(t, "list gym_id", list.Idx(0).Get("gym_id"), gym.Get("id").Str())

	// A workout without a gym reports null rather than omitting the field (a new user has no last gym).
	fresh := newUser(t, "fresh")
	bare := created(t, post(t, "/workouts", fresh.Token, map[string]any{}))
	null(t, "gym", bare.Get("gym"))
	null(t, "gym_id", bare.Get("gym_id"))
	fl := ok(t, get(t, "/workouts", fresh.Token))
	null(t, "list gym", fl.Idx(0).Get("gym"))
	null(t, "list gym_id", fl.Idx(0).Get("gym_id"))

	// History keeps the gym name even after the gym is soft-deleted.
	requireDB(t)
	sql(t, "update gyms set deleted_at = now() where id = '"+gym.Get("id").Str()+"'")
	detail = ok(t, get(t, "/workouts/"+wid, u.Token))
	str(t, "detail gym name after soft delete", detail.Get("gym").Get("name"), gymName)
	list = ok(t, get(t, "/workouts", u.Token))
	str(t, "list gym name after soft delete", list.Idx(0).Get("gym").Get("name"), gymName)
	// A deleted gym is no longer found, nor inherited by the next workout.
	isTrue(t, "a soft-deleted gym is not searchable",
		!contains(idsOf(ok(t, get(t, "/gyms?q="+url.QueryEscape(gymName), u.Token)).JSON), gym.Get("id").Str()))
	next := created(t, post(t, "/workouts", u.Token, map[string]any{}))
	null(t, "a soft-deleted gym is not carried over", next.Get("gym"))
}

func TestSeededGymIsVerifiedGlobalAndFoundByAreaCityOrName(t *testing.T) {
	t.Parallel()
	u := newUser(t, "newcomer")
	for _, q := range []string{"cult", "kalyani", "pune", "cult%20pune", "nagar%20cult", "Cult%20Gym%20Kalyani"} {
		found := ok(t, get(t, "/gyms?q="+q, u.Token)).JSON
		str(t, "query "+q+": "+found.String(), found.Idx(0).Get("name"), "Cult Kalyani Nagar")
	}
	emptyArray(t, "mumbai", ok(t, get(t, "/gyms?q=mumbai", u.Token)).JSON)

	found := ok(t, get(t, "/gyms?q=kalyani", u.Token)).JSON
	cult := found.Idx(0)
	assertGymShape(t, cult)
	eq(t, "verified", cult.Get("verified").Bool(), true)
	str(t, "area", cult.Get("area"), "Kalyani Nagar")
	str(t, "city", cult.Get("city"), "Pune")
	str(t, "chain", cult.Get("chain"), "Cult")
	null(t, "address", cult.Get("address"))
	str(t, "maps_url", cult.Get("maps_url"), "https://maps.app.goo.gl/9nog9QtzoJD9qtGQA")
	near(t, "lat", cult.Get("lat").Float(), 18.5492421, 1e-9)
	near(t, "lng", cult.Get("lng").Float(), 73.9060673, 1e-9)
	isTrue(t, "lat and lng are JSON numbers", cult.Get("lat").IsNumber() && cult.Get("lng").IsNumber())
}

func TestGymAreaAndMapsLinkAreStoredAndTheLinkIsRestricted(t *testing.T) {
	t.Parallel()
	u := newUser(t, "gymrat")
	tok := randLetters(10)

	gym := created(t, createGym(t, u.Token, map[string]any{
		"name": "Iron Works " + tok, "area": " Ar" + tok + " ", "city": "Cty" + randLetters(8),
		"maps_url": "https://www.google.com/maps/place/Iron+Works",
	}))
	str(t, "area trimmed", gym.Get("area"), "Ar"+tok)
	str(t, "maps_url", gym.Get("maps_url"), "https://www.google.com/maps/place/Iron+Works")
	eq(t, "verified", gym.Get("verified").Bool(), false)
	null(t, "lat", gym.Get("lat"))
	found := ok(t, get(t, "/gyms?q=ar"+tok, u.Token)).JSON
	str(t, "found by area", found.Idx(0).Get("area"), "Ar"+tok)
	str(t, "found id", found.Idx(0).Get("id"), gym.Get("id").Str())

	const badMsg = "maps_url must be a link to Google Maps, Apple Maps or OpenStreetMap"
	for i, bad := range []string{
		"http://maps.app.goo.gl/x",
		"https://evil.example/maps",
		"https://maps.app.goo.gl.evil.example/x",
		"https://www.google.com/search?q=x",
		"https://goo.gl/other",
		"https://user@maps.app.goo.gl/x",
		"https://maps.app.goo.gl/has space",
		"javascript:alert(1)",
		"https://maps.app.goo.gl/tab\there",
		"HTTPS://maps.app.goo.gl/x",
	} {
		_ = i
		errBody(t, createGym(t, u.Token, map[string]any{"name": "Bad Link " + randLetters(10), "maps_url": bad}), 400, badMsg, "")
	}
	for _, c := range []struct{ name, ok string }{
		{"Alpha Barbell", "https://maps.app.goo.gl/abc"},
		{"Zenith Fitness", "https://goo.gl/maps/abc"},
		{"Quartz Strength", "https://maps.apple.com/?q=x"},
		{"Nimbus Club", "https://www.openstreetmap.org/node/1"},
		{"Maple Lifting", "https://maps.google.com/?q=x"},
		{"Google Dot Com", "https://google.com/maps/place/x"},
		{"Host Only", "https://maps.app.goo.gl"},
	} {
		g := created(t, createGym(t, u.Token, map[string]any{"name": c.name + " " + randLetters(10), "maps_url": c.ok, "force": true}))
		str(t, c.ok, g.Get("maps_url"), c.ok)
	}
	// A blank link is just absent.
	g := created(t, createGym(t, u.Token, map[string]any{"name": "Blank Link Club " + randLetters(10), "maps_url": "  ", "force": true}))
	null(t, "maps_url", g.Get("maps_url"))

	// The column caps a link at 500 characters.
	long := "https://maps.app.goo.gl/" + strings.Repeat("a", 500)
	errBody(t, createGym(t, u.Token, map[string]any{"name": "Long Link " + randLetters(10), "maps_url": long, "force": true}), 400,
		"invalid value (gyms_maps_url_check)", "")
}

func TestGymDuplicateGuardListsAtMostTenSimilarGyms(t *testing.T) {
	t.Parallel()
	a := newUser(t, "gymrat")
	tok := randLetters(10)
	city := "Cty" + randLetters(8)
	for i := 0; i < 12; i++ {
		created(t, createGym(t, a.Token, map[string]any{"name": "Golds Gym Aundh " + tok, "city": city, "force": true}))
	}
	r := createGym(t, a.Token, map[string]any{"name": "Golds Gym Aundh " + tok, "city": city})
	status(t, r, 409)
	arrayLen(t, "similar is capped", r.Get("similar"), 10)
}
