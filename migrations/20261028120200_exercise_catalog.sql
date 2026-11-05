-- Canonical exercises, their muscles, aliases and stackable variation tags.

create table muscle_groups (
    id   uuid primary key default uuidv7(),
    slug text not null unique,
    name text not null
);
comment on table muscle_groups is 'Reference list of muscle groups (chest, lats, quads, ...).';

create type exercise_kind as enum (
    'weight_reps',      -- bench press: weight + reps
    'bodyweight_reps',  -- pull-up: reps, optional added weight
    'duration',         -- plank: seconds
    'distance'          -- run / row: metres, optional duration
);
create type exercise_status as enum ('official', 'user', 'pending_review');
create type muscle_role as enum ('primary', 'secondary');

create table exercises (
    id             uuid primary key default uuidv7(),
    slug           text unique,
    name           text not null,
    kind           exercise_kind not null,
    status         exercise_status not null default 'user',
    description    text,
    created_by     uuid references users (id) on delete set null,
    merged_into_id uuid references exercises (id),
    created_at     timestamptz not null default now(),
    updated_at     timestamptz not null default now(),
    deleted_at     timestamptz,
    check (status <> 'official' or slug is not null),
    check (status <> 'user' or created_by is not null),
    check (merged_into_id is null or merged_into_id <> id)
);
create index exercises_name_trgm_idx on exercises using gin (name gin_trgm_ops);
create index exercises_created_by_idx on exercises (created_by);
create index exercises_merged_into_idx on exercises (merged_into_id) where merged_into_id is not null;
comment on table exercises is 'Canonical exercises, e.g. "Lat Pulldown". Variations are tags, not separate exercises.';
comment on column exercises.status is 'official = curated catalog; user = private to created_by; pending_review = user exercise proposed for promotion.';
comment on column exercises.merged_into_id is 'Set when a duplicate is collapsed into another exercise. Readers follow the pointer; history is never rewritten.';

create trigger exercises_set_updated_at before update on exercises
    for each row execute function set_updated_at();

create table exercise_muscles (
    exercise_id     uuid not null references exercises (id) on delete cascade,
    muscle_group_id uuid not null references muscle_groups (id),
    role            muscle_role not null,
    primary key (exercise_id, muscle_group_id)
);
create index exercise_muscles_muscle_idx on exercise_muscles (muscle_group_id);

create table exercise_aliases (
    id               uuid primary key default uuidv7(),
    exercise_id      uuid not null references exercises (id) on delete cascade,
    alias            text not null,
    alias_normalized text generated always as (
        btrim(lower(regexp_replace(alias, '[^[:alnum:]]+', ' ', 'g')))
    ) stored,
    unique (alias_normalized)
);
create index exercise_aliases_exercise_idx on exercise_aliases (exercise_id);
create index exercise_aliases_trgm_idx on exercise_aliases using gin (alias_normalized gin_trgm_ops);
comment on table exercise_aliases is 'Alternate names ("Lat Pulldowns") that resolve to a canonical exercise. Globally unique once normalised.';

create type tag_category as enum ('equipment', 'grip', 'angle', 'stance', 'other');

create table variation_tags (
    id         uuid primary key default uuidv7(),
    slug       text not null unique,
    name       text not null,
    category   tag_category not null default 'other',
    created_at timestamptz not null default now()
);
comment on table variation_tags is 'Stackable modifiers (cable, v-bar, neutral grip). Slugs are immutable: they feed variant_key.';

create table exercise_variation_tags (
    exercise_id uuid not null references exercises (id) on delete cascade,
    tag_id      uuid not null references variation_tags (id) on delete cascade,
    primary key (exercise_id, tag_id)
);
create index exercise_variation_tags_tag_idx on exercise_variation_tags (tag_id);
comment on table exercise_variation_tags is 'Optional: tags suggested in the UI for an exercise. Not a restriction.';
