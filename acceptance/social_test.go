package acceptance

import (
	"testing"
)

// befriend makes two accounts friends through the API.
func befriend(t *testing.T, a, b User) {
	t.Helper()
	eq(t, "request sent", ok(t, post(t, "/people/"+b.ID+"/friend-request", a.Token, map[string]any{})).Get("relationship").Str(), "request_sent")
	eq(t, "accepted", ok(t, post(t, "/people/"+a.ID+"/accept", b.Token, map[string]any{})).Get("relationship").Str(), "friends")
}

func ids(v Val) []string { return strs(v, "id") }

func kinds(v Val) []string { return strs(v, "kind") }

func TestSearchPeople(t *testing.T) {
	t.Parallel()
	a, b := newUser(t, "findme"), newUser(t, "someone")
	short := get(t, "/people?q=ab", a.Token)
	errBody(t, short, 400, "type at least 3 characters to search", "")
	errBody(t, get(t, "/people", a.Token), 400, "q is required", "")
	unauthorized(t, get(t, "/people?q=abc", ""))

	// Find by part of the username, in any case; never yourself.
	part := b.Name[len(b.Name)-6:]
	found := ok(t, get(t, "/people?q="+part, a.Token)).JSON
	isTrue(t, "found by part of the username", contains(ids(found), b.ID))
	upper := ok(t, get(t, "/people?q="+part+"%20", a.Token)).JSON
	isTrue(t, "whitespace and case don't matter", contains(ids(upper), b.ID))
	self := ok(t, get(t, "/people?q="+a.Name, a.Token)).JSON
	isTrue(t, "never the caller", !contains(ids(self), a.ID))

	// A stranger shows the basics only.
	p := ok(t, get(t, "/people?q="+b.Name, a.Token)).JSON.Idx(0)
	eq(t, "person field count", p.Len(), 5)
	str(t, "username", p.Get("username"), b.Name)
	null(t, "no photo for a stranger", p.Get("avatar_url"))
	str(t, "relationship", p.Get("relationship"), "none")
}

func TestProfileShowsLittleToStrangers(t *testing.T) {
	t.Parallel()
	a, b := newUser(t, "profa"), newUser(t, "profb")
	p := ok(t, get(t, "/people/"+b.ID, a.Token))
	eq(t, "profile field count", p.JSON.Len(), 8)
	str(t, "username", p.Get("username"), b.Name)
	str(t, "relationship", p.Get("relationship"), "none")
	null(t, "avatar", p.Get("avatar_url"))
	null(t, "friends_since", p.Get("friends_since"))
	num(t, "no finished workouts", p.Get("workouts"), 0)
	isTrue(t, "member since", p.Get("member_since").IsString())
	str(t, "your own profile says self", ok(t, get(t, "/people/"+a.ID, a.Token)).Get("relationship"), "self")
	status(t, get(t, "/people/00000000-0000-4000-8000-"+randHex(12), a.Token), 404)
	unauthorized(t, get(t, "/people/"+b.ID, ""))
}

