package httpapi

import (
	"context"

	"github.com/google/uuid"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/exercise"
	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
)

// exercisesAPI holds the handlers of the exercises operations in api/openapi.yaml.
type exercisesAPI struct{ exercises *exercise.Service }

func newExercisesAPI(_ Deps, q *db.Queries) exercisesAPI {
	return exercisesAPI{exercises: exercise.NewService(q)}
}

func (a exercisesAPI) SearchExercises(ctx context.Context, req gen.SearchExercisesRequestObject) (gen.SearchExercisesResponseObject, error) {
	q := ""
	if req.Params.Q != nil {
		q = *req.Params.Q
	}
	found, err := a.exercises.Search(ctx, userFrom(ctx), q, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	out := make(gen.SearchExercises200JSONResponse, 0, len(found))
	for _, e := range found {
		tags := e.SuggestedTagIDs
		if tags == nil {
			tags = []uuid.UUID{}
		}
		out = append(out, gen.Exercise{
			Id: e.ID, Slug: e.Slug, Name: e.Name, Kind: e.Kind, Status: e.Status, SuggestedTagIds: tags,
		})
	}
	return out, nil
}

func (a exercisesAPI) ListTags(ctx context.Context, _ gen.ListTagsRequestObject) (gen.ListTagsResponseObject, error) {
	tags, err := a.exercises.Tags(ctx)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListTags200JSONResponse, 0, len(tags))
	for _, t := range tags {
		out = append(out, gen.Tag{Id: t.ID, Slug: t.Slug, Name: t.Name, Category: t.Category})
	}
	return out, nil
}
