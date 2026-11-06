package httpapi

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
	"github.com/smarniw/metamorphosis/internal/workout"
)

// workoutsAPI holds the handlers of the workouts operations in api/openapi.yaml.
type workoutsAPI struct{ svc *workout.Service }

func newWorkoutsAPI(d Deps, q *db.Queries) workoutsAPI {
	return workoutsAPI{svc: workout.NewService(q, func(ctx context.Context, fn func(*db.Queries) error) error {
		return db.InTx(ctx, d.DB, fn)
	})}
}

func workoutDec(f *float64) *decimal.Decimal {
	if f == nil {
		return nil
	}
	d := decimal.NewFromFloat(*f)
	return &d
}

func workoutFloat(d *decimal.Decimal) *float64 {
	if d == nil {
		return nil
	}
	f := d.InexactFloat64()
	return &f
}

func workoutGym(g *workout.Gym) *gen.WorkoutGymRef {
	if g == nil {
		return nil
	}
	return &gen.WorkoutGymRef{Id: g.ID, Name: g.Name, Area: g.Area, City: g.City, Lat: g.Lat, Lng: g.Lng, MapsUrl: g.MapsURL}
}

func workoutSet(s workout.Set) gen.WorkoutSet {
	return gen.WorkoutSet{
		Id: s.ID, Position: s.Position, SetType: gen.WorkoutSetType(s.SetType), WeightKg: workoutFloat(s.WeightKg),
		Reps: s.Reps, DurationS: s.DurationS, DistanceM: workoutFloat(s.DistanceM), Rir: s.Rir, Note: s.Note,
		CompletedAt: s.CompletedAt,
	}
}

func workoutSets(in []workout.Set) []gen.WorkoutSet {
	out := make([]gen.WorkoutSet, 0, len(in))
	for _, s := range in {
		out = append(out, workoutSet(s))
	}
	return out
}

func workoutExercise(e workout.Exercise) gen.WorkoutExercise {
	tags := make([]gen.WorkoutTag, 0, len(e.Tags))
	for _, t := range e.Tags {
		tags = append(tags, gen.WorkoutTag{Id: t.ID, Slug: t.Slug, Name: t.Name, Category: t.Category})
	}
	return gen.WorkoutExercise{
		Id: e.ID, ExerciseId: e.ExerciseID, ExerciseName: e.ExerciseName, Kind: e.Kind, Position: e.Position,
		VariantKey: e.VariantKey, SupersetGroup: e.SupersetGroup, Notes: e.Notes, Tags: tags, Sets: workoutSets(e.Sets),
	}
}

func workoutFull(w workout.Workout) gen.Workout {
	exs := make([]gen.WorkoutExercise, 0, len(w.Exercises))
	for _, e := range w.Exercises {
		exs = append(exs, workoutExercise(e))
	}
	return gen.Workout{
		Id: w.ID, Title: w.Title, GymId: w.GymID, Gym: workoutGym(w.Gym), StartedAt: w.StartedAt, EndedAt: w.EndedAt,
		Timezone: w.Timezone, Notes: w.Notes, Exercises: exs,
	}
}

func (a workoutsAPI) CreateWorkout(ctx context.Context, req gen.CreateWorkoutRequestObject) (gen.CreateWorkoutResponseObject, error) {
	b := req.Body
	w, err := a.svc.Create(ctx, userFrom(ctx), workout.CreateInput{
		Title: b.Title, GymID: b.GymId, Notes: b.Notes, StartedAt: b.StartedAt, Timezone: b.Timezone,
	})
	if err != nil {
		return nil, err
	}
	return gen.CreateWorkout201JSONResponse(workoutFull(w)), nil
}

func (a workoutsAPI) ListWorkouts(ctx context.Context, req gen.ListWorkoutsRequestObject) (gen.ListWorkoutsResponseObject, error) {
	rows, err := a.svc.List(ctx, userFrom(ctx), req.Params.Limit, req.Params.Before)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListWorkouts200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, gen.WorkoutSummary{
			Id: r.ID, Title: r.Title, GymId: r.GymID, Gym: workoutGym(r.Gym), StartedAt: r.StartedAt, EndedAt: r.EndedAt,
			Timezone: r.Timezone, ExerciseCount: r.ExerciseCount, SetCount: r.SetCount,
		})
	}
	return out, nil
}