func TestFriendRequestFlowAndNotifications(t *testing.T) {
	t.Parallel()
	a, b := newUser(t, "fra"), newUser(t, "frb")
	errBody(t, post(t, "/people/"+a.ID+"/friend-request", a.Token, map[string]any{}), 400, "you can't add yourself", "")
	status(t, post(t, "/people/00000000-0000-4000-8000-"+randHex(12)+"/friend-request", a.Token, map[string]any{}), 404)

	str(t, "sent", ok(t, post(t, "/people/"+b.ID+"/friend-request", a.Token, map[string]any{})).Get("relationship"), "request_sent")
	str(t, "asking again changes nothing", ok(t, post(t, "/people/"+b.ID+"/friend-request", a.Token, map[string]any{})).Get("relationship"), "request_sent")

	reqs := ok(t, get(t, "/friends/requests", b.Token))
	sameStrings(t, "incoming for the addressee", ids(reqs.Get("incoming")), []string{a.ID})
	emptyArray(t, "nothing outgoing for them", reqs.Get("outgoing"))
	sameStrings(t, "outgoing for the asker", ids(ok(t, get(t, "/friends/requests", a.Token)).Get("outgoing")), []string{b.ID})
	str(t, "how they see each other", ok(t, get(t, "/people/"+a.ID, b.Token)).Get("relationship"), "request_received")
	str(t, "and the other way", ok(t, get(t, "/people/"+b.ID, a.Token)).Get("relationship"), "request_sent")

	n := ok(t, get(t, "/notifications", b.Token))
	num(t, "one unread", n.Get("unread"), 1)
	sameStrings(t, "a request notification", kinds(n.Get("items")), []string{"friend_request"})
	str(t, "from the asker", n.Get("items").Idx(0).Get("actor").Get("id"), a.ID)
	num(t, "the badge", ok(t, get(t, "/notifications/unread", b.Token)).Get("count"), 1)

	// Only the addressee can accept.
	status(t, post(t, "/people/"+b.ID+"/accept", a.Token, map[string]any{}), 404)
	str(t, "accepted", ok(t, post(t, "/people/"+a.ID+"/accept", b.Token, map[string]any{})).Get("relationship"), "friends")
	status(t, post(t, "/people/"+a.ID+"/accept", b.Token, map[string]any{}), 404)

	emptyArray(t, "the request is gone from their notifications", ok(t, get(t, "/notifications", b.Token)).Get("items"))
	sameStrings(t, "the asker is told", kinds(ok(t, get(t, "/notifications", a.Token)).Get("items")), []string{"friend_accepted"})
	sameStrings(t, "friends of a", ids(ok(t, get(t, "/friends", a.Token)).JSON), []string{b.ID})
	sameStrings(t, "friends of b", ids(ok(t, get(t, "/friends", b.Token)).JSON), []string{a.ID})
	p := ok(t, get(t, "/people/"+b.ID, a.Token))
	str(t, "friends", p.Get("relationship"), "friends")
	isTrue(t, "friends since", p.Get("friends_since").IsString())
	str(t, "asking a friend again says friends", ok(t, post(t, "/people/"+b.ID+"/friend-request", a.Token, map[string]any{})).Get("relationship"), "friends")

	// Marking read clears the badge.
	noContent(t, post(t, "/notifications/read", a.Token, map[string]any{}))
	num(t, "badge cleared", ok(t, get(t, "/notifications/unread", a.Token)).Get("count"), 0)
	eq(t, "still listed, now read", ok(t, get(t, "/notifications", a.Token)).Get("items").Idx(0).Get("read").Bool(), true)
}

func TestAskingSomeoneWhoAskedYouIsAYes(t *testing.T) {
	t.Parallel()
	a, b := newUser(t, "mutuala"), newUser(t, "mutualb")
	ok(t, post(t, "/people/"+b.ID+"/friend-request", a.Token, map[string]any{}))
	str(t, "friends at once", ok(t, post(t, "/people/"+a.ID+"/friend-request", b.Token, map[string]any{})).Get("relationship"), "friends")
	sameStrings(t, "a's friends", ids(ok(t, get(t, "/friends", a.Token)).JSON), []string{b.ID})
}

func TestCancelDeclineAndUnfriend(t *testing.T) {
	t.Parallel()
	a, b, c := newUser(t, "cda"), newUser(t, "cdb"), newUser(t, "cdc")
	// Cancel your own request: it leaves no notification behind.
	ok(t, post(t, "/people/"+b.ID+"/friend-request", a.Token, map[string]any{}))
	str(t, "cancelled", ok(t, del(t, "/people/"+b.ID+"/friendship", a.Token)).Get("relationship"), "none")
	emptyArray(t, "no stale notification", ok(t, get(t, "/notifications", b.Token)).Get("items"))
	status(t, del(t, "/people/"+b.ID+"/friendship", a.Token), 404)
	// Decline a request.
	ok(t, post(t, "/people/"+a.ID+"/friend-request", c.Token, map[string]any{}))
	ok(t, del(t, "/people/"+c.ID+"/friendship", a.Token))
	emptyArray(t, "declined", ok(t, get(t, "/friends/requests", a.Token)).Get("incoming"))
	// Unfriend.
	befriend(t, a, b)
	ok(t, del(t, "/people/"+a.ID+"/friendship", b.Token))
	emptyArray(t, "no longer friends", ok(t, get(t, "/friends", a.Token)).JSON)
	str(t, "strangers again", ok(t, get(t, "/people/"+b.ID, a.Token)).Get("relationship"), "none")
	// The "accepted" that came with the friendship goes with it.
	emptyArray(t, "no stale 'accepted' notification", ok(t, get(t, "/notifications", a.Token)).Get("items"))
}

