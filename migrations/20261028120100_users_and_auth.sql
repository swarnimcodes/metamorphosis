-- Users, OAuth identities and API refresh tokens.

create table users (
    id           uuid primary key default uuidv7(),
    email        citext not null unique,
    username     citext not null unique
                 check (username ~ '^[a-z0-9_]{3,30}$'),
    display_name text not null,
    avatar_url   text,
    unit_system  unit_system not null default 'metric',
    timezone     text not null default 'UTC',
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now(),
    deleted_at   timestamptz
);
comment on table users is 'Application accounts. Identity proof lives in auth_identities, not here.';
comment on column users.unit_system is 'Display preference only. All weights/distances are stored in metric.';
comment on column users.timezone is 'IANA name. Used to bucket workouts into local days for streaks and the heatmap.';

create trigger users_set_updated_at before update on users
    for each row execute function set_updated_at();

create type auth_provider as enum ('google', 'apple', 'github');

create table auth_identities (
    id               uuid primary key default uuidv7(),
    user_id          uuid not null references users (id) on delete cascade,
    provider         auth_provider not null,
    provider_subject text not null,
    email_at_signup  citext,
    created_at       timestamptz not null default now(),
    last_login_at    timestamptz,
    unique (provider, provider_subject)
);
create index auth_identities_user_id_idx on auth_identities (user_id);
comment on table auth_identities is 'One row per linked OAuth account. A user may link several providers.';
comment on column auth_identities.provider_subject is 'The provider''s stable "sub" claim. Never key on email, which can change.';

create table refresh_tokens (
    id         uuid primary key default uuidv7(),
    user_id    uuid not null references users (id) on delete cascade,
    token_hash bytea not null unique,
    family_id  uuid not null,
    user_agent text,
    created_at timestamptz not null default now(),
    expires_at timestamptz not null,
    revoked_at timestamptz
);
create index refresh_tokens_user_id_idx on refresh_tokens (user_id);
create index refresh_tokens_family_id_idx on refresh_tokens (family_id);
comment on table refresh_tokens is 'Our own session tokens, issued after OAuth verification. Only a hash is stored.';
comment on column refresh_tokens.family_id is 'Shared by all tokens descended from one login. Reuse of a revoked token revokes the whole family.';
