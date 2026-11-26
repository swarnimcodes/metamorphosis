-- +goose Up
-- Friends, likes, notifications, blocking and reports.
--
-- Anyone can find anyone by name, but a stranger sees only the basics. Friends (mutual, accepted) see each other's
-- workouts. Workout notes, body weight and email are never shared.

create table friendships (
    requester_id uuid not null references users (id) on delete cascade,
    addressee_id uuid not null references users (id) on delete cascade,
    status       text not null default 'pending' check (status in ('pending', 'accepted')),
    created_at   timestamptz not null default now(),
    responded_at timestamptz,
    primary key (requester_id, addressee_id),
    check (requester_id <> addressee_id)
);
-- One row per pair, whoever asked first.
create unique index friendships_pair_idx on friendships (least(requester_id, addressee_id), greatest(requester_id, addressee_id));
create index friendships_addressee_idx on friendships (addressee_id, status);
create index friendships_recent_idx on friendships (requester_id, created_at desc);
comment on table friendships is 'A friend request (pending) or a friendship (accepted). Deleting the row cancels, declines or unfriends.';

create table blocks (
    blocker_id uuid not null references users (id) on delete cascade,
    blocked_id uuid not null references users (id) on delete cascade,
    created_at timestamptz not null default now(),
    primary key (blocker_id, blocked_id),
    check (blocker_id <> blocked_id)
);
create index blocks_blocked_idx on blocks (blocked_id);
comment on table blocks is 'blocker hides themself from blocked and the other way round; any friendship between them is removed.';

create table workout_likes (
    workout_id uuid not null references workouts (id) on delete cascade,
    user_id    uuid not null references users (id) on delete cascade,
    created_at timestamptz not null default now(),
    primary key (workout_id, user_id)
);
create index workout_likes_user_idx on workout_likes (user_id);

create table notifications (
    id         uuid primary key default uuidv7(),
    user_id    uuid not null references users (id) on delete cascade,
    kind       text not null check (kind in ('friend_request', 'friend_accepted', 'workout_liked')),
    actor_id   uuid not null references users (id) on delete cascade,
    workout_id uuid references workouts (id) on delete cascade,
    created_at timestamptz not null default now(),
    read_at    timestamptz
);
-- The same person liking the same workout twice is one notification.
create unique index notifications_once_idx on notifications (user_id, kind, actor_id, workout_id) nulls not distinct;
create index notifications_user_idx on notifications (user_id, created_at desc);
create index notifications_unread_idx on notifications (user_id) where read_at is null;

create table reports (
    id             uuid primary key default uuidv7(),
    reporter_id    uuid references users (id) on delete set null,
    target_user_id uuid not null references users (id) on delete cascade,
    workout_id     uuid references workouts (id) on delete cascade,
    reason         text not null check (length(reason) between 1 and 500),
    status         text not null default 'open' check (status in ('open', 'resolved')),
    created_at     timestamptz not null default now(),
    resolved_by    uuid references users (id) on delete set null,
    resolved_at    timestamptz
);
create index reports_status_idx on reports (status, created_at desc);
create index reports_reporter_idx on reports (reporter_id, created_at desc);
comment on table reports is 'User reports of a person (or one of their workouts). Moderators and admins work the queue.';

-- Searching people by name.
create index users_display_name_trgm_idx on users using gin (lower(display_name) gin_trgm_ops);
create index users_username_trgm_idx on users using gin ((username::text) gin_trgm_ops);
