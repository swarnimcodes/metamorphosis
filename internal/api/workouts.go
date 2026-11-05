package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smarniw/metamorphosis/internal/httpx"
)

func (s *Server) workoutsRoutes(mux *http.ServeMux) {
	h := httpx.Handle
	mux.HandleFunc("POST /workouts", h(s.authed(s.workoutsCreate)))
	mux.HandleFunc("GET /workouts", h(s.authed(s.workoutsList)))
	mux.HandleFunc("GET /workouts/{id}", h(s.authed(s.workoutsDetail)))
	mux.HandleFunc("PATCH /workouts/{id}", h(s.authed(s.workoutsUpdate)))
	mux.HandleFunc("DELETE /workouts/{id}", h(s.authed(s.workoutsRemove)))
	mux.HandleFunc("POST /workouts/{id}/finish", h(s.authed(s.workoutsFinish)))
	mux.HandleFunc("POST /workouts/{id}/exercises", h(s.authed(s.workoutsAddExercise)))
	mux.HandleFunc("GET /exercises/{id}/last", h(s.authed(s.workoutsLastPerformance)))
	mux.HandleFunc("DELETE /workout-exercises/{id}", h(s.authed(s.workoutsRemoveExercise)))
	mux.HandleFunc("POST /workout-exercises/{id}/sets", h(s.authed(s.workoutsAddSet)))
	mux.HandleFunc("PATCH /sets/{id}", h(s.authed(s.workoutsUpdateSet)))
	mux.HandleFunc("DELETE /sets/{id}", h(s.authed(s.workoutsRemoveSet)))
}

// Every query below filters by the caller's user_id (directly or through the workout), so a valid id belonging
// to someone else is indistinguishable from a missing one and yields 404.

type workoutsTagOut struct {
	ID       uuid.UUID `json:"id"`
	Slug     string    `json:"slug"`
	Name     string    `json:"name"`
	Category string    `json:"category"`
}

type workoutsSetOut struct {
	ID          uuid.UUID   `json:"id"`
	Position    int32       `json:"position"`
	SetType     string      `json:"set_type"`
	WeightKg    *statsFloat `json:"weight_kg"`
	Reps        *int32      `json:"reps"`
	DurationS   *int32      `json:"duration_s"`
	DistanceM   *statsFloat `json:"distance_m"`
	Rir         *int16      `json:"rir"`
	Note        *string     `json:"note"`
	CompletedAt *time.Time  `json:"completed_at"`
}

// workoutsSetColumns is the select list (optionally prefixed with an alias) that workoutsScanSet reads.
func workoutsSetColumns(p string) string {
	return p + `id, ` + p + `position, ` + p + `set_type::text, ` + p + `weight_kg::float8, ` + p + `reps, ` +
		p + `duration_s, ` + p + `distance_m::float8, ` + p + `rir, ` + p + `note, ` + p + `completed_at`
}

func workoutsScanSet(row pgx.Row) (workoutsSetOut, error) {
	var o workoutsSetOut
	err := row.Scan(&o.ID, &o.Position, &o.SetType, &o.WeightKg, &o.Reps, &o.DurationS, &o.DistanceM, &o.Rir,
		&o.Note, &o.CompletedAt)
	o.CompletedAt = workoutsUTC(o.CompletedAt)
	return o, err
}

// workoutsUTC makes pgx's local-zone timestamps render as `Z`, as the Rust API did.
func workoutsUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

type workoutsExerciseOut struct {
	ID            uuid.UUID        `json:"id"`
	ExerciseID    uuid.UUID        `json:"exercise_id"`
	ExerciseName  string           `json:"exercise_name"`
	Kind          string           `json:"kind"`
	Position      int32            `json:"position"`
	VariantKey    string           `json:"variant_key"`
	SupersetGroup *int32           `json:"superset_group"`
	Notes         *string          `json:"notes"`
	Tags          []workoutsTagOut `json:"tags"`
	Sets          []workoutsSetOut `json:"sets"`
}

