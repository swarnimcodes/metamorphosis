-- Statistics. Conventions shared by every query: a "day" is the calendar date in the workout's own timezone (falling
-- back to the account's), a workout only counts if it has a logged set, and warm-up sets are left out of every number.
-- The rules (windows, streaks, records) live in internal/stats; these only fetch.

-- name: StatsUserTimezone :one
select timezone from users where id = @user_id and deleted_at is null;

-- name: StatsToday :one
select (now() at time zone @tz::text)::date as today;

-- name: StatsWorkoutDays :many
-- Every local day with a counted workout, over all history (the longest streak needs all of it).
select distinct (w.started_at at time zone coalesce(w.timezone, @tz::text))::date as day
  from workouts w
 where w.user_id = @user_id and w.deleted_at is null
   and exists (select 1 from workout_exercises we join sets s on s.workout_exercise_id = we.id
                where we.workout_id = w.id and we.deleted_at is null and s.deleted_at is null)
 order by 1;

-- name: StatsWorkoutsInWindow :many
-- One row per counted workout in the window, with its working-set count and volume.
select w.id as workout_id,
       (w.started_at at time zone coalesce(w.timezone, @tz::text))::date as day,
       count(s.id) filter (where s.set_type <> 'warmup') as sets,
       coalesce(sum(coalesce(s.weight_kg, 0) * coalesce(s.reps, 0)) filter (where s.set_type <> 'warmup'), 0)::numeric as volume
  from workouts w
  join workout_exercises we on we.workout_id = w.id and we.deleted_at is null
  join sets s on s.workout_exercise_id = we.id and s.deleted_at is null
 where w.user_id = @user_id and w.deleted_at is null
   and (w.started_at at time zone coalesce(w.timezone, @tz::text))::date between @from_day::date and @to_day::date
 group by w.id, w.started_at, w.timezone
 order by w.started_at, w.id;

-- name: StatsLoggedExercises :many
-- Exercises with a logged working set, grouped by variant, merged duplicates counted under their target.
select c.id as exercise_id, c.name, c.kind, we.variant_key,
       count(distinct w.id) as sessions,
       ((array_agg(w.started_at at time zone coalesce(w.timezone, @tz::text) order by w.started_at desc))[1])::date as last_performed
  from workout_exercises we
  join workouts w on w.id = we.workout_id
  join exercises e on e.id = we.exercise_id
  join exercises c on c.id = coalesce(e.merged_into_id, e.id)
 where w.user_id = @user_id and w.deleted_at is null and we.deleted_at is null
   and exists (select 1 from sets s
                where s.workout_exercise_id = we.id and s.deleted_at is null and s.set_type <> 'warmup')
 group by c.id, c.name, c.kind, we.variant_key
 order by max(w.started_at) desc, c.name, we.variant_key;

-- name: StatsVariationTagNames :many
select slug, name from variation_tags;

-- name: StatsResolveExercise :one
-- The canonical exercise behind an id; only official exercises and the caller's own are visible.
select c.id, c.name, c.kind
  from exercises e
  join exercises c on c.id = coalesce(e.merged_into_id, e.id)
 where e.id = @id and e.deleted_at is null
   and (e.status = 'official' or e.created_by = @user_id);

-- name: StatsExerciseSets :many
-- The working sets of one exercise (merged duplicates included), grouped by workout in time order.
select w.id as workout_id,
       (w.started_at at time zone coalesce(w.timezone, @tz::text))::date as day,
       s.weight_kg, s.reps, s.duration_s, s.distance_m
  from workout_exercises we
  join workouts w on w.id = we.workout_id
  join sets s on s.workout_exercise_id = we.id
 where w.user_id = @user_id and w.deleted_at is null
   and we.deleted_at is null and s.deleted_at is null and s.set_type <> 'warmup'
   and we.exercise_id in (select x.id from exercises x where x.id = @exercise_id or x.merged_into_id = @exercise_id)
   and (sqlc.narg('variant_key')::text is null or we.variant_key = sqlc.narg('variant_key'))
 order by w.started_at, w.id, we.position, s.position;