func TestCancelNeverUnfriendsOnAStaleScreen(t *testing.T) {
	t.Parallel()
	a, b := newUser(t, "stalea"), newUser(t, "staleb")
	ok(t, post(t, "/people/"+b.ID+"/friend-request", a.Token, map[string]any{}))
	// b accepts while a's screen still says "Request sent"; a then presses Cancel.
	ok(t, post(t, "/people/"+a.ID+"/accept", b.Token, map[string]any{}))
	errBody(t, del(t, "/people/"+b.ID+"/friendship?pending_only=true", a.Token), 409, "you're already friends; refresh to see", "already_friends")
	str(t, "still friends", ok(t, get(t, "/people/"+b.ID, a.Token)).Get("relationship"), "friends")
	sameStrings(t, "and still told", kinds(ok(t, get(t, "/notifications", a.Token)).Get("items")), []string{"friend_accepted"})
	// Cancelling a request that is still pending works, and so does declining one.
	c := newUser(t, "stalec")
	ok(t, post(t, "/people/"+c.ID+"/friend-request", a.Token, map[string]any{}))
	str(t, "cancelled", ok(t, del(t, "/people/"+c.ID+"/friendship?pending_only=true", a.Token)).Get("relationship"), "none")
	status(t, del(t, "/people/"+c.ID+"/friendship?pending_only=true", a.Token), 404)
	// Unfriending on purpose (no pending_only) still works.
	ok(t, del(t, "/people/"+b.ID+"/friendship", a.Token))
	str(t, "unfriended", ok(t, get(t, "/people/"+b.ID, a.Token)).Get("relationship"), "none")
}

func TestFriendsSeeWorkoutsButNotNotesAndStrangersSeeNothing(t *testing.T) {
	t.Parallel()
	owner, friend, stranger := newUser(t, "own"), newUser(t, "frd"), newUser(t, "str")
	gymID := newGym(t, owner, nil)
	w := created(t, post(t, "/workouts", owner.Token, map[string]any{"title": "Push", "notes": "private note", "gym_id": gymID})).Get("id").Str()
	bench := exerciseBySlug(t, owner.Token, "bench-press")
	we := created(t, post(t, "/workouts/"+w+"/exercises", owner.Token, map[string]any{"exercise_id": bench, "notes": "private exercise note"}))
	created(t, post(t, "/workout-exercises/"+we.Get("id").Str()+"/sets", owner.Token, map[string]any{"weight_kg": 60, "reps": 5, "note": "private set note"}))
	ok(t, post(t, "/workouts/"+w+"/finish", owner.Token, map[string]any{}))

	// Nobody who is not a friend can tell the workout exists.
	status(t, get(t, "/people/"+owner.ID+"/workouts", stranger.Token), 404)
	status(t, get(t, "/people/"+owner.ID+"/workouts/"+w, stranger.Token), 404)
	status(t, put(t, "/people/"+owner.ID+"/workouts/"+w+"/like", stranger.Token, map[string]any{}), 404)
	status(t, get(t, "/people/"+owner.ID+"/workouts", friend.Token), 404)

	befriend(t, owner, friend)
	list := ok(t, get(t, "/people/"+owner.ID+"/workouts", friend.Token)).JSON
	sameStrings(t, "the friend's workouts", ids(list), []string{w})
	str(t, "with where", list.Idx(0).Get("gym").Get("id"), gymID)

	d := ok(t, get(t, "/people/"+owner.ID+"/workouts/"+w, friend.Token))
	eq(t, "detail field count", d.JSON.Len(), 3)
	str(t, "title", d.Get("workout").Get("title"), "Push")
	str(t, "the gym", d.Get("workout").Get("gym").Get("id"), gymID)
	null(t, "workout notes stay private", d.Get("workout").Get("notes"))
	ex := d.Get("workout").Get("exercises").Idx(0)
	null(t, "exercise notes stay private", ex.Get("notes"))
	num(t, "the set is visible", ex.Get("sets").Idx(0).Get("reps"), 5)
	null(t, "set notes stay private", ex.Get("sets").Idx(0).Get("note"))
	str(t, "the owner still sees their own notes", ok(t, get(t, "/workouts/"+w, owner.Token)).Get("notes"), "private note")
	// A workout that is not theirs is not found through them.
	status(t, get(t, "/people/"+friend.ID+"/workouts/"+w, owner.Token), 404)
	status(t, get(t, "/people/"+owner.ID+"/workouts/00000000-0000-4000-8000-"+randHex(12), friend.Token), 404)
	num(t, "finished workouts on the profile", ok(t, get(t, "/people/"+owner.ID, friend.Token)).Get("workouts"), 1)

	ok(t, del(t, "/people/"+owner.ID+"/friendship", friend.Token))
	status(t, get(t, "/people/"+owner.ID+"/workouts/"+w, friend.Token), 404)
}

