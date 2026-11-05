package acceptance

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- JSON values --------------------------------------------------------------------------

// Val wraps a decoded JSON value and remembers whether it was present at all, so a test can
// tell `"x": null` (present) from an omitted field (missing).
type Val struct {
	v       any
	present bool
}

func (x Val) Get(key string) Val {
	m, ok := x.v.(map[string]any)
	if !ok {
		return Val{}
	}
	v, ok := m[key]
	return Val{v: v, present: ok}
}

func (x Val) Idx(i int) Val {
	a, ok := x.v.([]any)
	if !ok || i < 0 || i >= len(a) {
		return Val{}
	}
	return Val{v: a[i], present: true}
}

func (x Val) Has(key string) bool { return x.Get(key).present }
func (x Val) IsNull() bool        { return x.present && x.v == nil }
func (x Val) IsArray() bool       { _, ok := x.v.([]any); return ok }
func (x Val) IsObject() bool      { _, ok := x.v.(map[string]any); return ok }
func (x Val) IsString() bool      { _, ok := x.v.(string); return ok }
func (x Val) IsNumber() bool      { _, ok := x.v.(float64); return ok }
func (x Val) IsBool() bool        { _, ok := x.v.(bool); return ok }

func (x Val) Arr() []Val {
	a, _ := x.v.([]any)
	out := make([]Val, len(a))
	for i, e := range a {
		out[i] = Val{v: e, present: true}
	}
	return out
}

func (x Val) Len() int {
	switch t := x.v.(type) {
	case []any:
		return len(t)
	case map[string]any:
		return len(t)
	}
	return -1
}

