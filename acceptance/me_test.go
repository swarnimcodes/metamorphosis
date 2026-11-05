package acceptance

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestPatchMeUpdatesPreferencesAndValidates(t *testing.T) {
	t.Parallel()
	u := newUser(t, "prefs")

	me := ok(t, patch(t, "/me", u.Token, map[string]any{
		"unit_system": "imperial", "timezone": "Asia/Kolkata", "display_name": "  Prefs P  ",
	}))
	eq(t, "field count", me.JSON.Len(), 7)
	str(t, "unit_system", me.Get("unit_system"), "imperial")
	str(t, "timezone", me.Get("timezone"), "Asia/Kolkata")
	str(t, "display_name", me.Get("display_name"), "Prefs P")
	str(t, "id", me.Get("id"), u.ID)
	str(t, "username", me.Get("username"), u.Name)
	str(t, "email", me.Get("email"), u.Name+"@dev.local")
	null(t, "avatar_url", me.Get("avatar_url"))

	// Omitted fields stay as they were, and GET /me reflects the change.
	me = ok(t, patch(t, "/me", u.Token, map[string]any{"display_name": "Prefs"}))
	str(t, "unit_system kept", me.Get("unit_system"), "imperial")
	me = ok(t, get(t, "/me", u.Token))
	str(t, "display_name", me.Get("display_name"), "Prefs")
	str(t, "timezone", me.Get("timezone"), "Asia/Kolkata")

	// An empty patch changes nothing.
	me = ok(t, patch(t, "/me", u.Token, map[string]any{}))
	str(t, "display_name", me.Get("display_name"), "Prefs")

	// 80 characters is the longest name; 81 is refused, counted in characters, not bytes.
	ok(t, patch(t, "/me", u.Token, map[string]any{"display_name": strings.Repeat("é", 80)}))
	errBody(t, patch(t, "/me", u.Token, map[string]any{"display_name": strings.Repeat("a", 81)}), 400,
		"display_name must be 1-80 characters", "")

	errBody(t, patch(t, "/me", u.Token, map[string]any{"timezone": "Mars/Olympus"}), 400, "unknown timezone", "")
	errBody(t, patch(t, "/me", u.Token, map[string]any{"display_name": "   "}), 400, "display_name must be 1-80 characters", "")
	errBody(t, patch(t, "/me", u.Token, map[string]any{"unit_system": "furlongs"}), 400, "", "")
	errBody(t, patch(t, "/me", u.Token, RawBody("{nope")), 400, "", "")
	me = ok(t, get(t, "/me", u.Token))
	str(t, "rejected requests must not change anything", me.Get("unit_system"), "imperial")
	str(t, "timezone unchanged", me.Get("timezone"), "Asia/Kolkata")

	unauthorized(t, patch(t, "/me", "", map[string]any{}))
}

func TestDeletingAnAccountRemovesEverythingItOwnsAndNothingElse(t *testing.T) {
	t.Parallel()
	requireDB(t)
	leaverName := "leaver_" + randHex(8)
	leaver, _ := loginAs(t, leaverName)
	stayer := newUser(t, "stayer")

	bench := exerciseBySlug(t, leaver.Token, "bench-press")
	// Both accounts own a workout with a set, a weigh-in, and a gym they created.
	fill := func(u User, gymName string) Val {
		gym := created(t, post(t, "/gyms", u.Token, map[string]any{"name": gymName, "city": "Del" + randLetters(8)})).JSON
		w := created(t, post(t, "/workouts", u.Token, map[string]any{"gym_id": gym.Get("id").Str()}))
		we := created(t, post(t, "/workouts/"+w.Get("id").Str()+"/exercises", u.Token, map[string]any{"exercise_id": bench}))
		created(t, post(t, "/workout-exercises/"+we.Get("id").Str()+"/sets", u.Token, map[string]any{"weight_kg": 60, "reps": 5}))
		ok(t, put(t, "/body-weights/2026-01-05", u.Token, map[string]any{"weight_kg": 80}))
		return gym
	}
	leaverGym := fill(leaver, "Leaver Gym "+randLetters(8))
	fill(stayer, "Stayer Gym "+randLetters(8))

	// Every table with a user_id column, found from the schema so a table added later is covered too.
	tables := strings.Fields(sql(t, "select distinct table_name::text from information_schema.columns "+
		"where table_schema = 'public' and column_name = 'user_id' order by 1"))
	sort.Strings(tables)
	rowsOwnedBy := func(id string) map[string]string {
		out := map[string]string{}
		for _, tbl := range tables {
			out[tbl] = sql(t, fmt.Sprintf("select count(*) from %s where user_id = '%s'", tbl, id))
		}
		out["users"] = sql(t, fmt.Sprintf("select count(*) from users where id = '%s'", id))
		return out
	}
	same := func(a, b map[string]string) bool {
		if len(a) != len(b) {
			return false
		}
		for k, v := range a {
			if b[k] != v {
				return false
			}
		}
		return true
	}
	beforeLeaver := rowsOwnedBy(leaver.ID)
	beforeStayer := rowsOwnedBy(stayer.ID)
	for _, tbl := range []string{"workouts", "body_weights", "refresh_tokens"} {
		if beforeLeaver[tbl] == "0" {
			t.Fatalf("expected the leaver to own %s rows before deleting: %v", tbl, beforeLeaver)
		}
	}

	// The confirmation has to match the username (case and surrounding space do not matter), and a wrong
	// one deletes nothing.
	for _, wrong := range []string{"", stayer.Name, leaverName + "2", "someone else"} {
		r := req(t, "DELETE", "/me", leaver.Token, map[string]any{"confirm_username": wrong})
		errBody(t, r, 400, "the username does not match this account", "username_mismatch")
	}
	isTrue(t, "a wrong confirmation changed something", same(rowsOwnedBy(leaver.ID), beforeLeaver))
	errBody(t, req(t, "DELETE", "/me", leaver.Token, map[string]any{}), 400, "", "")
	unauthorized(t, req(t, "DELETE", "/me", "", map[string]any{"confirm_username": leaverName}))

	noContent(t, req(t, "DELETE", "/me", leaver.Token, map[string]any{"confirm_username": "  " + strings.ToUpper(leaverName) + " "}))

	// Everything the account owned is gone, in every table that has an owner column.
	for tbl, n := range rowsOwnedBy(leaver.ID) {
		if n != "0" {
			t.Fatalf("%s still has %s rows for the deleted account", tbl, n)
		}
	}
	// Nothing of anyone else was touched.
	isTrue(t, "another user's rows changed", same(rowsOwnedBy(stayer.ID), beforeStayer))

	// The shared gym they created stays in the directory, no longer tied to them.
	eq(t, "gym still exists", sql(t, "select count(*) from gyms where id = '"+leaverGym.Get("id").Str()+"'"), "1")
	eq(t, "gym created_by", sql(t, "select coalesce(created_by::text, 'NULL') from gyms where id = '"+leaverGym.Get("id").Str()+"'"), "NULL")

	// The old tokens stop working at once, not when they expire.
	unauthorized(t, get(t, "/me", leaver.Token))
	unauthorized(t, get(t, "/workouts", leaver.Token))
	unauthorized(t, refresh(t, leaver.RefreshToken))

	// The username is free again, and signing in creates a fresh, empty account.
	again, _ := loginAs(t, leaverName)
	isTrue(t, "a fresh account id", again.ID != leaver.ID)
	emptyArray(t, "workouts", ok(t, get(t, "/workouts", again.Token)).JSON)
}
