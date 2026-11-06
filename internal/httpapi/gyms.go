package httpapi

import "github.com/smarniw/metamorphosis/internal/db"

// gymsAPI holds the handlers of the gyms operations in api/openapi.yaml.
type gymsAPI struct{}

func newGymsAPI(_ Deps, _ *db.Queries) gymsAPI { return gymsAPI{} }
