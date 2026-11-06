package httpapi

import "github.com/smarniw/metamorphosis/internal/db"

// statsAPI holds the handlers of the stats operations in api/openapi.yaml.
type statsAPI struct{}

func newStatsAPI(_ Deps, _ *db.Queries) statsAPI { return statsAPI{} }