func TestLikesNotifyTheOwner(t *testing.T) {
	t.Parallel()
	owner, friend, other := newUser(t, "lko"), newUser(t, "lkf"), newUser(t, "lkx")
	// The friends ask, so the owner's notifications hold only likes.
	befriend(t, friend, owner)
	befriend(t, other, owner)
	w := created(t, post(t, "/workouts", owner.Token, map[string]any{})).Get("id").Str()
	ok(t, post(t, "/workouts/"+w+"/finish", owner.Token, map[string]any{}))
	base := "/people/" + owner.ID + "/workouts/" + w

	l := ok(t, put(t, base+"/like", friend.Token, map[string]any{}))
	num(t, "one like", l.Get("likes"), 1)
	eq(t, "liked by me", l.Get("liked").Bool(), true)
	num(t, "liking twice is one like", ok(t, put(t, base+"/like", friend.Token, map[string]any{})).Get("likes"), 1)
	num(t, "a second person", ok(t, put(t, base+"/like", other.Token, map[string]any{})).Get("likes"), 2)
	d := ok(t, get(t, base, friend.Token))
	num(t, "likes on the workout", d.Get("likes"), 2)
	eq(t, "and whether I liked it", d.Get("liked").Bool(), true)

	n := ok(t, get(t, "/notifications", owner.Token))
	sameStrings(t, "the owner is told once per person", kinds(n.Get("items")), []string{"workout_liked", "workout_liked"})
	str(t, "about this workout", n.Get("items").Idx(0).Get("workout_id"), w)
	num(t, "unread", n.Get("unread"), 2)

	u := ok(t, del(t, base+"/like", friend.Token))
	num(t, "unliked", u.Get("likes"), 1)
	eq(t, "no longer liked by me", u.Get("liked").Bool(), false)
	num(t, "the notification went with it", ok(t, get(t, "/notifications", owner.Token)).Get("unread"), 1)

	errBody(t, put(t, "/people/"+owner.ID+"/workouts/"+w+"/like", owner.Token, map[string]any{}), 400, "you can't like your own workout", "")
}

