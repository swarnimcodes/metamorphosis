// Google sign-in: the ID-token verifier and the login/signup endpoints. Tokens are signed with throwaway keys
// generated here; nothing touches the network or a real Google account.
package httpapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smarniw/metamorphosis/internal/config"
	"github.com/smarniw/metamorphosis/internal/google"
	"github.com/smarniw/metamorphosis/internal/httpapi"
	"github.com/smarniw/metamorphosis/internal/testutil"
)

const (
	clientID      = "web-client.apps.googleusercontent.com"
	otherClientID = "android-client.apps.googleusercontent.com"
	kid           = "test-key-1"
)

var keys = sync.OnceValue(func() [2]*rsa.PrivateKey {
	gen := func() *rsa.PrivateKey {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		return k
	}
	return [2]*rsa.PrivateKey{gen(), gen()}
})

// trusted is in the verifier's key set; untrusted is a different key that is not.
func trusted() *rsa.PrivateKey   { return keys()[0] }
func untrusted() *rsa.PrivateKey { return keys()[1] }

func now() int64 { return time.Now().Unix() }

func verifier() *google.Verifier {
	return google.NewStatic([]string{clientID, otherClientID}, map[string]*rsa.PublicKey{kid: &trusted().PublicKey})
}

// claims is a well-formed Google claim set that each test then tweaks.
func claims(sub, email string) map[string]any {
	return map[string]any{
		"iss": "https://accounts.google.com", "aud": clientID, "sub": sub, "email": email,
		"email_verified": true, "name": "Rahul Sharma", "picture": "https://example.com/p.png",
		"iat": now(), "exp": now() + 3600,
	}
}

func signWith(key any, method jwt.SigningMethod, kidValue string, c map[string]any) string {
	tok := jwt.NewWithClaims(method, jwt.MapClaims(c))
	if kidValue != "" {
		tok.Header["kid"] = kidValue
	}
	s, err := tok.SignedString(key)
	if err != nil {
		panic(err)
	}
	return s
}

func sign(c map[string]any) string { return signWith(trusted(), jwt.SigningMethodRS256, kid, c) }

func edit(base map[string]any, f func(map[string]any)) string {
	c := map[string]any{}
	for k, v := range base {
		c[k] = v
	}
	f(c)
	return sign(c)
}

// ---- verifier ----

func TestVerifierAcceptsAGoodToken(t *testing.T) {
	id, err := verifier().Verify(context.Background(), sign(claims("g-1", "rahul@example.com")))
	if err != nil {
		t.Fatal(err)
	}
	if id.Sub != "g-1" || id.Email != "rahul@example.com" || id.Name == nil || *id.Name != "Rahul Sharma" {
		t.Fatalf("unexpected identity %+v", id)
	}
}

func TestVerifierAcceptsEitherGoogleIssuerAndAnyConfiguredClient(t *testing.T) {
	v := verifier()
	c := claims("g-1", "a@example.com")
	c["iss"] = "accounts.google.com"
	if _, err := v.Verify(context.Background(), sign(c)); err != nil {
		t.Fatal(err)
	}
	c["aud"] = otherClientID
	if _, err := v.Verify(context.Background(), sign(c)); err != nil {
		t.Fatal(err)
	}
}

func TestVerifierRejectsEveryKindOfBadToken(t *testing.T) {
	v := verifier()
	base := claims("g-1", "a@example.com")
	cases := map[string]string{
		"wrong audience":             edit(base, func(c map[string]any) { c["aud"] = "someone-elses-client" }),
		"wrong issuer":               edit(base, func(c map[string]any) { c["iss"] = "https://evil.example.com" }),
		"expired":                    edit(base, func(c map[string]any) { c["exp"] = now() - 3600 }),
		"email not verified":         edit(base, func(c map[string]any) { c["email_verified"] = false }),
		"email_verified missing":     edit(base, func(c map[string]any) { delete(c, "email_verified") }),
		"email missing":              edit(base, func(c map[string]any) { delete(c, "email") }),
		"empty sub":                  edit(base, func(c map[string]any) { c["sub"] = "" }),
		"signed by an untrusted key": signWith(untrusted(), jwt.SigningMethodRS256, kid, base),
		"unknown kid":                signWith(trusted(), jwt.SigningMethodRS256, "rotated-away", base),
		"HS256 token":                signWith([]byte("any-shared-secret"), jwt.SigningMethodHS256, kid, base),
		"garbage":                    "not.a.jwt",
		"empty":                      "",
	}
	for name, token := range cases {
		if _, err := v.Verify(context.Background(), token); !errors.Is(err, google.ErrInvalid) {
			t.Errorf("%s: expected ErrInvalid, got %v", name, err)
		}
	}
}

