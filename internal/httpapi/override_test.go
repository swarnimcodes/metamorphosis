package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// While a feature moves to the generated handlers, the legacy handler for the same route is still registered. The
// migration relies on the later registration replacing the earlier one, so pin that behaviour of chi.
func TestALaterRegistrationOfTheSameRouteReplacesTheEarlierOne(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/x/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("legacy")) })
	r.Group(func(g chi.Router) {
		g.Get("/x/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("generated")) })
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x/1", nil))
	if got := rec.Body.String(); got != "generated" {
		t.Fatalf("got %q: the later registration must win", got)
	}
}
