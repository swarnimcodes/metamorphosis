-- Workouts, the exercises inside them, and sets. Every query filters by the caller's user_id (directly or through
-- the workout), so a valid id belonging to someone else is indistinguishable from a missing one.

-- name: WorkoutTimezoneKnown :one
-- Anything stored and later used in `at time zone` must pass this first.
select exists (select 1 from pg_catalog.pg_timezone_names where name = @name::text) as known;

-- name: WorkoutUserTimezone :one
select timezone from users where id = @user_id;

-- name: WorkoutLastGym :one
-- The gym of the caller's most recent workout that has a live gym: a gym-less workout in between never resets it.
select w.gym_id
  from workouts w
  join gyms g on g.id = w.gym_id and g.deleted_at is null
 where w.user_id = @user_id and w.deleted_at is null
 order by w.started_at desc
 limit 1;

-- name: InsertWorkout :one
insert into workouts (user_id, title, gym_id, notes, started_at, timezone)
values (@user_id, @title, @gym_id, @notes, coalesce(sqlc.narg('started_at')::timestamptz, now()), @timezone)
returning id;

-- name: GetWorkout :one
select w.id, w.title, w.gym_id, g.name as gym_name, g.area as gym_area, g.city as gym_city,
       g.lat as gym_lat, g.lng as gym_lng, g.maps_url as gym_maps_url,
       w.started_at, w.ended_at, w.notes,
       coalesce(w.timezone, u.timezone)::text as timezone
  from workouts w
  join users u on u.id = w.user_id
  left join gyms g on g.id = w.gym_id
 where w.id = @id and w.user_id = @user_id and w.deleted_at is null;

-- name: ListWorkoutExercises :many
select we.id, we.exercise_id, e.name as exercise_name, e.kind, we.position, we.variant_key, we.superset_group, we.notes
  from workout_exercises we
  join exercises e on e.id = we.exercise_id
 where we.workout_id = @workout_id and we.deleted_at is null
 order by we.position, we.created_at;

-- name: ListWorkoutTags :many
select wt.workout_exercise_id, t.id, t.slug, t.name, t.category
  from workout_exercise_tags wt
  join variation_tags t on t.id = wt.tag_id
  join workout_exercises we on we.id = wt.workout_exercise_id
 where we.workout_id = @workout_id
 order by t.slug;

-- name: ListWorkoutSets :many
select s.*
  from sets s
  join workout_exercises we on we.id = s.workout_exercise_id
 where we.workout_id = @workout_id and we.deleted_at is null and s.deleted_at is null
 order by s.position, s.created_at;

-- name: ListWorkoutSummaries :many
select w.id, w.title, w.gym_id, g.name as gym_name, g.area as gym_area, g.city as gym_city,
       g.lat as gym_lat, g.lng as gym_lng, g.maps_url as gym_maps_url,
       w.started_at, w.ended_at, coalesce(w.timezone, u.timezone)::text as timezone,
       (select count(*) from workout_exercises we
         where we.workout_id = w.id and we.deleted_at is null) as exercise_count,
       (select count(*) from sets s
          join workout_exercises we on we.id = s.workout_exercise_id
         where we.workout_id = w.id and we.deleted_at is null and s.deleted_at is null) as set_count
  from workouts w
  join users u on u.id = w.user_id
  left join gyms g on g.id = w.gym_id
 where w.user_id = @user_id and w.deleted_at is null
   and (sqlc.narg('before')::timestamptz is null or w.started_at < sqlc.narg('before'))
 order by w.started_at desc
 limit @max_rows;

-- name: UpdateWorkout :one
-- Omitted (or null) fields are left unchanged; a field cannot be cleared back to null.
update workouts
   set title = coalesce(sqlc.narg('title'), title),
       notes = coalesce(sqlc.narg('notes'), notes),
       gym_id = coalesce(sqlc.narg('gym_id')::uuid, gym_id),
       started_at = coalesce(sqlc.narg('started_at')::timestamptz, started_at),
       ended_at = coalesce(sqlc.narg('ended_at')::timestamptz, ended_at)
 where id = @id and user_id = @user_id and deleted_at is null
returning id;

-- name: FinishWorkout :one
-- Finishing twice keeps the first end time.
update workouts set ended_at = coalesce(ended_at, now())
 where id = @id and user_id = @user_id and deleted_at is null
returning id;

-- name: DeleteWorkout :execrows
update workouts set deleted_at = now()
 where id = @id and user_id = @user_id and deleted_at is null;

-- name: LockWorkout :one
-- Held until the transaction ends, so concurrent adds get distinct positions.
select id from workouts where id = @id and user_id = @user_id and deleted_at is null for update;

