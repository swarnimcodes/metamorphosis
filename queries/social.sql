-- name: SearchPeople :many
-- People whose username or display name contains the text (or is close to it), never the caller and never anyone
-- blocked either way. The relationship is from the caller's side.
select u.id, u.username::text as username, u.display_name, u.avatar_url,
       coalesce((select case when f.status = 'accepted' then 'friends'
                             when f.requester_id = @viewer_id then 'request_sent'
                             else 'request_received' end
                   from friendships f
                  where (f.requester_id = @viewer_id and f.addressee_id = u.id)
                     or (f.requester_id = u.id and f.addressee_id = @viewer_id)), 'none')::text as relationship
  from users u
 where u.deleted_at is null and u.id <> @viewer_id
   and not exists (select 1 from blocks b
                    where (b.blocker_id = @viewer_id and b.blocked_id = u.id)
                       or (b.blocker_id = u.id and b.blocked_id = @viewer_id))
   and (position(@needle::text in lower(u.username::text)) > 0
        or position(@needle::text in lower(u.display_name)) > 0
        or similarity(lower(u.display_name), @needle::text) > 0.35
        or similarity(lower(u.username::text), @needle::text) > 0.35)
 order by (lower(u.username::text) = @needle::text) desc,
          (lower(u.username::text) like @needle::text || '%' or lower(u.display_name) like @needle::text || '%') desc,
          greatest(similarity(lower(u.display_name), @needle::text), similarity(lower(u.username::text), @needle::text)) desc,
          u.display_name
 limit @max_rows;

-- name: GetPerson :one
-- One person as the caller sees them: not found when they are gone or have blocked the caller.
select u.id, u.username::text as username, u.display_name, u.avatar_url, u.created_at as member_since,
       (select count(*) from workouts w where w.user_id = u.id and w.deleted_at is null and w.ended_at is not null)::int4 as workouts,
       coalesce((select case when f.status = 'accepted' then 'friends'
                             when f.requester_id = @viewer_id then 'request_sent'
                             else 'request_received' end
                   from friendships f
                  where (f.requester_id = @viewer_id and f.addressee_id = u.id)
                     or (f.requester_id = u.id and f.addressee_id = @viewer_id)), 'none')::text as relationship,
       exists (select 1 from blocks b where b.blocker_id = @viewer_id and b.blocked_id = u.id) as blocked_by_me,
       (select f.responded_at from friendships f
         where f.status = 'accepted'
           and ((f.requester_id = @viewer_id and f.addressee_id = u.id) or (f.requester_id = u.id and f.addressee_id = @viewer_id))) as friends_since
  from users u
 where u.id = @id and u.deleted_at is null
   and not exists (select 1 from blocks b where b.blocker_id = u.id and b.blocked_id = @viewer_id);

-- name: ListFriends :many
select u.id, u.username::text as username, u.display_name, u.avatar_url
  from friendships f
  join users u on u.id = case when f.requester_id = @viewer_id then f.addressee_id else f.requester_id end
 where f.status = 'accepted' and (f.requester_id = @viewer_id or f.addressee_id = @viewer_id)
   and u.deleted_at is null
 order by lower(u.display_name), u.id;

-- name: ListIncomingRequests :many
select u.id, u.username::text as username, u.display_name
  from friendships f join users u on u.id = f.requester_id
 where f.addressee_id = @viewer_id and f.status = 'pending' and u.deleted_at is null
   and not exists (select 1 from blocks b where b.blocker_id = u.id and b.blocked_id = @viewer_id)
 order by f.created_at desc;

-- name: ListOutgoingRequests :many
select u.id, u.username::text as username, u.display_name
  from friendships f join users u on u.id = f.addressee_id
 where f.requester_id = @viewer_id and f.status = 'pending' and u.deleted_at is null
   and not exists (select 1 from blocks b where b.blocker_id = u.id and b.blocked_id = @viewer_id)
 order by f.created_at desc;

-- name: ListBlocked :many
select u.id, u.username::text as username, u.display_name
  from blocks b join users u on u.id = b.blocked_id
 where b.blocker_id = @viewer_id and u.deleted_at is null
 order by b.created_at desc;

-- name: LockFriendship :one
-- The pair's row, whoever asked, locked for the change about to be made.
select requester_id, addressee_id, status from friendships
 where least(requester_id, addressee_id) = least(@a::uuid, @b::uuid)
   and greatest(requester_id, addressee_id) = greatest(@a::uuid, @b::uuid)
   for update;

-- name: InsertFriendRequest :exec
insert into friendships (requester_id, addressee_id) values (@requester_id, @addressee_id);

-- name: AcceptFriendship :exec
update friendships set status = 'accepted', responded_at = now()
 where requester_id = @requester_id and addressee_id = @addressee_id and status = 'pending';

