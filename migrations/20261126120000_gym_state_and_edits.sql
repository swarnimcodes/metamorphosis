-- +goose Up
-- Gyms get a state, and every edit to a gym is kept.
--
-- country_code already existed (ISO 3166-1 alpha-2) but was never exposed; state is the name of the region as
-- shown in the app's picker. Edits are recorded as {"field": {"from": ..., "to": ...}} so the gym page can show
-- what changed. The editor is kept for moderation but the API only tells other people "a member".

alter table gyms add column state text;
comment on column gyms.state is 'Region/state name, e.g. "Maharashtra". Free text; the app offers a list.';
alter table gyms add constraint gyms_country_code_check check (country_code is null or country_code ~ '^[A-Z]{2}$');

create table gym_edits (
    id         uuid primary key default uuidv7(),
    gym_id     uuid not null references gyms (id) on delete cascade,
    user_id    uuid references users (id) on delete set null,
    changes    jsonb not null check (jsonb_typeof(changes) = 'object'),
    created_at timestamptz not null default now()
);
create index gym_edits_gym_idx on gym_edits (gym_id, created_at desc);
comment on table gym_edits is 'One row per saved edit of a gym: which fields changed, from what, to what, and by whom.';
