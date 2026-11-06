package httpapi

import "github.com/smarniw/metamorphosis/internal/db"

// workoutsAPI holds the handlers of the workouts operations in api/openapi.yaml.
type workoutsAPI struct{}

func newWorkoutsAPI(_ Deps, _ *db.Queries) workoutsAPI { return workoutsAPI{} }
