package api

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smarniw/metamorphosis/internal/httpx"
)

// Read-only statistics over the caller's logged workouts. Conventions shared by every endpoint:
//   - A "day" is the calendar date in the workout's own timezone (falling back to users.timezone).
//   - A workout only counts if it has at least one logged set.
//   - Warm-up sets are excluded from every number except that rule.
//   - Entries logged against exercises that were merged into another count towards the target.

func (s *Server) statsRoutes(r chi.Router) {
	h := httpx.Handle
	r.MethodFunc("GET", "/stats/consistency", h(s.authed(s.statsConsistency)))
	r.MethodFunc("GET", "/stats/exercises", h(s.authed(s.statsLoggedExercises)))
	r.MethodFunc("GET", "/stats/exercises/{id}/progress", h(s.authed(s.statsProgress)))
}

// statsFloat marshals like serde_json does for an f64: a whole number keeps its ".0" (560.0, not 560).
type statsFloat float64

func (f statsFloat) MarshalJSON() ([]byte, error) {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return []byte("null"), nil
	}
	b := strconv.AppendFloat(nil, v, 'f', -1, 64)
	if !strings.ContainsAny(string(b), ".") {
		b = append(b, '.', '0')
	}
	return b, nil
}

func statsFloatPtr(v *float64) *statsFloat {
	if v == nil {
		return nil
	}
	f := statsFloat(*v)
	return &f
}

func statsRound(value float64, places int) float64 {
	m := math.Pow10(places)
	return math.Round(float64(value*m)) / m
}

func statsDate(t time.Time) string { return t.Format("2006-01-02") }

// statsQuery parses the query string strictly for the named single-valued keys: a duplicated key is an
// error, as it was for axum's Query extractor.
func statsQuery(r *http.Request, keys ...string) (url.Values, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, httpx.BadRequest("Failed to deserialize query string: " + err.Error())
	}
	for _, k := range keys {
		if len(q[k]) > 1 {
			return nil, httpx.BadRequest("Failed to deserialize query string: duplicate field `" + k + "`")
		}
	}
	return q, nil
}

func (s *Server) statsTimezoneOf(r *http.Request, userID uuid.UUID) (string, error) {
	var tz string
	err := s.DB.QueryRow(r.Context(), `select timezone from users where id = $1 and deleted_at is null`, userID).Scan(&tz)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", httpx.Unauthorized()
	}
	return tz, httpx.FromDB(err)
}

// ---- consistency ----

type statsDay struct {
	Date     string     `json:"date"`
	Workouts int64      `json:"workouts"`
	Sets     int64      `json:"sets"`
	VolumeKg statsFloat `json:"volume_kg"`
}

type statsTotals struct {
	Workouts   int64      `json:"workouts"`
	ActiveDays int64      `json:"active_days"`
	Sets       int64      `json:"sets"`
	VolumeKg   statsFloat `json:"volume_kg"`
}

type statsStreak struct {
	CurrentWeeks uint32 `json:"current_weeks"`
	LongestWeeks uint32 `json:"longest_weeks"`
}

type statsConsistencyResponse struct {
	Timezone string      `json:"timezone"`
	From     string      `json:"from"`
	To       string      `json:"to"`
	Days     []statsDay  `json:"days"`
	Totals   statsTotals `json:"totals"`
	Streak   statsStreak `json:"streak"`
}

func statsFloorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// statsWeekID: Mondays share a week id; the Julian day number is 0 on a Monday modulo 7.
func statsWeekID(d time.Time) int64 {
	days := statsFloorDiv(time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC).Unix(), 86400)
	return statsFloorDiv(days+2440588, 7) // Julian day number of 1970-01-01 is 2440588
}

