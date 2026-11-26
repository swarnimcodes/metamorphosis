-- +goose Up
-- A record of every role an admin grants or takes away through the app, so nothing about who can moderate
-- changes silently. Roles granted by hand in SQL are not recorded here.

create table role_changes (
    id         uuid primary key default uuidv7(),
    admin_id   uuid references users (id) on delete set null,
    user_id    uuid not null references users (id) on delete cascade,
    from_role  user_role not null,
    to_role    user_role not null,
    created_at timestamptz not null default now()
);
create index role_changes_user_idx on role_changes (user_id, created_at desc);
comment on table role_changes is 'Role changes made by admins in the app: who changed whom, from what, to what.';
