package acceptance

import (
	"strings"
	"testing"
)

func TestHealthReportsOk(t *testing.T) {
	t.Parallel()
	r := ok(t, get(t, "/health", ""))
	eq(t, "body keys", r.JSON.Len(), 1)
	str(t, "status", r.Get("status"), "ok")
}

func TestDevLoginResponseShape(t *testing.T) {
	t.Parallel()
	name := "shape_" + randHex(8)
	_, r := loginAs(t, name)
	eq(t, "field count", r.JSON.Len(), 7)
	isTrue(t, "access_token is a non-empty string", r.Get("access_token").IsString() && r.Get("access_token").Str() != "")
	isTrue(t, "access_token is a JWT (three dot-separated parts)", strings.Count(r.Get("access_token").Str(), ".") == 2)
	str(t, "token_type", r.Get("token_type"), "Bearer")
	num(t, "expires_in", r.Get("expires_in"), 3600)
	// URL-safe base64 of 32 random bytes, no padding.
	rt := r.Get("refresh_token").Str()
	isTrue(t, "refresh_token is at least 43 chars", len(rt) >= 43)
	isTrue(t, "refresh_token is url-safe base64 without padding", !strings.ContainsAny(rt, "+/="))
	num(t, "refresh_expires_in", r.Get("refresh_expires_in"), 30*24*60*60)
	isTrue(t, "user_id is a uuid", uuidish(r.Get("user_id").Str()))
	str(t, "username", r.Get("username"), name)
}

func TestDevLoginLowercasesAndRejectsBadUsernames(t *testing.T) {
	t.Parallel()
	suffix := randHex(8)
	mixed := "Bob_" + strings.ToUpper(suffix)
	lower := strings.ToLower(mixed)
	first, r := loginAs(t, mixed)
	str(t, "login username", r.Get("username"), lower)
	me := ok(t, get(t, "/me", first.Token))
	str(t, "me.username", me.Get("username"), lower)

	// Surrounding whitespace is trimmed before the name is lowercased and stored.
	padded, _ := loginAs(t, "  "+mixed+" ")
	eq(t, "padded login resolves to the same user", padded.ID, first.ID)

	// Not matching ^[a-z0-9_]{3,30}$ after trimming and lowercasing.
	for _, bad := range []string{"a!", "ab", strings.Repeat("a", 31), "has space", "dash-ed", ""} {
		errBody(t, post(t, "/auth/dev-login", "", map[string]any{"username": bad}), 400, "invalid value (users_username_check)", "")
	}
	// Malformed or incomplete bodies get the usual error envelope.
	errBody(t, post(t, "/auth/dev-login", "", RawBody("{nope")), 400, "", "")
	errBody(t, post(t, "/auth/dev-login", "", map[string]any{}), 400, "", "")

	// Logging in again returns the same user rather than failing on the unique key.
	again, _ := loginAs(t, lower)
	me2 := ok(t, get(t, "/me", again.Token))
	str(t, "same id", me2.Get("id"), me.Get("id").Str())
	isTrue(t, "a second login mints a different access or refresh token", again.RefreshToken != first.RefreshToken)
}

func TestDevLoginDefaultsAndDisplayName(t *testing.T) {
	t.Parallel()
	name := "dflt_" + randHex(8)
	u, _ := loginAs(t, name)
	me := ok(t, get(t, "/me", u.Token))
	eq(t, "me field count", me.JSON.Len(), 7)
	str(t, "id", me.Get("id"), u.ID)
	str(t, "username", me.Get("username"), name)
	str(t, "display_name defaults to the username", me.Get("display_name"), name)
	str(t, "email", me.Get("email"), name+"@dev.local")
	null(t, "avatar_url", me.Get("avatar_url"))
	str(t, "unit_system", me.Get("unit_system"), "metric")
	str(t, "timezone", me.Get("timezone"), "UTC")

	// display_name on first login is used; later logins do not change it.
	n2 := "dn_" + randHex(8)
	r := post(t, "/auth/dev-login", "", map[string]any{"username": n2, "display_name": "Given Name"})
	ok(t, r)
	me = ok(t, get(t, "/me", r.Get("access_token").Str()))
	str(t, "display_name", me.Get("display_name"), "Given Name")
	r = post(t, "/auth/dev-login", "", map[string]any{"username": n2, "display_name": "Other"})
	ok(t, r)
	me = ok(t, get(t, "/me", r.Get("access_token").Str()))
	str(t, "display_name unchanged on re-login", me.Get("display_name"), "Given Name")
}

