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
insert into gym_edits (gym_id, user_id, changes) values (@gym_id, @user_id, @changes);

-- name: ListGymEdits :many
select id, user_id, changes, created_at from gym_edits where gym_id = @gym_id order by created_at desc, id desc limit @max_rows;

-- name: ListGymChains :many
-- Chains already in the directory, most used first: the suggestions for the chain field.
select chain::text as chain from gyms
 where chain is not null and deleted_at is null
 group by chain order by count(*) desc, chain limit @max_rows;