// workoutsGymRef is the gym a workout was logged at, so clients never need a second lookup for its name.
type workoutsGymRef struct {
	ID      uuid.UUID   `json:"id"`
	Name    string      `json:"name"`
	Area    *string     `json:"area"`
	City    *string     `json:"city"`
	Lat     *statsFloat `json:"lat"`
	Lng     *statsFloat `json:"lng"`
	MapsURL *string     `json:"maps_url"`
}

// workoutsGymJoin holds the gym columns of a left-joined row, any of which is null when there is no gym.
type workoutsGymJoin struct {
	name, area, city, mapsURL *string
	lat, lng                  *float64
}

// ref: a soft-deleted gym keeps its name in history.
func (j workoutsGymJoin) ref(gymID *uuid.UUID) *workoutsGymRef {
	if gymID == nil || j.name == nil {
		return nil
	}
	return &workoutsGymRef{*gymID, *j.name, j.area, j.city, statsFloatPtr(j.lat), statsFloatPtr(j.lng), j.mapsURL}
}

type workoutsOut struct {
	ID        uuid.UUID             `json:"id"`
	Title     *string               `json:"title"`
	GymID     *uuid.UUID            `json:"gym_id"`
	Gym       *workoutsGymRef       `json:"gym"`
	StartedAt time.Time             `json:"started_at"`
	EndedAt   *time.Time            `json:"ended_at"`
	Timezone  string                `json:"timezone"`
	Notes     *string               `json:"notes"`
	Exercises []workoutsExerciseOut `json:"exercises"`
}

type workoutsSummary struct {
	ID            uuid.UUID       `json:"id"`
	Title         *string         `json:"title"`
	GymID         *uuid.UUID      `json:"gym_id"`
	Gym           *workoutsGymRef `json:"gym"`
	StartedAt     time.Time       `json:"started_at"`
	EndedAt       *time.Time      `json:"ended_at"`
	Timezone      string          `json:"timezone"`
	ExerciseCount int64           `json:"exercise_count"`
	SetCount      int64           `json:"set_count"`
}

