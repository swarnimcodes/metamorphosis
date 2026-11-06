-- name: SearchGyms :many
-- Name search by trigram similarity or substring. Both sides are normalised (apostrophes dropped, punctuation
-- squeezed) so "gold's gym" finds "Golds Gym". Ranking: better match first, then the gyms the caller trained at
-- most recently, then verified ones, then by name. An empty query lists everything in that last-used/verified order.
with q as (
    select btrim(lower(regexp_replace(replace(replace(@q::text, '''', ''), '’', ''),
                                      '[^[:alnum:]]+', ' ', 'g'))) as nq
), scored as (
    select g.id, g.name, g.chain, g.area, g.address, g.city, g.lat, g.lng, g.maps_url, g.verified,
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
select id, name, chain, area, address, city, lat, lng, maps_url, verified
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
    select g.id, g.name, g.chain, g.area, g.address, g.city, g.lat, g.lng, g.maps_url, g.verified,
           similarity(btrim(lower(regexp_replace(replace(replace(g.name, '''', ''), '’', ''),
                                                 '[^[:alnum:]]+', ' ', 'g'))), q.nq) as score
      from gyms g, q
     where g.deleted_at is null
       and (sqlc.narg('city')::text is null or lower(g.city) = lower(sqlc.narg('city')))
)
select id, name, chain, area, address, city, lat, lng, maps_url, verified
  from cand
 where score > 0.5
 order by score desc, name
 limit @max_rows;

-- name: InsertGym :one
insert into gyms (name, chain, area, address, city, maps_url, created_by)
values (@name, @chain, @area, @address, @city, @maps_url, @created_by)
returning id, name, chain, area, address, city, lat, lng, maps_url, verified;