func (x Val) Keys() []string {
	m, _ := x.v.(map[string]any)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (x Val) Str() string    { s, _ := x.v.(string); return s }
func (x Val) Float() float64 { f, _ := x.v.(float64); return f }
func (x Val) Int() int64     { return int64(x.Float()) }
func (x Val) Bool() bool     { b, _ := x.v.(bool); return b }

func (x Val) String() string {
	if !x.present {
		return "<missing>"
	}
	b, _ := json.Marshal(x.v)
	return string(b)
}

// ---- HTTP client --------------------------------------------------------------------------

var baseURL = func() string {
	if v := os.Getenv("API_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:3100"
}()

var httpClient = &http.Client{Timeout: 30 * time.Second}

// Resp is one HTTP response with the body decoded as JSON when it is JSON.
type Resp struct {
	Status int
	Header http.Header
	Raw    []byte
	JSON   Val
}

func (r Resp) Get(key string) Val { return r.JSON.Get(key) }
func (r Resp) Idx(i int) Val      { return r.JSON.Idx(i) }
func (r Resp) String() string     { return fmt.Sprintf("%d %s", r.Status, r.Raw) }

// Raw is a request body sent verbatim (to test malformed JSON).
type RawBody string

func req(t testing.TB, method, path, token string, body any) Resp {
	t.Helper()
	var rd io.Reader
	hasBody := false
	switch b := body.(type) {
	case nil:
	case RawBody:
		rd, hasBody = strings.NewReader(string(b)), true
	default:
		enc, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		rd, hasBody = bytes.NewReader(enc), true
	}
	r, err := http.NewRequest(method, baseURL+path, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	switch {
	case token == "":
	case strings.Contains(token, " "): // a full Authorization header value, for scheme tests
		r.Header.Set("Authorization", token)
	default:
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if hasBody {
		r.Header.Set("Content-Type", "application/json")
	}
	res, err := httpClient.Do(r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	out := Resp{Status: res.StatusCode, Header: res.Header, Raw: raw}
	if len(bytes.TrimSpace(raw)) > 0 {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			out.JSON = Val{v: v, present: true}
		}
	}
	return out
}

func get(t testing.TB, path, token string) Resp { t.Helper(); return req(t, "GET", path, token, nil) }
func post(t testing.TB, path, token string, b any) Resp {
	t.Helper()
	return req(t, "POST", path, token, b)
}
func patch(t testing.TB, path, token string, b any) Resp {
	t.Helper()
	return req(t, "PATCH", path, token, b)
}
func put(t testing.TB, path, token string, b any) Resp {
	t.Helper()
	return req(t, "PUT", path, token, b)
}
func del(t testing.TB, path, token string) Resp {
	t.Helper()
	return req(t, "DELETE", path, token, nil)
}

// ---- assertions ---------------------------------------------------------------------------

func status(t testing.TB, r Resp, want int) {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status = %d, want %d; body: %s", r.Status, want, r.Raw)
	}
}

// ok asserts 200 and returns the response for chaining.
func ok(t testing.TB, r Resp) Resp      { t.Helper(); status(t, r, 200); return r }
func created(t testing.TB, r Resp) Resp { t.Helper(); status(t, r, 201); return r }
func noContent(t testing.TB, r Resp) {
	t.Helper()
	status(t, r, 204)
	if len(r.Raw) != 0 {
		t.Fatalf("204 must have an empty body, got %q", r.Raw)
	}
}

// errBody asserts the status and the API's error envelope {"error": message} (plus code when non-empty).
func errBody(t testing.TB, r Resp, wantStatus int, wantMessage, wantCode string) {
	t.Helper()
	status(t, r, wantStatus)
	if !r.JSON.IsObject() {
		t.Fatalf("error body is not a JSON object: %s", r.Raw)
	}
	if wantMessage != "" && r.Get("error").Str() != wantMessage {
		t.Fatalf("error = %q, want %q (body %s)", r.Get("error").Str(), wantMessage, r.Raw)
	}
	if wantMessage == "" && (!r.Get("error").IsString() || r.Get("error").Str() == "") {
		t.Fatalf("expected a non-empty string `error`, body %s", r.Raw)
	}
	if wantCode == "" {
		if r.Has2("code") {
			t.Fatalf("unexpected `code` in %s", r.Raw)
		}
	} else if r.Get("code").Str() != wantCode {
		t.Fatalf("code = %q, want %q (body %s)", r.Get("code").Str(), wantCode, r.Raw)
	}
}

func (r Resp) Has2(key string) bool { return r.JSON.Has(key) }

func unauthorized(t testing.TB, r Resp) {
	t.Helper()
	errBody(t, r, 401, "unauthorized", "")
}

// notFound asserts the API's own 404 for a missing (or someone else's) resource.
func notFound(t testing.TB, r Resp) {
	t.Helper()
	errBody(t, r, 404, "not found", "")
}

func eq[T comparable](t testing.TB, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func isTrue(t testing.TB, what string, cond bool) {
	t.Helper()
	if !cond {
		t.Fatalf("expected: %s", what)
	}
}

func near(t testing.TB, what string, got, want, eps float64) {
	t.Helper()
	d := got - want
	if d < 0 {
		d = -d
	}
	if d > eps {
		t.Fatalf("%s = %v, want %v (+-%v)", what, got, want, eps)
	}
}

// num asserts v is a JSON number (not a string) equal to want.
func num(t testing.TB, what string, v Val, want float64) {
	t.Helper()
	if !v.IsNumber() {
		t.Fatalf("%s = %s, want the JSON number %v", what, v, want)
	}
	near(t, what, v.Float(), want, 1e-9)
}

func null(t testing.TB, what string, v Val) {
	t.Helper()
	if !v.IsNull() {
		t.Fatalf("%s = %s, want null (present, not omitted)", what, v)
	}
}

func str(t testing.TB, what string, v Val, want string) {
	t.Helper()
	if !v.IsString() || v.Str() != want {
		t.Fatalf("%s = %s, want %q", what, v, want)
	}
}

func emptyArray(t testing.TB, what string, v Val) {
	t.Helper()
	if !v.IsArray() || v.Len() != 0 {
		t.Fatalf("%s = %s, want []", what, v)
	}
}

func arrayLen(t testing.TB, what string, v Val, want int) {
	t.Helper()
	if !v.IsArray() || v.Len() != want {
		t.Fatalf("%s: want array of %d, got %s", what, want, v)
	}
}

func strs(v Val, key string) []string {
	var out []string
	for _, e := range v.Arr() {
		out = append(out, e.Get(key).Str())
	}
	return out
}

func sameStrings(t testing.TB, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %q, want %q", what, got, want)
		}
	}
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// uuidish checks the 8-4-4-4-12 hex shape.
func uuidish(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdef", c) {
				return false
			}
		}
	}
	return true
}

// ---- random names -------------------------------------------------------------------------

func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)[:n]
}

