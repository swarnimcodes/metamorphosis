package httpapi

import (
	"context"
	"time"

	"github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
	"github.com/smarniw/metamorphosis/internal/stats"
)

// statsAPI holds the handlers of the stats operations in api/openapi.yaml.
type statsAPI struct{ svc *stats.Service }

func newStatsAPI(_ Deps, q *db.Queries) statsAPI { return statsAPI{svc: stats.NewService(q)} }

func statsDay(t time.Time) types.Date { return types.Date{Time: t} }

// statsNum is a decimal on the wire. A decimal has no negative zero, so none can reach the client.
func statsNum(d decimal.Decimal) float64 { return d.InexactFloat64() }

func statsNumPtr(d *decimal.Decimal) *float64 {
	if d == nil {
		return nil
	}
	v := statsNum(*d)
	return &v
}

func (a statsAPI) GetStatsConsistency(ctx context.Context, req gen.GetStatsConsistencyRequestObject) (gen.GetStatsConsistencyResponseObject, error) {
	weeks := int64(stats.DefaultWeeks)
	if req.Params.Weeks != nil {
		weeks = *req.Params.Weeks
	}
	c, err := a.svc.Consistency(ctx, userFrom(ctx), weeks)
	if err != nil {
		return nil, err
	}
	days := make([]gen.ConsistencyDay, 0, len(c.Days))
	for _, d := range c.Days {
		days = append(days, gen.ConsistencyDay{Date: statsDay(d.Date), Workouts: d.Workouts, Sets: d.Sets, VolumeKg: statsNum(d.Volume)})
	}
	return gen.GetStatsConsistency200JSONResponse{
		Timezone: c.Timezone, From: statsDay(c.From), To: statsDay(c.To), Days: days,
		Totals: gen.ConsistencyTotals{
			Workouts: c.Totals.Workouts, ActiveDays: c.Totals.ActiveDays, Sets: c.Totals.Sets, VolumeKg: statsNum(c.Totals.Volume),
		},
		Streak: gen.ConsistencyStreak{CurrentWeeks: int64(c.CurrentWeeks), LongestWeeks: int64(c.LongestWeeks)},
	}, nil
}

func (a statsAPI) ListStatsExercises(ctx context.Context, _ gen.ListStatsExercisesRequestObject) (gen.ListStatsExercisesResponseObject, error) {
	list, err := a.svc.LoggedExercises(ctx, userFrom(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListStatsExercises200JSONResponse, 0, len(list))
	for _, e := range list {
		out = append(out, gen.StatsLoggedExercise{
			ExerciseId: e.ExerciseID, Name: e.Name, Kind: e.Kind, VariantKey: e.VariantKey,
			VariantLabel: e.VariantLabel, Sessions: e.Sessions, LastPerformed: statsDay(e.LastPerformed),
		})
	}
	return out, nil
}

func statsNumRecord(r *stats.Record[decimal.Decimal]) *gen.ProgressNumberRecord {
	if r == nil {
		return nil
	}
	return &gen.ProgressNumberRecord{Value: statsNum(r.Value), Date: statsDay(r.Date)}
}

func statsCountRecord(r *stats.Record[int32]) *gen.ProgressCountRecord {
	if r == nil {
		return nil
	}
	return &gen.ProgressCountRecord{Value: r.Value, Date: statsDay(r.Date)}
}

func (a statsAPI) GetStatsExerciseProgress(ctx context.Context, req gen.GetStatsExerciseProgressRequestObject) (gen.GetStatsExerciseProgressResponseObject, error) {
	p, err := a.svc.Progress(ctx, userFrom(ctx), req.Id, req.Params.VariantKey)
	if err != nil {
		return nil, err
	}
	points := make([]gen.ProgressPoint, 0, len(p.Points))
	for _, pt := range p.Points {
		points = append(points, gen.ProgressPoint{
			Date: statsDay(pt.Date), WorkoutId: pt.WorkoutID, Sets: pt.Sets,
			VolumeKg: statsNumPtr(pt.VolumeKg), TopWeightKg: statsNumPtr(pt.TopWeightKg), BestE1rmKg: statsNumPtr(pt.BestE1RMKg),
			TopReps: pt.TopReps, TotalReps: pt.TotalReps,
			BestDurationS: pt.BestDurationS, TotalDurationS: pt.TotalDurationS,
			BestDistanceM: statsNumPtr(pt.BestDistanceM), TotalDistanceM: statsNumPtr(pt.TotalDistanceM),
		})
	}
	r := p.Records
	return gen.GetStatsExerciseProgress200JSONResponse{
		ExerciseId: p.ExerciseID, Name: p.Name, Kind: p.Kind, VariantKey: p.VariantKey, Points: points,
		Records: gen.ProgressRecords{
			TopWeightKg: statsNumRecord(r.TopWeightKg), BestE1rmKg: statsNumRecord(r.BestE1RMKg),
			BestVolumeKg: statsNumRecord(r.BestVolumeKg), TopReps: statsCountRecord(r.TopReps),
			BestDurationS: statsCountRecord(r.BestDurationS), BestDistanceM: statsNumRecord(r.BestDistanceM),
		},
	}, nil
}
