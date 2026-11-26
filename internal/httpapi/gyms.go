package httpapi

import (
	"context"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/gym"
	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
)

// gymsAPI holds the handlers of the gyms operations in api/openapi.yaml.
type gymsAPI struct{ gyms *gym.Service }

func newGymsAPI(d Deps, q *db.Queries) gymsAPI {
	return gymsAPI{gyms: gym.NewService(q, func(ctx context.Context, fn func(*db.Queries) error) error {
		return db.InTx(ctx, d.DB, fn)
	})}
}

func gymOut(g gym.Gym) gen.Gym {
	return gen.Gym{
		Id: g.ID, Name: g.Name, Chain: g.Chain, Area: g.Area, Address: g.Address, City: g.City,
		Lat: g.Lat, Lng: g.Lng, MapsUrl: g.MapsURL, Verified: g.Verified, CountryCode: g.CountryCode, State: g.State,
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
		Name: &b.Name, Chain: b.Chain, Area: b.Area, Address: b.Address, City: b.City, State: b.State, CountryCode: b.CountryCode,
		MapsURL: b.MapsUrl, Force: b.Force != nil && *b.Force,
	})
	if err != nil {
		return nil, err
	}
	return gen.CreateGym201JSONResponse(gymOut(g)), nil
}

func (a gymsAPI) GetGym(ctx context.Context, req gen.GetGymRequestObject) (gen.GetGymResponseObject, error) {
	d, err := a.gyms.Get(ctx, userFrom(ctx), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.GetGym200JSONResponse{
		Gym: gymOut(d.Gym), Visits: d.Visits, LastVisit: d.LastVisit, Editable: d.Can.Edit,
		Can: gen.GymAbilities{Edit: d.Can.Edit, Verify: d.Can.Verify, Revert: d.Can.Revert, Merge: d.Can.Merge, Delete: d.Can.Delete},
	}, nil
}

func (a gymsAPI) UpdateGym(ctx context.Context, req gen.UpdateGymRequestObject) (gen.UpdateGymResponseObject, error) {
	b := req.Body
	g, err := a.gyms.Update(ctx, userFrom(ctx), req.Id, gym.Patch{
		Name: b.Name, Chain: b.Chain, Area: b.Area, Address: b.Address, City: b.City, State: b.State,
		CountryCode: b.CountryCode, MapsURL: b.MapsUrl,
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateGym200JSONResponse(gymOut(g)), nil
}

func (a gymsAPI) GetGymHistory(ctx context.Context, req gen.GetGymHistoryRequestObject) (gen.GetGymHistoryResponseObject, error) {
	edits, err := a.gyms.History(ctx, userFrom(ctx), req.Id)
	if err != nil {
		return nil, err
	}
	out := make(gen.GetGymHistory200JSONResponse, 0, len(edits))
	for _, e := range edits {
		changes := make(map[string]gen.GymChange, len(e.Changes))
		for k, c := range e.Changes {
			changes[k] = gen.GymChange{From: c.From, To: c.To}
		}
		out = append(out, gen.GymEdit{
			Id: e.ID, Kind: gen.GymEditKind(e.Kind), ByMe: e.ByMe, By: e.By, At: e.At, Changes: changes,
			Reverted: e.Reverted, Revertible: e.Revertible,
		})
	}
	return out, nil
}

func (a gymsAPI) ListGymChains(ctx context.Context, _ gen.ListGymChainsRequestObject) (gen.ListGymChainsResponseObject, error) {
	chains, err := a.gyms.Chains(ctx)
	if err != nil {
		return nil, err
	}
	return gen.ListGymChains200JSONResponse(chains), nil
}

func (a gymsAPI) DeleteGym(ctx context.Context, req gen.DeleteGymRequestObject) (gen.DeleteGymResponseObject, error) {
	if err := a.gyms.Remove(ctx, userFrom(ctx), req.Id); err != nil {
		return nil, err
	}
	return gen.DeleteGym204Response{}, nil
}

func (a gymsAPI) SetGymVerified(ctx context.Context, req gen.SetGymVerifiedRequestObject) (gen.SetGymVerifiedResponseObject, error) {
	g, err := a.gyms.SetVerified(ctx, userFrom(ctx), req.Id, req.Body.Verified)
	if err != nil {
		return nil, err
	}
	return gen.SetGymVerified200JSONResponse(gymOut(g)), nil
}

func (a gymsAPI) MergeGym(ctx context.Context, req gen.MergeGymRequestObject) (gen.MergeGymResponseObject, error) {
	g, err := a.gyms.Merge(ctx, userFrom(ctx), req.Id, req.Body.Into)
	if err != nil {
		return nil, err
	}
	return gen.MergeGym200JSONResponse(gymOut(g)), nil
}

func (a gymsAPI) RevertGymEdit(ctx context.Context, req gen.RevertGymEditRequestObject) (gen.RevertGymEditResponseObject, error) {
	g, err := a.gyms.Revert(ctx, userFrom(ctx), req.Id, req.EditId)
	if err != nil {
		return nil, err
	}
	return gen.RevertGymEdit200JSONResponse(gymOut(g)), nil
}