func TestVerifierWithoutAKidOrWithAlgNoneIsRejected(t *testing.T) {
	v := verifier()
	enc := base64.RawURLEncoding.EncodeToString
	payload, _ := json.Marshal(claims("g-1", "a@example.com"))
	for _, header := range []string{`{"alg":"none","typ":"JWT"}`, `{"alg":"RS256","typ":"JWT"}`} {
		token := enc([]byte(header)) + "." + enc(payload) + "."
		if _, err := v.Verify(context.Background(), token); !errors.Is(err, google.ErrInvalid) {
			t.Errorf("%s: expected ErrInvalid, got %v", header, err)
		}
	}
}

// ---- endpoints ----

func testConfig(origins ...string) *config.Config {
	return &config.Config{
		JWTSecret: "test-secret", AccessTokenTTLSecs: 3600, GoogleClientIDs: []string{clientID}, CORSOrigins: origins,
	}
}

type app struct {
	t    *testing.T
	pool *pgxpool.Pool
	h    http.Handler
}

func newApp(t *testing.T) *app {
	pool := testutil.NewDB(t)
	return &app{t, pool, httpapi.NewHandler(httpapi.Deps{DB: pool, Cfg: testConfig(), Google: verifier()})}
}

type reply struct {
	status int
	body   map[string]any
	header http.Header
}

func (a *app) call(method, uri, token string, body any) reply {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, uri, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	var parsed map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return reply{rec.Code, parsed, rec.Header()}
}

func (a *app) googleLogin(token string) reply {
	return a.call("POST", "/auth/google", "", map[string]any{"id_token": token})
}

func (a *app) signup(signupToken, username string) reply {
	return a.call("POST", "/auth/google/signup", "", map[string]any{"signup_token": signupToken, "username": username})
}

// beginSignup runs /auth/google for a brand-new identity and returns its response.
func (a *app) beginSignup(sub, email string) map[string]any {
	r := a.googleLogin(sign(claims(sub, email)))
	if r.status != 200 || r.body["signup_required"] != true {
		a.t.Fatalf("expected signup_required, got %d %v", r.status, r.body)
	}
	return r.body
}

func (a *app) count(query string) int {
	var n int
	if err := a.pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}

func (a *app) userCount() int { return a.count("select count(*) from users") }

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func TestNewGoogleUserSignsUpThenLogsStraightIn(t *testing.T) {
	a := newApp(t)
	start := a.beginSignup("g-100", "rahul.sharma@example.com")
	eq(t, "suggested_username", start["suggested_username"], "rahul_sharma")
	eq(t, "display_name", start["display_name"], "Rahul Sharma")
	eq(t, "email", start["email"], "rahul.sharma@example.com")
	eq(t, "avatar_url", start["avatar_url"], "https://example.com/p.png")
	eq(t, "users before signup", a.userCount(), 0)

	avail := a.call("GET", "/auth/username-available?username=rahul_sharma", "", nil)
	eq(t, "available", avail.body["available"], true)

	login := a.signup(start["signup_token"].(string), "  Rahul_S  ")
	eq(t, "signup status", login.status, 200)
	eq(t, "username is trimmed and lowercased", login.body["username"], "rahul_s")
	for _, f := range []string{"access_token", "refresh_token", "user_id", "expires_in", "refresh_expires_in"} {
		if login.body[f] == nil {
			t.Errorf("%s missing from %v", f, login.body)
		}
	}
	eq(t, "token_type", login.body["token_type"], "Bearer")

	me := a.call("GET", "/me", login.body["access_token"].(string), nil)
	eq(t, "me status", me.status, 200)
	eq(t, "me.username", me.body["username"], "rahul_s")
	eq(t, "me.email", me.body["email"], "rahul.sharma@example.com")
	eq(t, "me.display_name", me.body["display_name"], "Rahul Sharma")
	eq(t, "me.avatar_url", me.body["avatar_url"], "https://example.com/p.png")

	var provider, subject string
	if err := a.pool.QueryRow(context.Background(),
		"select provider::text, provider_subject from auth_identities").Scan(&provider, &subject); err != nil {
		t.Fatal(err)
	}
	eq(t, "linked identity", provider+"/"+subject, "google/g-100")

	// The next sign-in is a plain login for the same user.
	again := a.googleLogin(sign(claims("g-100", "rahul.sharma@example.com")))
	eq(t, "second login status", again.status, 200)
	eq(t, "signup_required", again.body["signup_required"], false)
	eq(t, "same user", again.body["user_id"], login.body["user_id"])
	eq(t, "users after", a.userCount(), 1)

	// ...and its refresh token works through the shared refresh endpoint.
	refreshed := a.call("POST", "/auth/refresh", "", map[string]any{"refresh_token": again.body["refresh_token"]})
	eq(t, "refresh status", refreshed.status, 200)
}