func TestProtectedRoutesRequireAValidToken(t *testing.T) {
	t.Parallel()
	for _, uri := range []string{"/me", "/exercises", "/tags", "/workouts", "/gyms", "/body-weights",
		"/stats/consistency", "/stats/exercises"} {
		unauthorized(t, get(t, uri, ""))
		unauthorized(t, get(t, uri, "not-a-jwt"))
	}
	// Wrong scheme, even with a good token.
	u := newUser(t, "scheme")
	// (a token containing a space is sent verbatim as the Authorization header value)
	unauthorized(t, get(t, "/me", "Basic "+u.Token))
	// Mutating routes are protected too.
	unauthorized(t, post(t, "/workouts", "", map[string]any{}))
	unauthorized(t, post(t, "/gyms", "", map[string]any{"name": "x"}))
	unauthorized(t, req(t, "PUT", "/body-weights/2026-01-05", "", map[string]any{"weight_kg": 80}))
	unauthorized(t, patch(t, "/me", "", map[string]any{}))
	unauthorized(t, req(t, "DELETE", "/me", "", map[string]any{"confirm_username": "x"}))
}

func TestUnknownRoutesAre404(t *testing.T) {
	t.Parallel()
	status(t, get(t, "/definitely-not-a-route", ""), 404)
}

// ---- refresh tokens -----------------------------------------------------------------------

func refresh(t *testing.T, token string) Resp {
	t.Helper()
	return post(t, "/auth/refresh", "", map[string]any{"refresh_token": token})
}

func TestRefreshRotatesAndIssuesAWorkingAccessToken(t *testing.T) {
	t.Parallel()
	name := "rita_" + randHex(8)
	u, login := loginAs(t, name)
	num(t, "refresh_expires_in", login.Get("refresh_expires_in"), 30*24*60*60)
	isTrue(t, "refresh token length", len(u.RefreshToken) >= 43)

	second := ok(t, refresh(t, u.RefreshToken))
	eq(t, "field count", second.JSON.Len(), 7)
	isTrue(t, "a new refresh token", second.Get("refresh_token").Str() != u.RefreshToken)
	str(t, "user_id", second.Get("user_id"), u.ID)
	str(t, "username", second.Get("username"), name)
	str(t, "token_type", second.Get("token_type"), "Bearer")
	num(t, "expires_in", second.Get("expires_in"), 3600)
	num(t, "refresh_expires_in", second.Get("refresh_expires_in"), 30*24*60*60)
	me := ok(t, get(t, "/me", second.Get("access_token").Str()))
	str(t, "me.username", me.Get("username"), name)

	// Both tokens share one family; the first is revoked, the second live.
	requireDB(t)
	got := sql(t, "select count(*), count(distinct family_id), count(*) filter (where revoked_at is null) "+
		"from refresh_tokens where user_id = '"+u.ID+"'")
	eq(t, "(rows, families, live)", got, "2|1|1")
}

func TestReusingARotatedRefreshTokenKillsTheWholeFamily(t *testing.T) {
	t.Parallel()
	name := "reuse_" + randHex(8)
	u, _ := loginAs(t, name)
	second := ok(t, refresh(t, u.RefreshToken)).Get("refresh_token").Str()

	unauthorized(t, refresh(t, u.RefreshToken))
	// The token legitimately issued from that family is dead too.
	unauthorized(t, refresh(t, second))

	// Another login (new family) is unaffected.
	other, _ := loginAs(t, name)
	ok(t, refresh(t, other.RefreshToken))
}

func TestUnknownAndExpiredRefreshTokensAreRejected(t *testing.T) {
	t.Parallel()
	unauthorized(t, refresh(t, "not-a-real-token"))
	errBody(t, post(t, "/auth/refresh", "", map[string]any{}), 400, "", "")
	errBody(t, post(t, "/auth/refresh", "", RawBody("nope")), 400, "", "")

	u := newUser(t, "expiry")
	requireDB(t)
	sql(t, "update refresh_tokens set expires_at = now() - interval '1 second' where user_id = '"+u.ID+"'")
	unauthorized(t, refresh(t, u.RefreshToken))
}

func TestAnAccessTokenIsNotAcceptedAsARefreshToken(t *testing.T) {
	t.Parallel()
	u := newUser(t, "swap")
	unauthorized(t, refresh(t, u.Token))
	// And a refresh token is not a bearer credential.
	unauthorized(t, get(t, "/me", u.RefreshToken))
}
