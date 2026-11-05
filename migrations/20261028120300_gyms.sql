-- Places where workouts happen.

create table gyms (
    id           uuid primary key default uuidv7(),
    name         text not null,
    chain        text,
    address      text,
    city         text,
    country_code char(2),
    lat          double precision check (lat between -90 and 90),
    lng          double precision check (lng between -180 and 180),
    verified     boolean not null default false,
    created_by   uuid references users (id) on delete set null,
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now(),
    deleted_at   timestamptz,
    check ((lat is null) = (lng is null))
);
create index gyms_name_trgm_idx on gyms using gin (name gin_trgm_ops);
create index gyms_city_idx on gyms (city);
comment on table gyms is 'Shared gym directory, e.g. "Golds Gym, Aundh". Plain lat/lng for now; PostGIS only if "gyms near me" needs it.';

create trigger gyms_set_updated_at before update on gyms
    for each row execute function set_updated_at();

create table user_gyms (
    user_id    uuid not null references users (id) on delete cascade,
    gym_id     uuid not null references gyms (id) on delete cascade,
    is_default boolean not null default false,
    created_at timestamptz not null default now(),
    primary key (user_id, gym_id)
);
create index user_gyms_gym_idx on user_gyms (gym_id);
create unique index user_gyms_one_default_idx on user_gyms (user_id) where is_default;
comment on table user_gyms is 'A user''s saved gyms. Doubles as gym membership for later social features.';