-- name: ResolveWorkoutExercise :one
-- Follows a merge pointer so history lands on the canonical exercise.
select coalesce(merged_into_id, id)::uuid as exercise_id from exercises
 where id = @id and deleted_at is null and (status = 'official' or created_by = @user_id::uuid);

-- name: NextWorkoutExercisePosition :one
select (coalesce(max(position), 0) + 1)::int4 as next_position from workout_exercises
 where workout_id = @workout_id and deleted_at is null;

-- name: InsertWorkoutExercise :one
insert into workout_exercises (workout_id, exercise_id, position, superset_group, notes)
values (@workout_id, @exercise_id, @position, @superset_group, @notes)
returning id;

-- name: InsertWorkoutExerciseTags :exec
-- The tag trigger recomputes variant_key for each inserted row.
insert into workout_exercise_tags (workout_exercise_id, tag_id)
select @workout_exercise_id, unnest(@tag_ids::uuid[]) on conflict do nothing;

-- name: DeleteWorkoutExercise :execrows
update workout_exercises we set deleted_at = now()
  from workouts w
 where we.id = @id and w.id = we.workout_id and w.user_id = @user_id
   and w.deleted_at is null and we.deleted_at is null;

-- name: DeleteExerciseSets :exec
update sets set deleted_at = now() where workout_exercise_id = @workout_exercise_id and deleted_at is null;

-- name: LockWorkoutExercise :one
select we.id from workout_exercises we
  join workouts w on w.id = we.workout_id
 where we.id = @id and w.user_id = @user_id and w.deleted_at is null and we.deleted_at is null
   for update of we;

-- name: NextSetPosition :one
select (coalesce(max(position), 0) + 1)::int4 as next_position from sets
 where workout_exercise_id = @workout_exercise_id and deleted_at is null;

-- name: InsertSet :one
insert into sets (workout_exercise_id, position, set_type, weight_kg, reps, duration_s, distance_m, rir, note, completed_at)
values (@workout_exercise_id, @position, @set_type, @weight_kg, @reps, @duration_s, @distance_m, @rir, @note,
        coalesce(sqlc.narg('completed_at')::timestamptz, now()))
returning *;

-- name: UpdateSet :one
-- Omitted or null leaves a field unchanged; only rir can be cleared, and only when rir_set says it was sent.
update sets s
   set set_type = coalesce(sqlc.narg('set_type')::set_type, s.set_type),
       weight_kg = coalesce(sqlc.narg('weight_kg')::numeric, s.weight_kg),
       reps = coalesce(sqlc.narg('reps')::int4, s.reps),
       duration_s = coalesce(sqlc.narg('duration_s')::int4, s.duration_s),
       distance_m = coalesce(sqlc.narg('distance_m')::numeric, s.distance_m),
       rir = case when @rir_set::bool then sqlc.narg('rir')::int2 else s.rir end,
       note = coalesce(sqlc.narg('note')::text, s.note),
       completed_at = coalesce(sqlc.narg('completed_at')::timestamptz, s.completed_at)
  from workout_exercises we, workouts w
 where s.id = @id and we.id = s.workout_exercise_id and w.id = we.workout_id
   and w.user_id = @user_id and w.deleted_at is null
   and we.deleted_at is null and s.deleted_at is null
returning s.*;

-- name: DeleteSet :execrows
update sets s set deleted_at = now()
  from workout_exercises we, workouts w
 where s.id = @id and we.id = s.workout_exercise_id and w.id = we.workout_id
   and w.user_id = @user_id and w.deleted_at is null
   and we.deleted_at is null and s.deleted_at is null;

-- name: LastWorkoutExercise :one
-- The caller's most recent logged exercise with this exact variant, used to prefill a new set. A merged exercise
-- resolves to its target, and entries logged against the merged-away duplicates still count.
with recursive chain as (
    select e.id, e.merged_into_id from exercises e where e.id = @exercise_id
    union all
    select e.id, e.merged_into_id from exercises e join chain c on e.id = c.merged_into_id
), target as (
    select id from chain where merged_into_id is null
)
select we.id as workout_exercise_id, w.id as workout_id, w.started_at
  from workout_exercises we
  join workouts w on w.id = we.workout_id
 where w.user_id = @user_id and w.deleted_at is null and we.deleted_at is null
   and we.variant_key = @variant_key
   and (we.exercise_id in (select id from target)
        or we.exercise_id in (select id from exercises where merged_into_id in (select id from target)))
 order by w.started_at desc, we.created_at desc
 limit 1;

-- name: ListExerciseSets :many
select * from sets where workout_exercise_id = @workout_exercise_id and deleted_at is null order by position, created_at;