func TestBlocking(t *testing.T) {
	t.Parallel()
	a, b := newUser(t, "bla"), newUser(t, "blb")
	befriend(t, a, b)
	w := created(t, post(t, "/workouts", b.Token, map[string]any{})).Get("id").Str()
	ok(t, put(t, "/people/"+b.ID+"/workouts/"+w+"/like", a.Token, map[string]any{}))

	errBody(t, put(t, "/people/"+a.ID+"/block", a.Token, map[string]any{}), 400, "you can't block yourself", "")
	noContent(t, put(t, "/people/"+b.ID+"/block", a.Token, map[string]any{}))
	noContent(t, put(t, "/people/"+b.ID+"/block", a.Token, map[string]any{}))

	// The friendship, the like and the notifications are gone.
	emptyArray(t, "a's friends", ok(t, get(t, "/friends", a.Token)).JSON)
	emptyArray(t, "b's friends", ok(t, get(t, "/friends", b.Token)).JSON)
	num(t, "the like went", ok(t, get(t, "/notifications/unread", b.Token)).Get("count"), 0)
	sameStrings(t, "a's blocked list", ids(ok(t, get(t, "/blocks", a.Token)).JSON), []string{b.ID})
	str(t, "a sees b as blocked", ok(t, get(t, "/people/"+b.ID, a.Token)).Get("relationship"), "blocked")

	// The blocked person cannot find or see the blocker at all.
	status(t, get(t, "/people/"+a.ID, b.Token), 404)
	isTrue(t, "not in b's search", !contains(ids(ok(t, get(t, "/people?q="+a.Name, b.Token)).JSON), a.ID))
	isTrue(t, "not in a's search either", !contains(ids(ok(t, get(t, "/people?q="+b.Name, a.Token)).JSON), b.ID))
	status(t, post(t, "/people/"+a.ID+"/friend-request", b.Token, map[string]any{}), 404)
	status(t, get(t, "/people/"+a.ID+"/workouts", b.Token), 404)
	errBody(t, post(t, "/people/"+b.ID+"/friend-request", a.Token, map[string]any{}), 409, "you blocked this person; unblock them first", "blocked")

	noContent(t, del(t, "/people/"+b.ID+"/block", a.Token))
	status(t, del(t, "/people/"+b.ID+"/block", a.Token), 404)
	str(t, "visible again, but strangers", ok(t, get(t, "/people/"+b.ID, a.Token)).Get("relationship"), "none")
	emptyArray(t, "the blocked list is empty", ok(t, get(t, "/blocks", a.Token)).JSON)
}

func TestReports(t *testing.T) {
	t.Parallel()
	requireDB(t)
	a, b, stranger := newUser(t, "repa"), newUser(t, "repb"), newUser(t, "reps")
	mod := roleUser(t, "repmod", "moderator")
	befriend(t, a, b)
	w := created(t, post(t, "/workouts", b.Token, map[string]any{})).Get("id").Str()

	errBody(t, post(t, "/reports", a.Token, map[string]any{"user_id": b.ID, "reason": "   "}), 400, "reason must be 1-500 characters", "")
	errBody(t, post(t, "/reports", a.Token, map[string]any{"user_id": a.ID, "reason": "me"}), 400, "you can't report yourself", "")
	status(t, post(t, "/reports", a.Token, map[string]any{"user_id": "00000000-0000-4000-8000-" + randHex(12), "reason": "x"}), 404)
	// A workout can only be reported by someone who can see it.
	status(t, post(t, "/reports", stranger.Token, map[string]any{"user_id": b.ID, "workout_id": w, "reason": "x"}), 404)

	noContent(t, post(t, "/reports", a.Token, map[string]any{"user_id": b.ID, "reason": "spam profile"}))
	noContent(t, post(t, "/reports", a.Token, map[string]any{"user_id": b.ID, "reason": "spam profile again"}))
	noContent(t, post(t, "/reports", a.Token, map[string]any{"user_id": b.ID, "workout_id": w, "reason": "offensive title"}))
	noContent(t, post(t, "/reports", stranger.Token, map[string]any{"user_id": b.ID, "reason": "weird"}))

	errBody(t, get(t, "/reports", a.Token), 403, "only moderators can work the reports", "forbidden")
	status(t, post(t, "/reports/"+randHex(8)+"/resolve", a.Token, map[string]any{}), 400)
	list := ok(t, get(t, "/reports", mod.Token)).JSON
	var mine []Val
	for _, r := range list.Arr() {
		if r.Get("target").Get("id").Str() == b.ID {
			mine = append(mine, r)
		}
	}
	eq(t, "three open reports about b (the repeat is not added)", len(mine), 3)
	var wr Val
	for _, r := range mine {
		if r.Get("workout_id").IsString() {
			wr = r
		}
	}
	str(t, "the workout report names the workout", wr.Get("workout_id"), w)
	str(t, "and its reporter", wr.Get("reporter").Get("id"), a.ID)
	str(t, "open", wr.Get("status"), "open")

	noContent(t, post(t, "/reports/"+wr.Get("id").Str()+"/resolve", mod.Token, map[string]any{}))
	status(t, post(t, "/reports/"+wr.Get("id").Str()+"/resolve", mod.Token, map[string]any{}), 404)
	isTrue(t, "gone from the open list", !contains(ids(ok(t, get(t, "/reports", mod.Token)).JSON), wr.Get("id").Str()))
	isTrue(t, "in the resolved list", contains(ids(ok(t, get(t, "/reports?status=resolved", mod.Token)).JSON), wr.Get("id").Str()))
	errBody(t, get(t, "/reports?status=bogus", mod.Token), 400, "", "")
	status(t, post(t, "/reports/"+wr.Get("id").Str()+"/resolve", a.Token, map[string]any{}), 403)
}

