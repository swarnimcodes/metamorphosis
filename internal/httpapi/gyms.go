package httpapi

import (
	"context"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/gym"
	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
)

// gymsAPI holds the handlers of the gyms operations in api/openapi.yaml.
type gymsAPI struct{ gyms *gym.Service }

func newGymsAPI(_ Deps, q *db.Queries) gymsAPI { return gymsAPI{gyms: gym.NewService(q)} }

func gymOut(g gym.Gym) gen.Gym {
	return gen.Gym{
		Id: g.ID, Name: g.Name, Chain: g.Chain, Area: g.Area, Address: g.Address, City: g.City,
		Lat: g.Lat, Lng: g.Lng, MapsUrl: g.MapsURL, Verified: g.Verified,
	}
}

func (a gymsAPI) SearchGyms(ctx context.Context, req gen.SearchGymsRequestObject) (gen.SearchGymsResponseObject, error) {
	q := ""
	if req.Params.Q != nil {
		q = *req.Params.Q
	}
	found, err := a.gyms.Search(ctx, userFrom(ctx), q, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	out := make(gen.SearchGyms200JSONResponse, 0, len(found))
	for _, g := range found {
		out = append(out, gymOut(g))
	}
	return out, nil
}

func (a gymsAPI) CreateGym(ctx context.Context, req gen.CreateGymRequestObject) (gen.CreateGymResponseObject, error) {
	b := req.Body
	g, err := a.gyms.Create(ctx, userFrom(ctx), gym.NewGym{
		Name: &b.Name, Chain: b.Chain, Area: b.Area, Address: b.Address, City: b.City, MapsURL: b.MapsUrl,
		Force: b.Force != nil && *b.Force,
	})
	if err != nil {
		return nil, err
	}
	return gen.CreateGym201JSONResponse(gymOut(g)), nil
}
