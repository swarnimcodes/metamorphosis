-- name: SearchGyms :many
-- Name search by trigram similarity or substring. Both sides are normalised (apostrophes dropped, punctuation
-- squeezed) so "gold's gym" finds "Golds Gym". Ranking: better match first, then the gyms the caller trained at
-- most recently, then verified ones, then by name. An empty query lists everything in that last-used/verified order.
with q as (
    select btrim(lower(regexp_replace(replace(replace(@q::text, '''', ''), '’', ''),
                                      '[^[:alnum:]]+', ' ', 'g'))) as nq
), scored as (
    select g.id, g.name, g.chain, g.area, g.address, g.city, g.lat, g.lng, g.maps_url, g.verified, g.country_code, g.state,
           (select max(w.started_at) from workouts w
             where w.gym_id = g.id and w.user_id = @user_id and w.deleted_at is null) as last_used,
           similarity(n.nname, q.nq) as score,
           -- every word of the query appears in the name, area or city, in any order
           not exists (
               select 1 from unnest(string_to_array(q.nq, ' ')) as w
                where w <> '' and n.hay not like '%' || w || '%'
           ) as contains
      from gyms g
     cross join q
     cross join lateral (
         select btrim(lower(regexp_replace(replace(replace(g.name, '''', ''), '’', ''),
                                           '[^[:alnum:]]+', ' ', 'g'))) as nname,
                btrim(lower(regexp_replace(replace(replace(
                    g.name || ' ' || coalesce(g.area, '') || ' ' || coalesce(g.city, ''), '''', ''), '’', ''),
                    '[^[:alnum:]]+', ' ', 'g'))) as hay
     ) n
     where g.deleted_at is null
)
select id, name, chain, area, address, city, lat, lng, maps_url, verified, country_code, state
  from scored, q
 where q.nq = '' or contains or score > 0.3
 order by (case when q.nq = '' then true else contains end) desc,
          (case when q.nq = '' then 0 else score end) desc,
          last_used desc nulls last, verified desc, name
 limit @max_rows;

-- name: SimilarGyms :many
-- Gyms whose name is close to @name (in the same city when one is given): the near-duplicate guard's candidates.
with q as (
    select btrim(lower(regexp_replace(replace(replace(@name::text, '''', ''), '’', ''),
                                      '[^[:alnum:]]+', ' ', 'g'))) as nq
), cand as (
    select g.id, g.name, g.chain, g.area, g.address, g.city, g.lat, g.lng, g.maps_url, g.verified, g.country_code, g.state,
           similarity(btrim(lower(regexp_replace(replace(replace(g.name, '''', ''), '’', ''),
                                                 '[^[:alnum:]]+', ' ', 'g'))), q.nq) as score
      from gyms g, q
     where g.deleted_at is null
       and (sqlc.narg('city')::text is null or lower(g.city) = lower(sqlc.narg('city')))
)
select id, name, chain, area, address, city, lat, lng, maps_url, verified, country_code, state
  from cand
 where score > 0.5
 order by score desc, name
 limit @max_rows;

-- name: InsertGym :one
insert into gyms (name, chain, area, address, city, state, country_code, maps_url, created_by)
values (@name, @chain, @area, @address, @city, @state, @country_code, @maps_url, @created_by)
returning id, name, chain, area, address, city, lat, lng, maps_url, verified, country_code, state;

-- name: GetGym :one
-- One gym, with how often and when the caller last trained there. last_visit is the epoch when there were none.
select g.id, g.name, g.chain, g.area, g.address, g.city, g.lat, g.lng, g.maps_url, g.verified, g.country_code, g.state,
       v.visits::int4 as visits, v.last_visit
  from gyms g
  left join lateral (
      select count(*) as visits, coalesce(max(w.started_at), 'epoch'::timestamptz)::timestamptz as last_visit from workouts w
       where w.gym_id = g.id and w.user_id = @user_id and w.deleted_at is null
  ) v on true
 where g.id = @id and g.deleted_at is null;

-- name: LockGym :one
select id, name, chain, area, address, city, lat, lng, maps_url, verified, country_code, state
  from gyms where id = @id and deleted_at is null for update;

-- name: UpdateGym :exec
update gyms set name = @name, chain = @chain, area = @area, address = @address, city = @city, state = @state,
                country_code = @country_code, maps_url = @maps_url
 where id = @id;

-- name: InsertGymEdit :exec
insert into gym_edits (gym_id, user_id, kind, changes, revert_of)
values (@gym_id, @user_id, @kind, @changes, @revert_of);

-- name: ListGymEdits :many
-- editor_name is only ever shown to moderators; the service drops it for everyone else.
select e.id, e.user_id, e.kind, e.changes, e.revert_of, e.created_at,
       u.display_name as editor_name,
       exists (select 1 from gym_edits r where r.revert_of = e.id) as reverted
  from gym_edits e
  left join users u on u.id = e.user_id
 where e.gym_id = @gym_id
 order by e.created_at desc, e.id desc
 limit @max_rows;

-- name: GetGymEdit :one
select e.id, e.kind, e.changes,
       exists (select 1 from gym_edits r where r.revert_of = e.id) as reverted
  from gym_edits e
 where e.id = @id and e.gym_id = @gym_id;

-- name: GymUserRole :one
select role from users where id = @id and deleted_at is null;

-- name: CountRecentGymEdits :one
-- Edits and reverts a user made since a moment: the edit rate limit.
select count(*)::int4 from gym_edits
 where user_id = @user_id and kind in ('edit', 'revert') and created_at > @since;

-- name: SetGymVerified :exec
update gyms set verified = @verified,
                verified_at = case when @verified then now() end,
                verified_by = case when @verified then sqlc.narg('by')::uuid end
 where id = @id;

-- name: SoftDeleteGym :exec
update gyms set deleted_at = now() where id = @id and deleted_at is null;

-- name: MoveGymWorkouts :execrows
update workouts set gym_id = @target_id where gym_id = @source_id;

-- name: MoveUserGyms :exec
-- Saved gyms follow the merge. A user's default is not carried over: they may already have another one.
insert into user_gyms (user_id, gym_id, is_default)
select ug.user_id, @target_id::uuid, false from user_gyms ug where ug.gym_id = @source_id
on conflict do nothing;

-- name: DropUserGyms :exec
delete from user_gyms where gym_id = @gym_id;

-- name: MarkGymMerged :exec
update gyms set deleted_at = now(), merged_into_id = @target_id where id = @id and deleted_at is null;

-- name: ListGymChains :many
-- Chains already in the directory, most used first: the suggestions for the chain field.
select chain::text as chain from gyms
 where chain is not null and deleted_at is null
 group by chain order by count(*) desc, chain limit @max_rows;
