// Package httpapi is the HTTP layer generated from api/openapi.yaml: the generated chi router and strict server
// call the handlers here, and each handler maps between the wire types and a feature's service.
package httpapi

import (
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smarniw/metamorphosis/internal/bodyweight"
	"github.com/smarniw/metamorphosis/internal/config"
	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/google"
	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
)

// Deps is what the handlers are built from.
type Deps struct {
	DB  *pgxpool.Pool
	Cfg *config.Config
	// Google is nil unless Google sign-in is configured.
	Google *google.Verifier
}

// Server implements the generated strict server by composing one handler set per feature. Each feature's
// operations live in its own file; adding an operation to the spec means adding a method there.
type Server struct {
	accountAPI
	gymsAPI
	exercisesAPI
	workoutsAPI
	statsAPI
	bodyWeightAPI
}

// Register mounts every operation of the spec on r.
func Register(r chi.Router, d Deps) {
	q := db.New(d.DB)
	srv := &Server{
		accountAPI:    newAccountAPI(d, q),
		gymsAPI:       newGymsAPI(d, q),
		exercisesAPI:  newExercisesAPI(d, q),
		workoutsAPI:   newWorkoutsAPI(d, q),
		statsAPI:      newStatsAPI(d, q),
		bodyWeightAPI: bodyWeightAPI{svc: bodyweight.NewService(q)},
	}
	strict := gen.NewStrictHandlerWithOptions(srv, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: renderError,
	})
	r.Group(func(g chi.Router) {
		// Before routing: auth first, so 401 beats 400 and 404.
		g.Use(authenticate(d.DB, d.Cfg))
		gen.HandlerWithOptions(strict, gen.ChiServerOptions{BaseRouter: g, ErrorHandlerFunc: badRequest})
	})
}