func (s *Server) workoutsLoad(ctx context.Context, userID, id uuid.UUID) (workoutsOut, error) {
	var w workoutsOut
	var g workoutsGymJoin
	err := s.DB.QueryRow(ctx,
		`select w.id, w.title, w.gym_id, g.name, g.area, g.city, g.lat, g.lng, g.maps_url,
		        w.started_at, w.ended_at, w.notes,
		        coalesce(w.timezone, u.timezone)
		   from workouts w
		   join users u on u.id = w.user_id
		   left join gyms g on g.id = w.gym_id
		  where w.id = $1 and w.user_id = $2 and w.deleted_at is null`, id, userID).
		Scan(&w.ID, &w.Title, &w.GymID, &g.name, &g.area, &g.city, &g.lat, &g.lng, &g.mapsURL,
			&w.StartedAt, &w.EndedAt, &w.Notes, &w.Timezone)
	if err != nil {
		return w, httpx.FromDB(err)
	}
	w.Gym = g.ref(w.GymID)
	w.StartedAt = w.StartedAt.UTC()
	w.EndedAt = workoutsUTC(w.EndedAt)

	w.Exercises = []workoutsExerciseOut{}
	byID := map[uuid.UUID]*workoutsExerciseOut{}
	rows, err := s.DB.Query(ctx,
		`select we.id, we.exercise_id, e.name, e.kind::text, we.position,
		        we.variant_key, we.superset_group, we.notes
		   from workout_exercises we
		   join exercises e on e.id = we.exercise_id
		  where we.workout_id = $1 and we.deleted_at is null
		  order by we.position, we.created_at`, id)
	if err != nil {
		return w, httpx.FromDB(err)
	}
	for rows.Next() {
		e := workoutsExerciseOut{Tags: []workoutsTagOut{}, Sets: []workoutsSetOut{}}
		if err := rows.Scan(&e.ID, &e.ExerciseID, &e.ExerciseName, &e.Kind, &e.Position, &e.VariantKey,
			&e.SupersetGroup, &e.Notes); err != nil {
			rows.Close()
			return w, httpx.FromDB(err)
		}
		w.Exercises = append(w.Exercises, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return w, httpx.FromDB(err)
	}
	for i := range w.Exercises {
		byID[w.Exercises[i].ID] = &w.Exercises[i]
	}

	rows, err = s.DB.Query(ctx,
		`select wt.workout_exercise_id, t.id, t.slug, t.name, t.category::text
		   from workout_exercise_tags wt
		   join variation_tags t on t.id = wt.tag_id
		   join workout_exercises we on we.id = wt.workout_exercise_id
		  where we.workout_id = $1
		  order by t.slug`, id)
	if err != nil {
		return w, httpx.FromDB(err)
	}
	for rows.Next() {
		var weID uuid.UUID
		var t workoutsTagOut
		if err := rows.Scan(&weID, &t.ID, &t.Slug, &t.Name, &t.Category); err != nil {
			rows.Close()
			return w, httpx.FromDB(err)
		}
		if e := byID[weID]; e != nil {
			e.Tags = append(e.Tags, t)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return w, httpx.FromDB(err)
	}

	rows, err = s.DB.Query(ctx,
		`select s.workout_exercise_id, `+workoutsSetColumns("s.")+`
		   from sets s
		   join workout_exercises we on we.id = s.workout_exercise_id
		  where we.workout_id = $1 and we.deleted_at is null and s.deleted_at is null
		  order by s.position, s.created_at`, id)
	if err != nil {
		return w, httpx.FromDB(err)
	}
	for rows.Next() {
		var weID uuid.UUID
		var o workoutsSetOut
		if err := rows.Scan(&weID, &o.ID, &o.Position, &o.SetType, &o.WeightKg, &o.Reps, &o.DurationS,
			&o.DistanceM, &o.Rir, &o.Note, &o.CompletedAt); err != nil {
			rows.Close()
			return w, httpx.FromDB(err)
		}
		o.CompletedAt = workoutsUTC(o.CompletedAt)
		if e := byID[weID]; e != nil {
			e.Sets = append(e.Sets, o)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return w, httpx.FromDB(err)
	}
	return w, nil
}

func (s *Server) workoutsLastPerformance(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	exerciseID, err := pathUUID(r, "id")
	if err != nil {
		return err
	}
	variantKey := r.URL.Query().Get("variant_key")
	ctx := r.Context()
	var weID, workoutID uuid.UUID
	var startedAt time.Time
	// The caller's most recent logged sets for an exercise with this exact variant, used to prefill a new set.
	// A merged exercise resolves to its target, and entries logged against the merged-away duplicates still count.
	err = s.DB.QueryRow(ctx,
		`with recursive chain as (
		     select id, merged_into_id from exercises where id = $1
		     union all
		     select e.id, e.merged_into_id from exercises e join chain c on e.id = c.merged_into_id
		 ), target as (
		     select id from chain where merged_into_id is null
		 )
		 select we.id, w.id, w.started_at
		   from workout_exercises we
		   join workouts w on w.id = we.workout_id
		  where w.user_id = $2 and w.deleted_at is null and we.deleted_at is null
		    and we.variant_key = $3
		    and (we.exercise_id in (select id from target)
		         or we.exercise_id in (select id from exercises
		                                where merged_into_id in (select id from target)))
		  order by w.started_at desc, we.created_at desc
		  limit 1`, exerciseID, userID, variantKey).Scan(&weID, &workoutID, &startedAt)
	if err != nil {
		return httpx.FromDB(err)
	}
	sets, err := s.workoutsSets(ctx, weID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, struct {
		WorkoutID uuid.UUID        `json:"workout_id"`
		StartedAt time.Time        `json:"started_at"`
		Sets      []workoutsSetOut `json:"sets"`
	}{workoutID, startedAt.UTC(), sets})
	return nil
}

func (s *Server) workoutsSets(ctx context.Context, weID uuid.UUID) ([]workoutsSetOut, error) {
	rows, err := s.DB.Query(ctx,
		`select `+workoutsSetColumns("")+`
		   from sets
		  where workout_exercise_id = $1 and deleted_at is null
		  order by position, created_at`, weID)
	if err != nil {
		return nil, httpx.FromDB(err)
	}
	defer rows.Close()
	sets := []workoutsSetOut{}
	for rows.Next() {
		o, err := workoutsScanSet(rows)
		if err != nil {
			return nil, httpx.FromDB(err)
		}
		sets = append(sets, o)
	}
	return sets, httpx.FromDB(rows.Err())
}

func (s *Server) workoutsCreate(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	var body struct {
		Title    *string    `json:"title"`
		GymID    *uuid.UUID `json:"gym_id"`
		Notes    *string    `json:"notes"`
		StartAt  *time.Time `json:"started_at"`
		Timezone *string    `json:"timezone"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	ctx := r.Context()
	if body.Timezone != nil {
		known, err := s.knownTimezone(ctx, *body.Timezone)
		if err != nil {
			return err
		}
		if !known {
			return httpx.BadRequest("unknown timezone")
		}
	}
	// No gym given: start where the caller last trained. The join means a gym-less workout never matches, so one
	// in between does not reset it. The time zone is the device's, or failing that the account's: every new
	// workout records the zone it happened in.
	var id uuid.UUID
	err := s.DB.QueryRow(ctx,
		`insert into workouts (user_id, title, gym_id, notes, started_at, timezone)
		 values ($1, $2,
		         coalesce($3::uuid, (select w.gym_id from workouts w
		                        join gyms g on g.id = w.gym_id and g.deleted_at is null
		                       where w.user_id = $1 and w.deleted_at is null
		                       order by w.started_at desc limit 1)),
		         $4, coalesce($5::timestamptz, now()),
		         coalesce($6::text, (select timezone from users where id = $1)))
		 returning id`, userID, body.Title, body.GymID, body.Notes, body.StartAt, body.Timezone).Scan(&id)
	if err != nil {
		return httpx.FromDB(err)
	}
	out, err := s.workoutsLoad(ctx, userID, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
	return nil
}

func (s *Server) workoutsList(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	q := r.URL.Query()
	limit := int64(20)
	if q.Has("limit") {
		v, err := strconv.ParseInt(q.Get("limit"), 10, 64)
		if err != nil {
			return httpx.BadRequest("Failed to deserialize query string: invalid limit")
		}
		limit = v
	}
	limit = min(max(limit, 1), 100)
	var before *time.Time
	if q.Has("before") {
		t, err := time.Parse(time.RFC3339, q.Get("before"))
		if err != nil {
			return httpx.BadRequest("Failed to deserialize query string: invalid before")
		}
		before = &t
	}
	rows, err := s.DB.Query(r.Context(),
		`select w.id, w.title, w.gym_id, g.name, g.area, g.city, g.lat, g.lng, g.maps_url,
		        w.started_at, w.ended_at, coalesce(w.timezone, u.timezone),
		        (select count(*) from workout_exercises we
		          where we.workout_id = w.id and we.deleted_at is null),
		        (select count(*) from sets s
		           join workout_exercises we on we.id = s.workout_exercise_id
		          where we.workout_id = w.id and we.deleted_at is null
		            and s.deleted_at is null)
		   from workouts w
		   join users u on u.id = w.user_id
		   left join gyms g on g.id = w.gym_id
		  where w.user_id = $1 and w.deleted_at is null
		    and ($2::timestamptz is null or w.started_at < $2)
		  order by w.started_at desc
		  limit $3`, userID, before, limit)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer rows.Close()
	out := []workoutsSummary{}
	for rows.Next() {
		var m workoutsSummary
		var g workoutsGymJoin
		if err := rows.Scan(&m.ID, &m.Title, &m.GymID, &g.name, &g.area, &g.city, &g.lat, &g.lng, &g.mapsURL,
			&m.StartedAt, &m.EndedAt, &m.Timezone, &m.ExerciseCount, &m.SetCount); err != nil {
			return httpx.FromDB(err)
		}
		m.Gym = g.ref(m.GymID)
		m.StartedAt = m.StartedAt.UTC()
		m.EndedAt = workoutsUTC(m.EndedAt)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return httpx.FromDB(err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) workoutsDetail(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	id, err := pathUUID(r, "id")
	if err != nil {
		return err
	}
	out, err := s.workoutsLoad(r.Context(), userID, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// Omitted (or null) fields are left unchanged; a field cannot be cleared back to null.
func (s *Server) workoutsUpdate(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	id, err := pathUUID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		Title     *string    `json:"title"`
		Notes     *string    `json:"notes"`
		GymID     *uuid.UUID `json:"gym_id"`
		StartedAt *time.Time `json:"started_at"`
		EndedAt   *time.Time `json:"ended_at"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	ctx := r.Context()
	var got uuid.UUID
	err = s.DB.QueryRow(ctx,
		`update workouts
		    set title = coalesce($3, title),
		        notes = coalesce($4, notes),
		        gym_id = coalesce($5, gym_id),
		        started_at = coalesce($6, started_at),
		        ended_at = coalesce($7, ended_at)
		  where id = $1 and user_id = $2 and deleted_at is null
		returning id`, id, userID, body.Title, body.Notes, body.GymID, body.StartedAt, body.EndedAt).Scan(&got)
	if err != nil {
		return httpx.FromDB(err)
	}
	out, err := s.workoutsLoad(ctx, userID, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// workoutsFinish marks the workout as ended now. Finishing twice keeps the first end time.
func (s *Server) workoutsFinish(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	id, err := pathUUID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	var got uuid.UUID
	err = s.DB.QueryRow(ctx,
		`update workouts set ended_at = coalesce(ended_at, now())
		  where id = $1 and user_id = $2 and deleted_at is null
		returning id`, id, userID).Scan(&got)
	if err != nil {
		return httpx.FromDB(err)
	}
	out, err := s.workoutsLoad(ctx, userID, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) workoutsRemove(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	id, err := pathUUID(r, "id")
	if err != nil {
		return err
	}
	var got uuid.UUID
	err = s.DB.QueryRow(r.Context(),
		`update workouts set deleted_at = now()
		  where id = $1 and user_id = $2 and deleted_at is null
		returning id`, id, userID).Scan(&got)
	if err != nil {
		return httpx.FromDB(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) workoutsAddExercise(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	workoutID, err := pathUUID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		ExerciseID    *uuid.UUID  `json:"exercise_id"`
		TagIDs        []uuid.UUID `json:"tag_ids"`
		SupersetGroup *int32      `json:"superset_group"`
		Notes         *string     `json:"notes"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.ExerciseID == nil {
		return httpx.BadRequest("missing field `exercise_id`")
	}
	if body.TagIDs == nil {
		body.TagIDs = []uuid.UUID{}
	}
	ctx := r.Context()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock the workout row so concurrent adds get distinct positions.
	var locked uuid.UUID
	if err := tx.QueryRow(ctx,
		`select id from workouts where id = $1 and user_id = $2 and deleted_at is null for update`,
		workoutID, userID).Scan(&locked); err != nil {
		return httpx.FromDB(err)
	}

	// Follow a merge pointer so history lands on the canonical exercise.
	var exerciseID uuid.UUID
	err = tx.QueryRow(ctx,
		`select coalesce(merged_into_id, id) from exercises
		  where id = $1 and deleted_at is null
		    and (status = 'official' or created_by = $2)`, *body.ExerciseID, userID).Scan(&exerciseID)
	if err == pgx.ErrNoRows {
		return httpx.BadRequest("unknown exercise")
	}
	if err != nil {
		return httpx.FromDB(err)
	}

	var position int32
	if err := tx.QueryRow(ctx,
		`select (coalesce(max(position), 0) + 1)::int4 from workout_exercises
		  where workout_id = $1 and deleted_at is null`, workoutID).Scan(&position); err != nil {
		return httpx.FromDB(err)
	}

	var weID uuid.UUID
	if err := tx.QueryRow(ctx,
		`insert into workout_exercises (workout_id, exercise_id, position, superset_group, notes)
		 values ($1, $2, $3, $4, $5) returning id`,
		workoutID, exerciseID, position, body.SupersetGroup, body.Notes).Scan(&weID); err != nil {
		return httpx.FromDB(err)
	}

	// The tag trigger recomputes variant_key for each inserted row.
	if _, err := tx.Exec(ctx,
		`insert into workout_exercise_tags (workout_exercise_id, tag_id)
		 select $1, unnest($2::uuid[]) on conflict do nothing`, weID, body.TagIDs); err != nil {
		return httpx.FromDB(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return httpx.FromDB(err)
	}

	workout, err := s.workoutsLoad(ctx, userID, workoutID)
	if err != nil {
		return err
	}
	for _, e := range workout.Exercises {
		if e.ID == weID {
			httpx.WriteJSON(w, http.StatusCreated, e)
			return nil
		}
	}
	return httpx.NotFound()
}

func (s *Server) workoutsRemoveExercise(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	id, err := pathUUID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var got uuid.UUID
	if err := tx.QueryRow(ctx,
		`update workout_exercises we set deleted_at = now()
		   from workouts w
		  where we.id = $1 and w.id = we.workout_id and w.user_id = $2
		    and w.deleted_at is null and we.deleted_at is null
		returning we.id`, id, userID).Scan(&got); err != nil {
		return httpx.FromDB(err)
	}
	if _, err := tx.Exec(ctx,
		`update sets set deleted_at = now() where workout_exercise_id = $1 and deleted_at is null`, id); err != nil {
		return httpx.FromDB(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return httpx.FromDB(err)
	}
	httpx.NoContent(w)
	return nil
}

// workoutsSetType validates an optional set_type; serde rejected unknown variants with 400.
func workoutsSetType(v *string) error {
	if v == nil {
		return nil
	}
	switch *v {
	case "working", "warmup", "drop", "failure":
		return nil
	}
	return httpx.BadRequest("unknown variant `" + *v + "`, expected one of `working`, `warmup`, `drop`, `failure`")
}

func (s *Server) workoutsAddSet(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	weID, err := pathUUID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		SetType     *string    `json:"set_type"`
		WeightKg    *float64   `json:"weight_kg"`
		Reps        *int32     `json:"reps"`
		DurationS   *int32     `json:"duration_s"`
		DistanceM   *float64   `json:"distance_m"`
		Rir         *int16     `json:"rir"`
		Note        *string    `json:"note"`
		CompletedAt *time.Time `json:"completed_at"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if err := workoutsSetType(body.SetType); err != nil {
		return err
	}
	setType := "working"
	if body.SetType != nil {
		setType = *body.SetType
	}
	ctx := r.Context()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var locked uuid.UUID
	if err := tx.QueryRow(ctx,
		`select we.id from workout_exercises we
		   join workouts w on w.id = we.workout_id
		  where we.id = $1 and w.user_id = $2
		    and w.deleted_at is null and we.deleted_at is null
		    for update of we`, weID, userID).Scan(&locked); err != nil {
		return httpx.FromDB(err)
	}
	var position int32
	if err := tx.QueryRow(ctx,
		`select (coalesce(max(position), 0) + 1)::int4 from sets
		  where workout_exercise_id = $1 and deleted_at is null`, weID).Scan(&position); err != nil {
		return httpx.FromDB(err)
	}
	set, err := workoutsScanSet(tx.QueryRow(ctx,
		`insert into sets (workout_exercise_id, position, set_type, weight_kg, reps,
		                   duration_s, distance_m, rir, note, completed_at)
		 values ($1, $2, $3::set_type, $4::float8, $5, $6, $7::float8, $8, $9, coalesce($10::timestamptz, now()))
		 returning `+workoutsSetColumns(""),
		weID, position, setType, body.WeightKg, body.Reps, body.DurationS, body.DistanceM, body.Rir, body.Note,
		body.CompletedAt))
	if err != nil {
		return httpx.FromDB(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return httpx.FromDB(err)
	}
	httpx.WriteJSON(w, http.StatusCreated, set)
	return nil
}

// workoutsPresentInt16 tells an omitted field (unchanged) from an explicit null (cleared).
type workoutsPresentInt16 struct {
	Present bool
	Val     *int16
}

func (p *workoutsPresentInt16) UnmarshalJSON(b []byte) error {
	p.Present = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		p.Val = nil
		return nil
	}
	var v int16
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.Val = &v
	return nil
}

// Omitted fields are left unchanged; only `rir` can be cleared back to null (send `null`).
func (s *Server) workoutsUpdateSet(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	id, err := pathUUID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		SetType     *string              `json:"set_type"`
		WeightKg    *float64             `json:"weight_kg"`
		Reps        *int32               `json:"reps"`
		DurationS   *int32               `json:"duration_s"`
		DistanceM   *float64             `json:"distance_m"`
		Rir         workoutsPresentInt16 `json:"rir"`
		Note        *string              `json:"note"`
		CompletedAt *time.Time           `json:"completed_at"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if err := workoutsSetType(body.SetType); err != nil {
		return err
	}
	set, err := workoutsScanSet(s.DB.QueryRow(r.Context(),
		`update sets s
		    set set_type = coalesce($3::set_type, s.set_type),
		        weight_kg = coalesce($4::float8::numeric, s.weight_kg),
		        reps = coalesce($5::int4, s.reps),
		        duration_s = coalesce($6::int4, s.duration_s),
		        distance_m = coalesce($7::float8::numeric, s.distance_m),
		        rir = case when $8::bool then $9::int2 else s.rir end,
		        note = coalesce($10::text, s.note),
		        completed_at = coalesce($11::timestamptz, s.completed_at)
		   from workout_exercises we, workouts w
		  where s.id = $1 and we.id = s.workout_exercise_id and w.id = we.workout_id
		    and w.user_id = $2 and w.deleted_at is null
		    and we.deleted_at is null and s.deleted_at is null
		returning `+workoutsSetColumns("s."),
		id, userID, body.SetType, body.WeightKg, body.Reps, body.DurationS, body.DistanceM,
		body.Rir.Present, body.Rir.Val, body.Note, body.CompletedAt))
	if err != nil {
		return httpx.FromDB(err)
	}
	httpx.WriteJSON(w, http.StatusOK, set)
	return nil
}

func (s *Server) workoutsRemoveSet(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	id, err := pathUUID(r, "id")
	if err != nil {
		return err
	}
	var got uuid.UUID
	err = s.DB.QueryRow(r.Context(),
		`update sets s set deleted_at = now()
		   from workout_exercises we, workouts w
		  where s.id = $1 and we.id = s.workout_exercise_id and w.id = we.workout_id
		    and w.user_id = $2 and w.deleted_at is null
		    and we.deleted_at is null and s.deleted_at is null
		returning s.id`, id, userID).Scan(&got)
	if err != nil {
		return httpx.FromDB(err)
	}
	httpx.NoContent(w)
	return nil
}
