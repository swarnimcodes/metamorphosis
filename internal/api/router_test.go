package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smarniw/metamorphosis/internal/api"
	"github.com/smarniw/metamorphosis/internal/config"
	"github.com/smarniw/metamorphosis/internal/testutil"
)

// The router's behaviour outside the handlers: unknown routes, wrong methods and CORS. These pin what the
// apps and browsers have been getting, so changing the router library cannot change it unnoticed.

func routerHandler(t *testing.T, cfg *config.Config) http.Handler {
	t.Helper()
	return (&api.Server{DB: testutil.NewDB(t), Cfg: cfg}).Handler()
}

func serve(h http.Handler, method, path, origin string, extra map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUnknownRoutesAndWrongMethods(t *testing.T) {
	h := routerHandler(t, testConfig())
	rec := serve(h, "GET", "/nope", "", nil)
	eq(t, "unknown path status", rec.Code, 404)
	eq(t, "unknown path body", strings.TrimSpace(rec.Body.String()), "404 page not found")
	eq(t, "unknown path type", strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain"), true)

	rec = serve(h, "DELETE", "/health", "", nil)
	eq(t, "wrong method status", rec.Code, 405)
	if rec.Header().Get("Allow") == "" {
		t.Error("a 405 must say which methods are allowed")
	}
}

func TestCORSWithConfiguredOrigins(t *testing.T) {
	h := routerHandler(t, testConfig("https://tauri.localhost", "http://localhost:1420"))
	const methods, headers = "GET,POST,PUT,PATCH,DELETE", "authorization,content-type,accept"
	preflight := map[string]string{"Access-Control-Request-Method": "PATCH", "Access-Control-Request-Headers": "authorization, content-type"}

	for _, c := range []struct {
		origin  string
		allowed bool
	}{
		{"https://tauri.localhost", true}, {"http://localhost:1420", true}, {"https://evil.example.com", false}, {"", false},
	} {
		name := "origin " + c.origin
		// Every response varies by Origin, and only an allowed origin is echoed back.
		rec := serve(h, "GET", "/health", c.origin, nil)
		eq(t, name+" GET status", rec.Code, 200)
		eq(t, name+" Vary", rec.Header().Get("Vary"), "Origin")
		want := ""
		if c.allowed {
			want = c.origin
		}
		eq(t, name+" Allow-Origin", rec.Header().Get("Access-Control-Allow-Origin"), want)
		eq(t, name+" simple GET has no preflight headers", rec.Header().Get("Access-Control-Allow-Methods"), "")

		// An OPTIONS request is answered by the CORS layer itself, whether or not it names a method.
		for _, extra := range []map[string]string{preflight, nil} {
			rec := serve(h, "OPTIONS", "/me", c.origin, extra)
			eq(t, name+" OPTIONS status", rec.Code, 200)
			eq(t, name+" OPTIONS Allow-Origin", rec.Header().Get("Access-Control-Allow-Origin"), want)
			eq(t, name+" OPTIONS Allow-Methods", rec.Header().Get("Access-Control-Allow-Methods"), methods)
			eq(t, name+" OPTIONS Allow-Headers", rec.Header().Get("Access-Control-Allow-Headers"), headers)
		}
		// Errors carry the CORS headers too, or the app could not read them.
		rec = serve(h, "GET", "/me", c.origin, nil)
		eq(t, name+" 401 status", rec.Code, 401)
		eq(t, name+" 401 Allow-Origin", rec.Header().Get("Access-Control-Allow-Origin"), want)
		rec = serve(h, "GET", "/nope", c.origin, nil)
		eq(t, name+" 404 Allow-Origin", rec.Header().Get("Access-Control-Allow-Origin"), want)
	}
}

func TestCORSIsClosedWithoutOriginsAndPermissiveInDevMode(t *testing.T) {
	closed := routerHandler(t, testConfig())
	rec := serve(closed, "GET", "/health", "https://tauri.localhost", nil)
	eq(t, "production without origins: no CORS headers", rec.Header().Get("Access-Control-Allow-Origin")+rec.Header().Get("Vary"), "")

	dev := testConfig()
	dev.DevLogin = true
	h := routerHandler(t, dev)
	rec = serve(h, "GET", "/health", "http://anything.example", nil)
	eq(t, "dev: any origin", rec.Header().Get("Access-Control-Allow-Origin"), "*")
	rec = serve(h, "OPTIONS", "/me", "http://anything.example", map[string]string{"Access-Control-Request-Method": "PATCH"})
	eq(t, "dev preflight status", rec.Code, 200)
	eq(t, "dev preflight methods", rec.Header().Get("Access-Control-Allow-Methods"), "*")
}
