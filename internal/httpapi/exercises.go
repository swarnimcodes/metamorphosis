package httpapi

import "github.com/smarniw/metamorphosis/internal/db"

// exercisesAPI holds the handlers of the exercises operations in api/openapi.yaml.
type exercisesAPI struct{}

func newExercisesAPI(_ Deps, _ *db.Queries) exercisesAPI { return exercisesAPI{} }
