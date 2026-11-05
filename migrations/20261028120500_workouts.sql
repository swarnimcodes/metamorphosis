-- Workout logging: workouts -> exercises (with stacked tags) -> sets.

create type workout_source as enum ('manual', 'excel_import');
create type set_type as enum ('working', 'warmup', 'drop', 'failure');

create table workouts (
    id             uuid primary key default uuidv7(),
    user_id        uuid not null references users (id) on delete cascade,
    title          text,
    gym_id         uuid references gyms (id) on delete set null,
    program_day_id uuid references program_days (id) on delete set null,
    started_at     timestamptz not null,
    ended_at       timestamptz,
    notes          text,
    visibility     visibility not null default 'private',
    source         workout_source not null default 'manual',
    external_id    text,
    created_at     timestamptz not null default now(),
    updated_at     timestamptz not null default now(),
    deleted_at     timestamptz,
    check (ended_at is null or ended_at >= started_at),
    check ((source = 'manual') = (external_id is null))
);
create index workouts_user_started_idx on workouts (user_id, started_at desc) where deleted_at is null;
create index workouts_gym_idx on workouts (gym_id) where gym_id is not null;
create index workouts_program_day_idx on workouts (program_day_id) where program_day_id is not null;
create unique index workouts_import_idempotency_idx on workouts (user_id, source, external_id)
    where external_id is not null;
comment on table workouts is 'One training session. Day bucketing (heatmap, streaks) uses started_at in users.timezone.';
comment on column workouts.external_id is 'Row key from an import source. With source it makes re-running an import idempotent.';
create trigger workouts_set_updated_at before update on workouts
    for each row execute function set_updated_at();

create table workout_exercises (
    id             uuid primary key default uuidv7(),
    workout_id     uuid not null references workouts (id) on delete cascade,
    exercise_id    uuid not null references exercises (id),
    position       integer not null,
    variant_key    text not null default '',
    superset_group integer,
    notes          text,
    created_at     timestamptz not null default now(),
    updated_at     timestamptz not null default now(),
    deleted_at     timestamptz
);
create index workout_exercises_workout_idx on workout_exercises (workout_id, position) where deleted_at is null;
create index workout_exercises_progress_idx on workout_exercises (exercise_id, variant_key) where deleted_at is null;
comment on column workout_exercises.variant_key is
    'Sorted tag slugs joined by "+", e.g. "cable+v-bar"; empty when untagged. Maintained by trigger from workout_exercise_tags. Group by (exercise_id, variant_key) for progress and leaderboards.';
comment on column workout_exercises.superset_group is 'Exercises in one workout sharing the same number are a superset.';
create trigger workout_exercises_set_updated_at before update on workout_exercises
    for each row execute function set_updated_at();

create table workout_exercise_tags (
    workout_exercise_id uuid not null references workout_exercises (id) on delete cascade,
    tag_id              uuid not null references variation_tags (id),
    primary key (workout_exercise_id, tag_id)
);
create index workout_exercise_tags_tag_idx on workout_exercise_tags (tag_id);

create function refresh_workout_exercise_variant_key() returns trigger
language plpgsql as $$
declare
    target uuid := coalesce(new.workout_exercise_id, old.workout_exercise_id);
begin
    update workout_exercises
       set variant_key = coalesce((
               select string_agg(t.slug, '+' order by t.slug)
                 from workout_exercise_tags x
                 join variation_tags t on t.id = x.tag_id
                where x.workout_exercise_id = target), '')
     where id = target;
    return null;
end;
$$;
create trigger workout_exercise_tags_variant_key
    after insert or delete on workout_exercise_tags
    for each row execute function refresh_workout_exercise_variant_key();

create table sets (
    id                  uuid primary key default uuidv7(),
    workout_exercise_id uuid not null references workout_exercises (id) on delete cascade,
    position            integer not null,
    set_type            set_type not null default 'working',
    weight_kg           numeric(7, 3) check (weight_kg >= 0),
    reps                integer check (reps >= 0),
    duration_s          integer check (duration_s >= 0),
    distance_m          numeric(9, 2) check (distance_m >= 0),
    rpe                 numeric(3, 1) check (rpe between 1 and 10),
    note                text,
    completed_at        timestamptz,
    created_at          timestamptz not null default now(),
    updated_at          timestamptz not null default now(),
    deleted_at          timestamptz
);
create index sets_workout_exercise_idx on sets (workout_exercise_id, position) where deleted_at is null;
comment on table sets is 'Individual sets. Which of weight/reps/duration/distance are required depends on exercises.kind; enforced in the API.';
create trigger sets_set_updated_at before update on sets
    for each row execute function set_updated_at();