func TestUsernameRulesAreEnforcedWithActionableCodes(t *testing.T) {
	a := newApp(t)
	first := a.beginSignup("g-1", "one@example.com")
	eq(t, "first signup", a.signup(first["signup_token"].(string), "taken_name").status, 200)

	second := a.beginSignup("g-2", "two@example.com")
	token := second["signup_token"].(string)
	for _, c := range []struct {
		name   string
		status int
		code   string
	}{
		{"Taken_Name", 409, "username_taken"}, // case-insensitive
		{"admin", 400, "reserved_username"},
		{"ab", 400, "invalid_username"},
		{"has space", 400, "invalid_username"},
		{"dash-ed", 400, "invalid_username"},
		{"ünïcode", 400, "invalid_username"},
		{strings.Repeat("x", 31), 400, "invalid_username"},
	} {
		r := a.signup(token, c.name)
		if r.status != c.status || r.body["code"] != c.code {
			t.Errorf("%q: got %d %v, want %d %s", c.name, r.status, r.body, c.status, c.code)
		}
	}
	eq(t, "failed signups must not leave users behind", a.userCount(), 1)

	for name, reason := range map[string]string{"taken_name": "taken", "TAKEN_NAME": "taken", "admin": "reserved", "a": "invalid", "no spaces": "invalid"} {
		r := a.call("GET", "/auth/username-available?username="+url.QueryEscape(name), "", nil)
		if r.body["available"] != false || r.body["reason"] != reason {
			t.Errorf("%q: got %v, want unavailable (%s)", name, r.body, reason)
		}
	}

	// The same signup token still works once the name is fixed.
	eq(t, "retry with a free name", a.signup(token, "fresh_name").status, 200)
}

func TestSuggestedUsernameAvoidsTakenAndReservedNames(t *testing.T) {
	a := newApp(t)
	first := a.beginSignup("g-1", "rahul.sharma@example.com")
	a.signup(first["signup_token"].(string), "rahul_sharma")

	second := a.beginSignup("g-2", "other@example.com")
	suggestion := second["suggested_username"].(string)
	if suggestion == "rahul_sharma" || !strings.HasPrefix(suggestion, "rahul_sharma") {
		t.Errorf("suggestion %q must be a variant of rahul_sharma", suggestion)
	}
	avail := a.call("GET", "/auth/username-available?username="+suggestion, "", nil)
	eq(t, "the suggestion must itself be available", avail.body["available"], true)

	// No name and a reserved email local part still produce a valid suggestion.
	c := claims("g-3", "admin@example.com")
	c["name"] = ""
	third := a.googleLogin(sign(c)).body
	suggestion = third["suggested_username"].(string)
	if !strings.HasPrefix(suggestion, "admin") || suggestion == "admin" {
		t.Errorf("suggestion %q must be a variant of admin", suggestion)
	}
	eq(t, "display_name falls back to the suggestion", third["display_name"], suggestion)
}

func TestAnExistingEmailIsNeverAutoLinked(t *testing.T) {
	a := newApp(t)
	first := a.beginSignup("g-1", "same@example.com")
	a.signup(first["signup_token"].(string), "first_user")

	// A different Google account (new sub) that reports the same email.
	second := a.beginSignup("g-2", "same@example.com")
	r := a.signup(second["signup_token"].(string), "second_user")
	if r.status != 409 || r.body["code"] != "email_in_use" {
		t.Errorf("got %d %v, want 409 email_in_use", r.status, r.body)
	}
	eq(t, "users", a.userCount(), 1)
	eq(t, "the failed signup must roll back its identity too", a.count("select count(*) from auth_identities"), 1)
}

