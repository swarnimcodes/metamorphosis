package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smarniw/metamorphosis/internal/httpapi"
	"github.com/smarniw/metamorphosis/internal/testutil"
)

func hardenedHandler(t *testing.T, authLimit int) http.Handler {
	t.Helper()
	cfg := testConfig()
	cfg.AuthRateLimit = authLimit
	return httpapi.NewHandler(httpapi.Deps{DB: testutil.NewDB(t), Cfg: cfg, Google: verifier()})
}

func fromClient(h http.Handler, method, path, ip, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:40000" // the local proxy: the client's address is in the header
	req.Header.Set("CF-Connecting-IP", ip)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSignInRoutesAreRateLimitedPerClientAddress(t *testing.T) {
	h := hardenedHandler(t, 3)
	refresh := func(ip string) *httptest.ResponseRecorder {
		return fromClient(h, http.MethodPost, "/auth/refresh", ip, `{"refresh_token":"nope"}`)
	}
	for i := 1; i <= 3; i++ {
		if rec := refresh("203.0.113.1"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: got %d, want 401 (a wrong token, not yet limited)", i, rec.Code)
		}
	}
	rec := refresh("203.0.113.1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" ||
		!strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("4th request: got %d %q (Retry-After %q), want a JSON 429 with Retry-After", rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"))
	}
	if rec := refresh("203.0.113.2"); rec.Code != http.StatusUnauthorized {
		t.Errorf("another client was limited too: got %d", rec.Code)
	}
	// Only /auth/* is limited.
	for i := 0; i < 10; i++ {
		if rec := fromClient(h, http.MethodGet, "/health", "203.0.113.1", ""); rec.Code != http.StatusOK {
			t.Fatalf("/health request %d: got %d", i, rec.Code)
		}
	}
}

func TestCheckingUsernamesHasItsOwnLooserBudget(t *testing.T) {
	h := hardenedHandler(t, 3) // 3 a minute for sign-in, 12 for the username check
	check := func() int {
		return fromClient(h, http.MethodGet, "/auth/username-available?username=abc", "203.0.113.5", "").Code
	}
	for i := 1; i <= 12; i++ {
		if code := check(); code != http.StatusOK {
			t.Fatalf("check %d: got %d, want 200", i, code)
		}
	}
	if code := check(); code != http.StatusTooManyRequests {
		t.Errorf("13th check: got %d, want 429", code)
	}
	// Using up the username budget does not touch the sign-in budget.
	if rec := fromClient(h, http.MethodPost, "/auth/refresh", "203.0.113.5", `{"refresh_token":"nope"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("sign-in after the username budget ran out: got %d, want 401", rec.Code)
	}
}

func TestTheRateLimitCanBeTurnedOff(t *testing.T) {
	h := hardenedHandler(t, 0)
	for i := 0; i < 50; i++ {
		if rec := fromClient(h, http.MethodPost, "/auth/refresh", "203.0.113.1", `{"refresh_token":"nope"}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: got %d, want 401", i, rec.Code)
		}
	}
}

func TestAHugeRequestBodyIsRefusedAndResponsesAreNotCacheable(t *testing.T) {
	h := hardenedHandler(t, 0)
	rec := fromClient(h, http.MethodPost, "/auth/refresh", "203.0.113.1", `{"refresh_token":"`+strings.Repeat("a", 2<<20)+`"}`)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a 2 MiB body: got %d, want 413", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control is %q, want no-store", got)
	}
	if rec := fromClient(h, http.MethodGet, "/health", "203.0.113.1", ""); rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("even /health must say no-store")
	}
}