func (a workoutsAPI) GetWorkout(ctx context.Context, req gen.GetWorkoutRequestObject) (gen.GetWorkoutResponseObject, error) {
	w, err := a.svc.Get(ctx, userFrom(ctx), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.GetWorkout200JSONResponse(workoutFull(w)), nil
}

func (a workoutsAPI) UpdateWorkout(ctx context.Context, req gen.UpdateWorkoutRequestObject) (gen.UpdateWorkoutResponseObject, error) {
	b := req.Body
	w, err := a.svc.Update(ctx, userFrom(ctx), req.Id, workout.UpdateInput{
		Title: b.Title, Notes: b.Notes, GymID: b.GymId, StartedAt: b.StartedAt, EndedAt: b.EndedAt,
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateWorkout200JSONResponse(workoutFull(w)), nil
}

func (a workoutsAPI) DeleteWorkout(ctx context.Context, req gen.DeleteWorkoutRequestObject) (gen.DeleteWorkoutResponseObject, error) {
	if err := a.svc.Remove(ctx, userFrom(ctx), req.Id); err != nil {
		return nil, err
	}
	return gen.DeleteWorkout204Response{}, nil
}

func (a workoutsAPI) FinishWorkout(ctx context.Context, req gen.FinishWorkoutRequestObject) (gen.FinishWorkoutResponseObject, error) {
	w, err := a.svc.Finish(ctx, userFrom(ctx), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.FinishWorkout200JSONResponse(workoutFull(w)), nil
}

func (a workoutsAPI) AddWorkoutExercise(ctx context.Context, req gen.AddWorkoutExerciseRequestObject) (gen.AddWorkoutExerciseResponseObject, error) {
	b := req.Body
	in := workout.AddExerciseInput{ExerciseID: b.ExerciseId, SupersetGroup: b.SupersetGroup, Notes: b.Notes}
	if b.TagIds != nil {
		in.TagIDs = append([]uuid.UUID{}, *b.TagIds...)
	}
	e, err := a.svc.AddExercise(ctx, userFrom(ctx), req.Id, in)
	if err != nil {
		return nil, err
	}
	return gen.AddWorkoutExercise201JSONResponse(workoutExercise(e)), nil
}

func (a workoutsAPI) GetLastSession(ctx context.Context, req gen.GetLastSessionRequestObject) (gen.GetLastSessionResponseObject, error) {
	variant := ""
	if req.Params.VariantKey != nil {
		variant = *req.Params.VariantKey
	}
	l, err := a.svc.LastSession(ctx, userFrom(ctx), req.Id, variant)
	if err != nil {
		return nil, err
	}
	return gen.GetLastSession200JSONResponse{WorkoutId: l.WorkoutID, StartedAt: l.StartedAt, Sets: workoutSets(l.Sets)}, nil
}

func (a workoutsAPI) DeleteWorkoutExercise(ctx context.Context, req gen.DeleteWorkoutExerciseRequestObject) (gen.DeleteWorkoutExerciseResponseObject, error) {
	if err := a.svc.RemoveExercise(ctx, userFrom(ctx), req.Id); err != nil {
		return nil, err
	}
	return gen.DeleteWorkoutExercise204Response{}, nil
}

func workoutSetType(t *gen.WorkoutSetType) *string {
	if t == nil {
		return nil
	}
	s := string(*t)
	return &s
}

func (a workoutsAPI) AddSet(ctx context.Context, req gen.AddSetRequestObject) (gen.AddSetResponseObject, error) {
	b := req.Body
	s, err := a.svc.AddSet(ctx, userFrom(ctx), req.Id, workout.SetInput{
		SetType: workoutSetType(b.SetType), WeightKg: workoutDec(b.WeightKg), Reps: b.Reps, DurationS: b.DurationS,
		DistanceM: workoutDec(b.DistanceM), Rir: b.Rir, Note: b.Note, CompletedAt: b.CompletedAt,
	})
	if err != nil {
		return nil, err
	}
	return gen.AddSet201JSONResponse(workoutSet(s)), nil
}

// workoutPatchRir turns the nullable wire value into the service's: omitted, null, or a number.
func workoutPatchRir(n gen.WorkoutSetUpdate) workout.OptionalInt16 {
	if !n.Rir.IsSpecified() {
		return workout.OptionalInt16{}
	}
	if n.Rir.IsNull() {
		return workout.OptionalInt16{Specified: true}
	}
	v := n.Rir.MustGet()
	return workout.OptionalInt16{Specified: true, Value: &v}
}

func (a workoutsAPI) UpdateSet(ctx context.Context, req gen.UpdateSetRequestObject) (gen.UpdateSetResponseObject, error) {
	b := req.Body
	s, err := a.svc.UpdateSet(ctx, userFrom(ctx), req.Id, workout.SetPatch{
		SetType: workoutSetType(b.SetType), WeightKg: workoutDec(b.WeightKg), Reps: b.Reps, DurationS: b.DurationS,
		DistanceM: workoutDec(b.DistanceM), Rir: workoutPatchRir(*b), Note: b.Note, CompletedAt: b.CompletedAt,
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateSet200JSONResponse(workoutSet(s)), nil
}

func (a workoutsAPI) DeleteSet(ctx context.Context, req gen.DeleteSetRequestObject) (gen.DeleteSetResponseObject, error) {
	if err := a.svc.RemoveSet(ctx, userFrom(ctx), req.Id); err != nil {
		return nil, err
	}
	return gen.DeleteSet204Response{}, nil
}
