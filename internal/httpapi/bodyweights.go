package httpapi

import (
	"context"
	"time"

	"github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/smarniw/metamorphosis/internal/bodyweight"
	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
)

type bodyWeightAPI struct{ svc *bodyweight.Service }

func day(d *types.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time
	return &t
}

func weighIn(e bodyweight.Entry) gen.BodyWeightEntry {
	return gen.BodyWeightEntry{Date: types.Date{Time: e.Day}, WeightKg: e.WeightKg.InexactFloat64()}
}

func (a bodyWeightAPI) ListBodyWeights(ctx context.Context, req gen.ListBodyWeightsRequestObject) (gen.ListBodyWeightsResponseObject, error) {
	entries, err := a.svc.List(ctx, userFrom(ctx), day(req.Params.From), day(req.Params.To))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListBodyWeights200JSONResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, weighIn(e))
	}
	return out, nil
}

func (a bodyWeightAPI) SetBodyWeight(ctx context.Context, req gen.SetBodyWeightRequestObject) (gen.SetBodyWeightResponseObject, error) {
	e, err := a.svc.Set(ctx, userFrom(ctx), req.Date.Time, decimal.NewFromFloat(req.Body.WeightKg))
	if err != nil {
		return nil, err
	}
	return gen.SetBodyWeight200JSONResponse(weighIn(e)), nil
}

func (a bodyWeightAPI) DeleteBodyWeight(ctx context.Context, req gen.DeleteBodyWeightRequestObject) (gen.DeleteBodyWeightResponseObject, error) {
	if err := a.svc.Remove(ctx, userFrom(ctx), req.Date.Time); err != nil {
		return nil, err
	}
	return gen.DeleteBodyWeight204Response{}, nil
}
