package acceptance

import (
	"testing"
)

// roleUser signs in a fresh account and gives it a role (a database update, as an operator would).
func roleUser(t *testing.T, prefix, role string) User {
	t.Helper()
	requireDB(t)
	u := newUser(t, prefix)
	sql(t, "update users set role = '"+role+"' where id = '"+u.ID+"'")
	return u
}

func newGym(t *testing.T, u User, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"name": "Role Gym " + randLetters(10), "city": "Cty" + randLetters(8)}
	for k, v := range extra {
		body[k] = v
	}
	return created(t, createGym(t, u.Token, body)).Get("id").Str()
}

func TestRoleIsOnMeAndOnlyAnOperatorChangesIt(t *testing.T) {
	t.Parallel()
	mod := roleUser(t, "rolemod", "moderator")
	str(t, "a moderator's role", ok(t, get(t, "/me", mod.Token)).Get("role"), "moderator")
	// The API offers no way to grant a role, not even to yourself.
	r := ok(t, patch(t, "/me", mod.Token, map[string]any{"role": "admin", "display_name": "Mod"}))
	str(t, "still a moderator", r.Get("role"), "moderator")
	admin := roleUser(t, "roleadm", "admin")
	str(t, "an admin's role", ok(t, get(t, "/me", admin.Token)).Get("role"), "admin")
}

func TestVerifiedGymsAreEditedByStaffOnly(t *testing.T) {
	t.Parallel()
	user, mod := newUser(t, "gymuser"), roleUser(t, "gymmod", "moderator")
	id := newGym(t, user, nil)

	errBody(t, put(t, "/gyms/"+id+"/verified", user.Token, map[string]any{"verified": true}), 403, "only moderators can verify gyms", "forbidden")
	v := ok(t, put(t, "/gyms/"+id+"/verified", mod.Token, map[string]any{"verified": true}))
	eq(t, "verified", v.Get("verified").Bool(), true)
	// Verifying again changes nothing and adds nothing to the history.
	ok(t, put(t, "/gyms/"+id+"/verified", mod.Token, map[string]any{"verified": true}))

	d := ok(t, get(t, "/gyms/"+id, user.Token))
	eq(t, "a user may not edit it now", d.Get("editable").Bool(), false)
	eq(t, "can.edit for a user", d.Get("can").Get("edit").Bool(), false)
	eq(t, "can.verify for a user", d.Get("can").Get("verify").Bool(), false)
	errBody(t, patch(t, "/gyms/"+id, user.Token, map[string]any{"city": "Nope"}), 403, "only moderators can edit verified gyms", "gym_locked")

	m := ok(t, get(t, "/gyms/"+id, mod.Token))
	eq(t, "a moderator may edit it", m.Get("can").Get("edit").Bool(), true)
	eq(t, "a moderator may verify", m.Get("can").Get("verify").Bool(), true)
	eq(t, "a moderator may not delete", m.Get("can").Get("delete").Bool(), false)
	ok(t, patch(t, "/gyms/"+id, mod.Token, map[string]any{"area": "Fixed by mod"}))

	u := ok(t, put(t, "/gyms/"+id+"/verified", mod.Token, map[string]any{"verified": false}))
	eq(t, "unverified", u.Get("verified").Bool(), false)
	ok(t, patch(t, "/gyms/"+id, user.Token, map[string]any{"city": "Open again"}))

	h := ok(t, get(t, "/gyms/"+id+"/history", mod.Token)).JSON
	sameStrings(t, "history kinds, newest first", strs(h, "kind"), []string{"edit", "unverify", "edit", "verify"})
}

func TestHistoryNamesEditorsOnlyToStaffAndStaffRevert(t *testing.T) {
	t.Parallel()
	user, mod := newUser(t, "histuser"), roleUser(t, "histmod", "moderator")
	id := newGym(t, user, map[string]any{"chain": "Old Chain"})
	ok(t, patch(t, "/gyms/"+id, user.Token, map[string]any{"chain": "Vandal Chain", "area": "Vandal Area"}))

	forUser := ok(t, get(t, "/gyms/"+id+"/history", user.Token)).JSON.Idx(0)
	null(t, "a user is not told who", forUser.Get("by"))
	eq(t, "a user cannot revert", forUser.Get("revertible").Bool(), false)
	forMod := ok(t, get(t, "/gyms/"+id+"/history", mod.Token)).JSON.Idx(0)
	isTrue(t, "a moderator is told who", forMod.Get("by").IsString() && forMod.Get("by").Str() != "")
	eq(t, "a moderator can revert", forMod.Get("revertible").Bool(), true)
	editID := forMod.Get("id").Str()

	errBody(t, post(t, "/gyms/"+id+"/history/"+editID+"/revert", user.Token, map[string]any{}), 403, "only moderators can revert edits", "forbidden")
	r := ok(t, post(t, "/gyms/"+id+"/history/"+editID+"/revert", mod.Token, map[string]any{}))
	str(t, "chain restored", r.Get("chain"), "Old Chain")
	null(t, "area restored", r.Get("area"))

	h := ok(t, get(t, "/gyms/"+id+"/history", mod.Token)).JSON
	sameStrings(t, "kinds", strs(h, "kind"), []string{"revert", "edit"})
	eq(t, "the undone edit is marked", h.Idx(1).Get("reverted").Bool(), true)
	eq(t, "and can't be reverted again", h.Idx(1).Get("revertible").Bool(), false)
	errBody(t, post(t, "/gyms/"+id+"/history/"+editID+"/revert", mod.Token, map[string]any{}), 409, "this edit was already reverted", "already_reverted")
	errBody(t, post(t, "/gyms/"+id+"/history/"+h.Idx(0).Get("id").Str()+"/revert", mod.Token, map[string]any{}), 400, "only edits can be reverted", "")

	// A later edit of the same field blocks reverting the earlier one.
	ok(t, patch(t, "/gyms/"+id, user.Token, map[string]any{"city": "First"}))
	first := ok(t, get(t, "/gyms/"+id+"/history", mod.Token)).JSON.Idx(0).Get("id").Str()
	ok(t, patch(t, "/gyms/"+id, user.Token, map[string]any{"city": "Second"}))
	errBody(t, post(t, "/gyms/"+id+"/history/"+first+"/revert", mod.Token, map[string]any{}), 409, "a later edit changed this again; revert that one first", "changed_since")
	status(t, post(t, "/gyms/"+id+"/history/00000000-0000-0000-0000-000000000000/revert", mod.Token, map[string]any{}), 404)
}

