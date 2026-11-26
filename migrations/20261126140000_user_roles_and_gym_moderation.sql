-- +goose Up
-- Roles, and what moderating the gym directory needs to remember.
--
-- user < moderator < admin. Everyone starts as a user; roles are granted by hand in the database (see
-- deploy/README.md), never through the API. What each role may do lives in internal/gym/policy.go.

create type user_role as enum ('user', 'moderator', 'admin');
alter table users add column role user_role not null default 'user';
comment on column users.role is 'user, moderator or admin. Granted by an operator with SQL; the API never changes it.';

-- Who verified a gym and when; a merged gym points at the one it was folded into (as exercises do).
alter table gyms add column verified_at timestamptz;
alter table gyms add column verified_by uuid references users (id) on delete set null;
alter table gyms add column merged_into_id uuid references gyms (id) on delete set null;
comment on column gyms.merged_into_id is 'Set when this gym was merged into another: its workouts moved there and it is soft-deleted.';

-- gym_edits becomes the moderation log: what kind of action it was, and for a revert which edit it undid.
alter table gym_edits add column kind text not null default 'edit'
    check (kind in ('edit', 'verify', 'unverify', 'delete', 'merge', 'revert'));
alter table gym_edits add column revert_of uuid references gym_edits (id) on delete set null;
-- An edit is undone at most once.
create unique index gym_edits_one_revert_idx on gym_edits (revert_of) where revert_of is not null;
create index gym_edits_user_idx on gym_edits (user_id, created_at desc);