-- name: DeleteFriendship :execrows
delete from friendships
 where least(requester_id, addressee_id) = least(@a::uuid, @b::uuid)
   and greatest(requester_id, addressee_id) = greatest(@a::uuid, @b::uuid);

-- name: CountRecentFriendRequests :one
select count(*)::int4 from friendships where requester_id = @user_id and created_at > @since;

-- name: PersonExists :one
-- A live account that has not blocked the caller.
select exists (select 1 from users u where u.id = @id and u.deleted_at is null
                and not exists (select 1 from blocks b where b.blocker_id = u.id and b.blocked_id = @viewer_id)) as found;

-- name: IsBlockedEitherWay :one
select exists (select 1 from blocks
                where (blocker_id = @a and blocked_id = @b) or (blocker_id = @b and blocked_id = @a)) as blocked;

-- name: AreFriends :one
select exists (select 1 from friendships
                where status = 'accepted'
                  and least(requester_id, addressee_id) = least(@a::uuid, @b::uuid)
                  and greatest(requester_id, addressee_id) = greatest(@a::uuid, @b::uuid)) as friends;

-- name: InsertBlock :exec
insert into blocks (blocker_id, blocked_id) values (@blocker_id, @blocked_id) on conflict do nothing;

-- name: DeleteBlock :execrows
delete from blocks where blocker_id = @blocker_id and blocked_id = @blocked_id;

-- name: DeleteLikesBetween :exec
-- Likes either of two people left on the other's workouts, when they block each other.
delete from workout_likes l using workouts w
 where l.workout_id = w.id
   and ((l.user_id = @a::uuid and w.user_id = @b::uuid) or (l.user_id = @b::uuid and w.user_id = @a::uuid));

-- name: DeleteNotificationsBetween :exec
delete from notifications
 where (user_id = @a::uuid and actor_id = @b::uuid) or (user_id = @b::uuid and actor_id = @a::uuid);

-- name: WorkoutBelongsTo :one
select exists (select 1 from workouts where id = @id and user_id = @owner_id and deleted_at is null) as found;

-- name: InsertLike :execrows
insert into workout_likes (workout_id, user_id) values (@workout_id, @user_id) on conflict do nothing;

-- name: DeleteLike :execrows
delete from workout_likes where workout_id = @workout_id and user_id = @user_id;

-- name: LikeSummary :one
select count(*)::int4 as likes, coalesce(bool_or(user_id = @viewer_id), false)::bool as liked
  from workout_likes where workout_id = @workout_id;

-- name: InsertNotification :exec
insert into notifications (user_id, kind, actor_id, workout_id)
values (@user_id, @kind, @actor_id, @workout_id) on conflict do nothing;

-- name: DeleteNotification :exec
delete from notifications
 where user_id = @user_id and kind = @kind and actor_id = @actor_id and workout_id is not distinct from @workout_id;

-- name: ListNotifications :many
select n.id, n.kind, n.workout_id, n.created_at, (n.read_at is not null)::bool as read,
       a.id as actor_id, a.username::text as actor_username, a.display_name as actor_name
  from notifications n join users a on a.id = n.actor_id
 where n.user_id = @user_id and a.deleted_at is null
 order by n.created_at desc, n.id desc
 limit @max_rows;

-- name: CountUnreadNotifications :one
select count(*)::int4 from notifications n join users a on a.id = n.actor_id
 where n.user_id = @user_id and n.read_at is null and a.deleted_at is null;

-- name: MarkNotificationsRead :exec
update notifications set read_at = now() where user_id = @user_id and read_at is null;

-- name: SocialUserRole :one
select role from users where id = @id and deleted_at is null;

-- name: CountRecentReports :one
select count(*)::int4 from reports where reporter_id = @user_id and created_at > @since;

-- name: OpenReportExists :one
select exists (select 1 from reports
                where reporter_id = @reporter_id and target_user_id = @target_user_id
                  and workout_id is not distinct from @workout_id and status = 'open') as found;

-- name: InsertReport :exec
insert into reports (reporter_id, target_user_id, workout_id, reason)
values (@reporter_id, @target_user_id, @workout_id, @reason);

-- name: ListReports :many
select r.id, r.workout_id, r.reason, r.status, r.created_at, r.resolved_at,
       rp.id as reporter_id, coalesce(rp.username::text, '')::text as reporter_username,
       t.id as target_id, t.username::text as target_username, t.display_name as target_name
  from reports r
  join users t on t.id = r.target_user_id
  left join users rp on rp.id = r.reporter_id
 where r.status = @status
 order by r.created_at desc, r.id desc
 limit @max_rows;

-- name: ResolveReport :execrows
update reports set status = 'resolved', resolved_by = @by, resolved_at = now() where id = @id and status = 'open';