func TestOnlyAdminsDeleteAndWorkoutsKeepTheGym(t *testing.T) {
	t.Parallel()
	user, mod, admin := newUser(t, "deluser"), roleUser(t, "delmod", "moderator"), roleUser(t, "deladm", "admin")
	id := newGym(t, user, nil)
	w := created(t, post(t, "/workouts", user.Token, map[string]any{"gym_id": id}))

	status(t, del(t, "/gyms/"+id, user.Token), 403)
	errBody(t, del(t, "/gyms/"+id, mod.Token), 403, "only admins can delete gyms", "forbidden")
	eq(t, "an admin may delete", ok(t, get(t, "/gyms/"+id, admin.Token)).Get("can").Get("delete").Bool(), true)
	status(t, del(t, "/gyms/"+id, admin.Token), 204)

	status(t, get(t, "/gyms/"+id, admin.Token), 404)
	status(t, del(t, "/gyms/"+id, admin.Token), 404)
	status(t, patch(t, "/gyms/"+id, admin.Token, map[string]any{"city": "X"}), 404)
	for _, g := range ok(t, get(t, "/gyms?q=Role+Gym", admin.Token)).JSON.Arr() {
		isTrue(t, "a deleted gym is not searchable", g.Get("id").Str() != id)
	}
	// The workout logged there keeps its gym.
	str(t, "the workout still names the gym", ok(t, get(t, "/workouts/"+w.Get("id").Str(), user.Token)).Get("gym").Get("id"), id)
}

func TestMergeMovesWorkoutsAndHidesTheDuplicate(t *testing.T) {
	t.Parallel()
	user, mod := newUser(t, "mrguser"), roleUser(t, "mrgmod", "moderator")
	dup, keep := newGym(t, user, nil), newGym(t, user, nil)
	w := created(t, post(t, "/workouts", user.Token, map[string]any{"gym_id": dup}))

	errBody(t, post(t, "/gyms/"+dup+"/merge", user.Token, map[string]any{"into": keep}), 403, "only moderators can merge gyms", "forbidden")
	errBody(t, post(t, "/gyms/"+dup+"/merge", mod.Token, map[string]any{"into": dup}), 400, "a gym can't be merged into itself", "")
	status(t, post(t, "/gyms/"+dup+"/merge", mod.Token, map[string]any{"into": "00000000-0000-4000-8000-" + randHex(12)}), 404)
	errBody(t, post(t, "/gyms/"+dup+"/merge", mod.Token, map[string]any{}), 400, "missing field `into`", "")

	m := ok(t, post(t, "/gyms/"+dup+"/merge", mod.Token, map[string]any{"into": keep}))
	str(t, "the gym that stays is returned", m.Get("id"), keep)
	str(t, "the workout moved", ok(t, get(t, "/workouts/"+w.Get("id").Str(), user.Token)).Get("gym").Get("id"), keep)
	status(t, get(t, "/gyms/"+dup, user.Token), 404)
	num(t, "the visit counts at the gym that stays", ok(t, get(t, "/gyms/"+keep, user.Token)).Get("visits"), 1)
	h := ok(t, get(t, "/gyms/"+keep+"/history", mod.Token)).JSON.Idx(0)
	str(t, "recorded as a merge", h.Get("kind"), "merge")
	status(t, post(t, "/gyms/"+dup+"/merge", mod.Token, map[string]any{"into": keep}), 404)
}

func TestOrdinaryUsersMayEditThirtyTimesAnHour(t *testing.T) {
	t.Parallel()
	user, mod := newUser(t, "ratey"), roleUser(t, "ratemod", "moderator")
	id := newGym(t, user, nil)
	for i := 0; i < 30; i++ {
		ok(t, patch(t, "/gyms/"+id, user.Token, map[string]any{"area": "Area " + randLetters(8)}))
	}
	errBody(t, patch(t, "/gyms/"+id, user.Token, map[string]any{"area": "One too many"}), 429, "too many gym edits, try again later", "rate_limited")
	// Moderators are not limited, and the limit is per user.
	for i := 0; i < 31; i++ {
		ok(t, patch(t, "/gyms/"+id, mod.Token, map[string]any{"area": "Mod " + randLetters(8)}))
	}
	ok(t, patch(t, "/gyms/"+id, newUser(t, "ratey2").Token, map[string]any{"area": "Someone else"}))
}