func TestSocialRateLimits(t *testing.T) {
	t.Parallel()
	a := newUser(t, "ratesoc")
	for i := 0; i < 20; i++ {
		ok(t, post(t, "/people/"+newUser(t, "target").ID+"/friend-request", a.Token, map[string]any{}))
	}
	errBody(t, post(t, "/people/"+newUser(t, "target").ID+"/friend-request", a.Token, map[string]any{}), 429, "too many friend requests, try again later", "rate_limited")

	r := newUser(t, "raterep")
	for i := 0; i < 10; i++ {
		noContent(t, post(t, "/reports", r.Token, map[string]any{"user_id": newUser(t, "reported").ID, "reason": "spam"}))
	}
	errBody(t, post(t, "/reports", r.Token, map[string]any{"user_id": newUser(t, "reported").ID, "reason": "spam"}), 429, "too many reports, try again later", "rate_limited")
}

func TestFriendsSeeEachOthersActivityCalendar(t *testing.T) {
	t.Parallel()
	owner, friend, stranger := newUser(t, "calo"), newUser(t, "calf"), newUser(t, "cals")
	w := created(t, post(t, "/workouts", owner.Token, map[string]any{})).Get("id").Str()
	bench := exerciseBySlug(t, owner.Token, "bench-press")
	we := created(t, post(t, "/workouts/"+w+"/exercises", owner.Token, map[string]any{"exercise_id": bench}))
	created(t, post(t, "/workout-exercises/"+we.Get("id").Str()+"/sets", owner.Token, map[string]any{"weight_kg": 60, "reps": 5}))
	ok(t, post(t, "/workouts/"+w+"/finish", owner.Token, map[string]any{}))

	// Not a friend: nothing, not even that the person exists.
	status(t, get(t, "/people/"+owner.ID+"/consistency", stranger.Token), 404)
	status(t, get(t, "/people/"+owner.ID+"/consistency", friend.Token), 404)

	befriend(t, owner, friend)
	c := ok(t, get(t, "/people/"+owner.ID+"/consistency", friend.Token))
	eq(t, "calendar field count", c.JSON.Len(), 6)
	num(t, "one workout in the totals", c.Get("totals").Get("workouts"), 1)
	num(t, "one active day", c.Get("totals").Get("active_days"), 1)
	isTrue(t, "a day with a workout", c.Get("days").Len() >= 1)
	// It is the owner's calendar, the same as they see for themselves.
	mine := ok(t, get(t, "/stats/consistency", owner.Token))
	str(t, "same window", c.Get("from"), mine.Get("from").Str())
	num(t, "same totals", c.Get("totals").Get("sets"), mine.Get("totals").Get("sets").Float())
	// The friend's own calendar is empty of that workout.
	num(t, "not the viewer's", ok(t, get(t, "/stats/consistency", friend.Token)).Get("totals").Get("workouts"), 0)

	ok(t, get(t, "/people/"+owner.ID+"/consistency?weeks=1", friend.Token))
	errBody(t, get(t, "/people/"+owner.ID+"/consistency?weeks=abc", friend.Token), 400, "", "")
	unauthorized(t, get(t, "/people/"+owner.ID+"/consistency", ""))

	ok(t, del(t, "/people/"+owner.ID+"/friendship", friend.Token))
	status(t, get(t, "/people/"+owner.ID+"/consistency", friend.Token), 404)
}