func TestASignupTokenCannotBeReplayed(t *testing.T) {
	a := newApp(t)
	start := a.beginSignup("g-1", "one@example.com")
	token := start["signup_token"].(string)
	eq(t, "first use", a.signup(token, "first_try").status, 200)

	r := a.signup(token, "second_try")
	if r.status != 409 || r.body["code"] != "identity_exists" {
		t.Errorf("got %d %v, want 409 identity_exists", r.status, r.body)
	}
	eq(t, "users", a.userCount(), 1)
}

func TestSignupAndAccessTokensAreNotInterchangeable(t *testing.T) {
	a := newApp(t)
	start := a.beginSignup("g-1", "one@example.com")
	signupToken := start["signup_token"].(string)

	// A signup token is not a session.
	eq(t, "/me with a signup token", a.call("GET", "/me", signupToken, nil).status, 401)
	eq(t, "/workouts with a signup token", a.call("GET", "/workouts", signupToken, nil).status, 401)

	// An access token is not a signup token.
	login := a.signup(signupToken, "legit_user")
	access := login.body["access_token"].(string)
	eq(t, "signup with an access token", a.signup(access, "sneaky_user").status, 401)
	eq(t, "signup with garbage", a.signup("garbage", "sneaky_user").status, 401)
	eq(t, "users", a.userCount(), 1)

	// A Google ID token is neither.
	eq(t, "/me with a Google token", a.call("GET", "/me", sign(claims("g-1", "one@example.com")), nil).status, 401)
}

func TestBadGoogleTokensAre401AndCreateNothing(t *testing.T) {
	a := newApp(t)
	wrongAud := claims("g-1", "one@example.com")
	wrongAud["aud"] = "another-app"
	for _, token := range []string{
		sign(wrongAud),
		signWith(untrusted(), jwt.SigningMethodRS256, kid, claims("g-1", "one@example.com")),
		"junk",
	} {
		eq(t, "status", a.googleLogin(token).status, 401)
	}
	r := a.call("POST", "/auth/google", "", map[string]any{"id_token": 5})
	eq(t, "a non-string id_token", r.status, 400)
	eq(t, "users", a.userCount(), 0)
}

func TestGoogleRoutesAreAbsentWhenNotConfigured(t *testing.T) {
	pool := testutil.NewDB(t)
	h := httpapi.NewHandler(httpapi.Deps{DB: pool, Cfg: testConfig()})
	for _, c := range [][2]string{{"POST", "/auth/google"}, {"POST", "/auth/google/signup"}, {"GET", "/auth/username-available?username=abc"}} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c[0], c[1], strings.NewReader("{}")))
		eq(t, c[1], rec.Code, 404)
	}
}

func TestCORSAllowsOnlyConfiguredOriginsWithoutDevLogin(t *testing.T) {
	pool := testutil.NewDB(t)
	h := httpapi.NewHandler(httpapi.Deps{DB: pool, Cfg: testConfig("http://localhost:1420"), Google: verifier()})
	preflight := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/auth/google", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	eq(t, "allowed origin", preflight("http://localhost:1420").Header().Get("Access-Control-Allow-Origin"), "http://localhost:1420")
	eq(t, "denied origin", preflight("http://evil.example.com").Header().Get("Access-Control-Allow-Origin"), "")
}

// The `typ` claim is what keeps the token kinds apart even when the rest of a forged token would parse: same
// secret, a valid user id as `sub`, only `typ` differs.
func TestTheTypClaimAloneSeparatesTokenKinds(t *testing.T) {
	a := newApp(t)
	start := a.beginSignup("g-1", "one@example.com")
	login := a.signup(start["signup_token"].(string), "typ_user")
	userID := login.body["user_id"].(string)

	forge := func(typ string) string {
		return signWith([]byte("test-secret"), jwt.SigningMethodHS256, "",
			map[string]any{"typ": typ, "sub": userID, "exp": now() + 3600})
	}
	eq(t, "control: a correctly typed token is accepted", a.call("GET", "/me", forge("access"), nil).status, 200)
	for _, typ := range []string{"signup", "refresh", ""} {
		eq(t, fmt.Sprintf("typ=%q must not work as an access token", typ), a.call("GET", "/me", forge(typ), nil).status, 401)
	}
}