// statsWeekStreaks returns (current, longest) runs of consecutive weeks that contain a workout. The current
// run may start from last week, so a week with nothing logged yet does not break it.
func statsWeekStreaks(active map[int64]bool, thisWeek int64) (uint32, uint32) {
	weeks := make([]int64, 0, len(active))
	for w := range active {
		weeks = append(weeks, w)
	}
	sort.Slice(weeks, func(i, j int) bool { return weeks[i] < weeks[j] })
	var longest, run uint32
	var prev int64
	have := false
	for _, w := range weeks {
		if have && prev == w-1 {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
		prev, have = w, true
	}
	var start int64
	switch {
	case active[thisWeek]:
		start = thisWeek
	case active[thisWeek-1]:
		start = thisWeek - 1
	default:
		return 0, longest
	}
	var current uint32
	for active[start-int64(current)] {
		current++
	}
	return current, longest
}

func (s *Server) statsConsistency(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	q, err := statsQuery(r, "weeks")
	if err != nil {
		return err
	}
	var weeksParam int64 = 53
	if vs, ok := q["weeks"]; ok {
		weeksParam, err = strconv.ParseInt(vs[0], 10, 64)
		if err != nil {
			return httpx.BadRequest("Failed to deserialize query string: invalid weeks")
		}
	}
	ctx := r.Context()
	tz, err := s.statsTimezoneOf(r, userID)
	if err != nil {
		return err
	}
	weeks := min(max(weeksParam, 1), 104)

	var today time.Time
	if err := s.DB.QueryRow(ctx, `select (now() at time zone $1::text)::date`, tz).Scan(&today); err != nil {
		return httpx.FromDB(err)
	}
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	from := monday.AddDate(0, 0, -7*int(weeks-1))

	rows, err := s.DB.Query(ctx,
		`with w as (
		       select id, (started_at at time zone coalesce(timezone, $2::text))::date as d
		         from workouts
		        where user_id = $1 and deleted_at is null
		          and exists (select 1 from workout_exercises we join sets s on s.workout_exercise_id = we.id
		                       where we.workout_id = workouts.id and we.deleted_at is null and s.deleted_at is null)
		   )
		   select w.d, count(*),
		          coalesce(sum(st.sets), 0)::bigint,
		          coalesce(sum(st.volume), 0)::float8
		     from w
		     left join lateral (
		          select count(*) as sets, sum(coalesce(s.weight_kg, 0) * coalesce(s.reps, 0)) as volume
		            from workout_exercises we
		            join sets s on s.workout_exercise_id = we.id
		           where we.workout_id = w.id and we.deleted_at is null
		             and s.deleted_at is null and s.set_type <> 'warmup'
		     ) st on true
		    where w.d between $3::date and $4::date
		    group by w.d
		    order by w.d`, userID, tz, from, today)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer rows.Close()
	days := []statsDay{}
	var totals statsTotals
	volSum := math.Copysign(0, -1) // an empty f64 sum is -0.0 in Rust, and serde_json prints it as such
	for rows.Next() {
		var d time.Time
		var workouts, sets int64
		var vol float64
		if err := rows.Scan(&d, &workouts, &sets, &vol); err != nil {
			return httpx.FromDB(err)
		}
		v := statsRound(vol, 1)
		days = append(days, statsDay{statsDate(d), workouts, sets, statsFloat(v)})
		totals.Workouts += workouts
		totals.Sets += sets
		volSum += v
	}
	if err := rows.Err(); err != nil {
		return httpx.FromDB(err)
	}
	rows.Close()
	totals.ActiveDays = int64(len(days))
	totals.VolumeKg = statsFloat(statsRound(volSum, 1))

	// The longest streak is over all history, not just the window above.
	wr, err := s.DB.Query(ctx,
		`select distinct (w.started_at at time zone coalesce(w.timezone, $2::text))::date
		     from workouts w
		    where w.user_id = $1 and w.deleted_at is null
		      and exists (select 1 from workout_exercises we join sets s on s.workout_exercise_id = we.id
		                   where we.workout_id = w.id and we.deleted_at is null and s.deleted_at is null)`, userID, tz)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer wr.Close()
	active := map[int64]bool{}
	for wr.Next() {
		var d time.Time
		if err := wr.Scan(&d); err != nil {
			return httpx.FromDB(err)
		}
		active[statsWeekID(d)] = true
	}
	if err := wr.Err(); err != nil {
		return httpx.FromDB(err)
	}
	cur, longest := statsWeekStreaks(active, statsWeekID(today))

	httpx.WriteJSON(w, http.StatusOK, statsConsistencyResponse{
		Timezone: tz, From: statsDate(from), To: statsDate(today),
		Days: days, Totals: totals, Streak: statsStreak{cur, longest},
	})
	return nil
}

// ---- exercises the user has logged ----

type statsLoggedExercise struct {
	ExerciseID    uuid.UUID `json:"exercise_id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	VariantKey    string    `json:"variant_key"`
	VariantLabel  string    `json:"variant_label"`
	Sessions      int64     `json:"sessions"`
	LastPerformed string    `json:"last_performed"`
}

// statsVariantLabel: "ez-bar+preacher" -> "EZ Bar + Preacher", using the catalog's tag names.
func statsVariantLabel(key string, names map[string]string) string {
	parts := []string{}
	for _, slug := range strings.Split(key, "+") {
		if slug == "" {
			continue
		}
		if n, ok := names[slug]; ok {
			slug = n
		}
		parts = append(parts, slug)
	}
	return strings.Join(parts, " + ")
}

func (s *Server) statsLoggedExercises(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	ctx := r.Context()
	tz, err := s.statsTimezoneOf(r, userID)
	if err != nil {
		return err
	}
	rows, err := s.DB.Query(ctx,
		`select c.id, c.name, c.kind::text,
		          we.variant_key,
		          count(distinct w.id),
		          ((array_agg(w.started_at at time zone coalesce(w.timezone, $2::text) order by w.started_at desc))[1])::date
		     from workout_exercises we
		     join workouts w on w.id = we.workout_id
		     join exercises e on e.id = we.exercise_id
		     join exercises c on c.id = coalesce(e.merged_into_id, e.id)
		    where w.user_id = $1 and w.deleted_at is null and we.deleted_at is null
		      and exists (select 1 from sets s
		                   where s.workout_exercise_id = we.id and s.deleted_at is null
		                     and s.set_type <> 'warmup')
		    group by c.id, c.name, c.kind, we.variant_key
		    order by max(w.started_at) desc, c.name, we.variant_key`, userID, tz)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer rows.Close()
	out := []statsLoggedExercise{}
	for rows.Next() {
		var e statsLoggedExercise
		var last time.Time
		if err := rows.Scan(&e.ExerciseID, &e.Name, &e.Kind, &e.VariantKey, &e.Sessions, &last); err != nil {
			return httpx.FromDB(err)
		}
		e.LastPerformed = statsDate(last)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return httpx.FromDB(err)
	}
	rows.Close()

	names := map[string]string{}
	tr, err := s.DB.Query(ctx, `select slug, name from variation_tags`)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer tr.Close()
	for tr.Next() {
		var slug, name string
		if err := tr.Scan(&slug, &name); err != nil {
			return httpx.FromDB(err)
		}
		names[slug] = name
	}
	if err := tr.Err(); err != nil {
		return httpx.FromDB(err)
	}
	for i := range out {
		out[i].VariantLabel = statsVariantLabel(out[i].VariantKey, names)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// ---- progress for one exercise ----

type statsPoint struct {
	Date           string      `json:"date"`
	WorkoutID      uuid.UUID   `json:"workout_id"`
	Sets           int64       `json:"sets"`
	VolumeKg       *statsFloat `json:"volume_kg"`
	TopWeightKg    *statsFloat `json:"top_weight_kg"`
	BestE1rmKg     *statsFloat `json:"best_e1rm_kg"`
	TopReps        *int32      `json:"top_reps"`
	TotalReps      *int64      `json:"total_reps"`
	BestDurationS  *int32      `json:"best_duration_s"`
	TotalDurationS *int64      `json:"total_duration_s"`
	BestDistanceM  *statsFloat `json:"best_distance_m"`
	TotalDistanceM *statsFloat `json:"total_distance_m"`
}

type statsRecord[T any] struct {
	Value T      `json:"value"`
	Date  string `json:"date"`
}

type statsRecords struct {
	TopWeightKg   *statsRecord[statsFloat] `json:"top_weight_kg"`
	BestE1rmKg    *statsRecord[statsFloat] `json:"best_e1rm_kg"`
	BestVolumeKg  *statsRecord[statsFloat] `json:"best_volume_kg"`
	TopReps       *statsRecord[int32]      `json:"top_reps"`
	BestDurationS *statsRecord[int32]      `json:"best_duration_s"`
	BestDistanceM *statsRecord[statsFloat] `json:"best_distance_m"`
}

type statsProgressResponse struct {
	ExerciseID uuid.UUID    `json:"exercise_id"`
	Name       string       `json:"name"`
	Kind       string       `json:"kind"`
	VariantKey *string      `json:"variant_key"`
	Points     []statsPoint `json:"points"`
	Records    statsRecords `json:"records"`
}

// statsE1RM is the Epley estimate of a one-rep max, trusted only for 1-12 rep sets.
func statsE1RM(weight float64, reps int32) (float64, bool) {
	switch {
	case weight <= 0 || reps < 1 || reps > 12:
		return 0, false
	case reps == 1:
		return weight, true
	}
	return float64(weight * float64(1.0+float64(float64(reps)/30.0))), true
}

type statsSet struct {
	weight    *float64
	reps      *int32
	durationS *int32
	distanceM *float64
}

func statsPointFromSets(date string, workoutID uuid.UUID, sets []statsSet) statsPoint {
	p := statsPoint{Date: date, WorkoutID: workoutID, Sets: int64(len(sets))}
	var maxW, maxE, maxD *float64
	var volSum, distSum float64
	var haveVol, haveDist bool
	var totalReps, totalDur int64
	for _, st := range sets {
		if st.weight != nil {
			if maxW == nil || *st.weight > *maxW {
				v := *st.weight
				maxW = &v
			}
		}
		if st.weight != nil && st.reps != nil {
			prod := float64(*st.weight * float64(*st.reps))
			volSum = float64(volSum + prod)
			haveVol = true
			if e, ok := statsE1RM(*st.weight, *st.reps); ok && (maxE == nil || e > *maxE) {
				maxE = &e
			}
		}
		if st.reps != nil {
			if p.TopReps == nil || *st.reps > *p.TopReps {
				v := *st.reps
				p.TopReps = &v
			}
			totalReps += int64(*st.reps)
			if p.TotalReps == nil {
				p.TotalReps = new(int64)
			}
		}
		if st.durationS != nil {
			if p.BestDurationS == nil || *st.durationS > *p.BestDurationS {
				v := *st.durationS
				p.BestDurationS = &v
			}
			totalDur += int64(*st.durationS)
			if p.TotalDurationS == nil {
				p.TotalDurationS = new(int64)
			}
		}
		if st.distanceM != nil {
			if maxD == nil || *st.distanceM > *maxD {
				v := *st.distanceM
				maxD = &v
			}
			distSum = float64(distSum + *st.distanceM)
			haveDist = true
		}
	}
	round := func(v *float64, places int) *statsFloat {
		if v == nil {
			return nil
		}
		r := statsFloat(statsRound(*v, places))
		return &r
	}
	if haveVol {
		v := statsFloat(statsRound(volSum, 2))
		p.VolumeKg = &v
	}
	p.TopWeightKg = round(maxW, 3)
	p.BestE1rmKg = round(maxE, 2)
	if p.TotalReps != nil {
		*p.TotalReps = totalReps
	}
	if p.TotalDurationS != nil {
		*p.TotalDurationS = totalDur
	}
	p.BestDistanceM = round(maxD, 2)
	if haveDist {
		v := statsFloat(statsRound(distSum, 2))
		p.TotalDistanceM = &v
	}
	return p
}

// statsRecordOf is the highest value across points; the earliest session wins a tie.
func statsRecordOf[T int32 | statsFloat](points []statsPoint, pick func(*statsPoint) *T) *statsRecord[T] {
	var best *statsRecord[T]
	for i := range points {
		v := pick(&points[i])
		if v != nil && (best == nil || *v > best.Value) {
			best = &statsRecord[T]{Value: *v, Date: points[i].Date}
		}
	}
	return best
}

func (s *Server) statsProgress(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	exerciseID, err := pathUUID(r, "id")
	if err != nil {
		return err
	}
	q, err := statsQuery(r, "variant_key")
	if err != nil {
		return err
	}
	var variantKey *string // absent: all variations combined. Present (even empty): exactly that variant.
	if vs, ok := q["variant_key"]; ok {
		variantKey = &vs[0]
	}
	ctx := r.Context()
	tz, err := s.statsTimezoneOf(r, userID)
	if err != nil {
		return err
	}

	// Resolve to the canonical exercise; only official ones and the caller's own are visible.
	var exID uuid.UUID
	var name, kind string
	err = s.DB.QueryRow(ctx,
		`select c.id, c.name, c.kind::text
		     from exercises e
		     join exercises c on c.id = coalesce(e.merged_into_id, e.id)
		    where e.id = $1 and e.deleted_at is null
		      and (e.status = 'official' or e.created_by = $2)`, exerciseID, userID).Scan(&exID, &name, &kind)
	if err != nil {
		return httpx.FromDB(err)
	}

	rows, err := s.DB.Query(ctx,
		`select w.id, (w.started_at at time zone coalesce(w.timezone, $3::text))::date,
		          s.weight_kg::float8, s.reps, s.duration_s, s.distance_m::float8
		     from workout_exercises we
		     join workouts w on w.id = we.workout_id
		     join sets s on s.workout_exercise_id = we.id
		    where w.user_id = $1 and w.deleted_at is null
		      and we.deleted_at is null and s.deleted_at is null and s.set_type <> 'warmup'
		      and we.exercise_id in (select id from exercises where id = $2 or merged_into_id = $2)
		      and ($4::text is null or we.variant_key = $4)
		    order by w.started_at, w.id, we.position, s.position`, userID, exID, tz, variantKey)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer rows.Close()

	// Rows arrive grouped by workout in time order; fold each group into one point.
	points := []statsPoint{}
	var curID uuid.UUID
	var curDate string
	var cur []statsSet
	for rows.Next() {
		var wid uuid.UUID
		var d time.Time
		var st statsSet
		if err := rows.Scan(&wid, &d, &st.weight, &st.reps, &st.durationS, &st.distanceM); err != nil {
			return httpx.FromDB(err)
		}
		if cur != nil && wid == curID {
			cur = append(cur, st)
			continue
		}
		if cur != nil {
			points = append(points, statsPointFromSets(curDate, curID, cur))
		}
		curID, curDate, cur = wid, statsDate(d), []statsSet{st}
	}
	if err := rows.Err(); err != nil {
		return httpx.FromDB(err)
	}
	if cur != nil {
		points = append(points, statsPointFromSets(curDate, curID, cur))
	}

	httpx.WriteJSON(w, http.StatusOK, statsProgressResponse{
		ExerciseID: exID, Name: name, Kind: kind, VariantKey: variantKey, Points: points,
		Records: statsRecords{
			TopWeightKg:   statsRecordOf(points, func(p *statsPoint) *statsFloat { return p.TopWeightKg }),
			BestE1rmKg:    statsRecordOf(points, func(p *statsPoint) *statsFloat { return p.BestE1rmKg }),
			BestVolumeKg:  statsRecordOf(points, func(p *statsPoint) *statsFloat { return p.VolumeKg }),
			TopReps:       statsRecordOf(points, func(p *statsPoint) *int32 { return p.TopReps }),
			BestDurationS: statsRecordOf(points, func(p *statsPoint) *int32 { return p.BestDurationS }),
			BestDistanceM: statsRecordOf(points, func(p *statsPoint) *statsFloat { return p.BestDistanceM }),
		},
	})
	return nil
}
