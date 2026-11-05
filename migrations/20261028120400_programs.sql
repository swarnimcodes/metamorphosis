-- Splits / programs (push-pull-legs etc.) that workouts can be started from.

create table programs (
    id             uuid primary key default uuidv7(),
    user_id        uuid not null references users (id) on delete cascade,
    name           text not null,
    description    text,
    visibility     visibility not null default 'private',
    forked_from_id uuid references programs (id) on delete set null,
    created_at     timestamptz not null default now(),
    updated_at     timestamptz not null default now(),
    deleted_at     timestamptz
);
create index programs_user_idx on programs (user_id) where deleted_at is null;
comment on table programs is 'A split, e.g. "Push / Pull / Legs". forked_from_id supports copying a friend''s program later.';
create trigger programs_set_updated_at before update on programs
    for each row execute function set_updated_at();

create table program_days (
    id         uuid primary key default uuidv7(),
    program_id uuid not null references programs (id) on delete cascade,
    position   integer not null,
    name       text not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    deleted_at timestamptz
);
create index program_days_program_idx on program_days (program_id, position) where deleted_at is null;
comment on column program_days.position is 'Order within the program. Not unique-constrained, so reordering never needs deferred checks.';
create trigger program_days_set_updated_at before update on program_days
    for each row execute function set_updated_at();

create table program_day_exercises (
    id             uuid primary key default uuidv7(),
    program_day_id uuid not null references program_days (id) on delete cascade,
    exercise_id    uuid not null references exercises (id),
    position       integer not null,
    variant_key    text not null default '',
    target_sets    integer check (target_sets > 0),
    rep_min        integer check (rep_min > 0),
    rep_max        integer check (rep_max > 0),
    notes          text,
    created_at     timestamptz not null default now(),
    updated_at     timestamptz not null default now(),
    deleted_at     timestamptz,
    check (rep_min is null or rep_max is null or rep_min <= rep_max)
);
create index program_day_exercises_day_idx on program_day_exercises (program_day_id, position) where deleted_at is null;
create index program_day_exercises_exercise_idx on program_day_exercises (exercise_id);
comment on column program_day_exercises.variant_key is 'Maintained by trigger from program_day_exercise_tags. See workout_exercises.variant_key.';
create trigger program_day_exercises_set_updated_at before update on program_day_exercises
    for each row execute function set_updated_at();

create table program_day_exercise_tags (
    program_day_exercise_id uuid not null references program_day_exercises (id) on delete cascade,
    tag_id                  uuid not null references variation_tags (id),
    primary key (program_day_exercise_id, tag_id)
);
create index program_day_exercise_tags_tag_idx on program_day_exercise_tags (tag_id);

create function refresh_program_day_exercise_variant_key() returns trigger
language plpgsql as $$
declare
    target uuid := coalesce(new.program_day_exercise_id, old.program_day_exercise_id);
begin
    update program_day_exercises
       set variant_key = coalesce((
               select string_agg(t.slug, '+' order by t.slug)
                 from program_day_exercise_tags x
                 join variation_tags t on t.id = x.tag_id
                where x.program_day_exercise_id = target), '')
     where id = target;
    return null;
end;
$$;
create trigger program_day_exercise_tags_variant_key
    after insert or delete on program_day_exercise_tags
    for each row execute function refresh_program_day_exercise_variant_key();

create table program_enrollments (
    id         uuid primary key default uuidv7(),
    user_id    uuid not null references users (id) on delete cascade,
    program_id uuid not null references programs (id) on delete cascade,
    started_at timestamptz not null default now(),
    ended_at   timestamptz,
    check (ended_at is null or ended_at >= started_at)
);
create index program_enrollments_program_idx on program_enrollments (program_id);
create unique index program_enrollments_one_active_idx on program_enrollments (user_id) where ended_at is null;
comment on table program_enrollments is 'Which program a user is currently following. At most one active row per user.';