// randLetters is lowercase a-z only, so it survives search normalisation and cannot be mistaken
// for a catalog word.
func randLetters(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = 'a' + b[i]%26
	}
	return string(b)
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func title(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// ---- users --------------------------------------------------------------------------------

// User is a signed-in dev-login account.
type User struct {
	Name         string
	ID           string
	Token        string
	RefreshToken string
	Login        Val
}

// loginAs signs in (creating the account on first use) and returns the whole login response.
func loginAs(t testing.TB, username string) (User, Resp) {
	t.Helper()
	r := post(t, "/auth/dev-login", "", map[string]any{"username": username})
	status(t, r, 200)
	return User{
		Name:         r.Get("username").Str(),
		ID:           r.Get("user_id").Str(),
		Token:        r.Get("access_token").Str(),
		RefreshToken: r.Get("refresh_token").Str(),
		Login:        r.JSON,
	}, r
}

// newUser creates a fresh account whose username is prefix_<8 hex>.
func newUser(t testing.TB, prefix string) User {
	t.Helper()
	u, _ := loginAs(t, prefix+"_"+randHex(8))
	return u
}

// ---- catalog lookups ----------------------------------------------------------------------

var (
	catalogMu  sync.Mutex
	exerciseID = map[string]string{}
	tagIDs     = map[string]string{}
)

func exerciseBySlug(t testing.TB, token, slug string) string {
	t.Helper()
	catalogMu.Lock()
	if id, ok := exerciseID[slug]; ok {
		catalogMu.Unlock()
		return id
	}
	catalogMu.Unlock()
	r := ok(t, get(t, "/exercises?q="+url.QueryEscape(slug), token))
	for _, e := range r.JSON.Arr() {
		if e.Get("slug").Str() == slug {
			catalogMu.Lock()
			exerciseID[slug] = e.Get("id").Str()
			catalogMu.Unlock()
			return e.Get("id").Str()
		}
	}
	t.Fatalf("exercise %s not found in %s", slug, r.Raw)
	return ""
}

func tagBySlug(t testing.TB, token, slug string) string {
	t.Helper()
	catalogMu.Lock()
	if id, ok := tagIDs[slug]; ok {
		catalogMu.Unlock()
		return id
	}
	catalogMu.Unlock()
	r := ok(t, get(t, "/tags", token))
	for _, e := range r.JSON.Arr() {
		if e.Get("slug").Str() == slug {
			catalogMu.Lock()
			tagIDs[slug] = e.Get("id").Str()
			catalogMu.Unlock()
			return e.Get("id").Str()
		}
	}
	t.Fatalf("tag %s not found", slug)
	return ""
}

// ---- optional SQL via psql ----------------------------------------------------------------

// sql runs a statement through the psql CLI against DATABASE_URL and returns trimmed stdout.
// The test is skipped when DATABASE_URL is not set.
func sql(t testing.TB, query string) string {
	t.Helper()
	out, err := trySQL(t, query)
	if err != nil {
		t.Fatalf("psql: %v\n%s", err, out)
	}
	return out
}

func trySQL(t testing.TB, query string) (string, error) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: this scenario needs direct database access")
	}
	cmd := exec.Command("psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", dsn, "-c", query)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		return stderr.String(), err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func requireDB(t testing.TB) {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set: this scenario needs direct database access")
	}
}

// ---- logging workouts through the API -----------------------------------------------------

type entry struct {
	exercise string
	tags     []string
	sets     []map[string]any
}

func kg(weight float64, reps int) map[string]any {
	return map[string]any{"weight_kg": weight, "reps": reps}
}

func warmup(weight float64, reps int) map[string]any {
	return map[string]any{"weight_kg": weight, "reps": reps, "set_type": "warmup"}
}

// logWorkout creates a workout at startedAt with the given exercises and sets; returns its id.
func logWorkout(t testing.TB, token, startedAt string, entries ...entry) string {
	t.Helper()
	w := created(t, post(t, "/workouts", token, map[string]any{"started_at": startedAt}))
	wid := w.Get("id").Str()
	for _, e := range entries {
		tags := e.tags
		if tags == nil {
			tags = []string{}
		}
		we := created(t, post(t, "/workouts/"+wid+"/exercises", token, map[string]any{"exercise_id": e.exercise, "tag_ids": tags}))
		for _, s := range e.sets {
			created(t, post(t, "/workout-exercises/"+we.Get("id").Str()+"/sets", token, s))
		}
	}
	return wid
}

func daysAgo(n int) time.Time {
	return time.Now().UTC().AddDate(0, 0, -n)
}

func ymd(d time.Time) string { return d.Format("2006-01-02") }

func at(d time.Time, hh, mm int) string {
	return fmt.Sprintf("%sT%02d:%02d:00Z", ymd(d), hh, mm)
}
