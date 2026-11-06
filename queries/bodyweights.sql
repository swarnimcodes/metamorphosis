-- name: ListBodyWeights :many
-- Oldest first, which is the order a chart wants; when capped, the oldest entries are dropped, never the newest.
select measured_on, weight_kg from (
    select measured_on, weight_kg
      from body_weights
     where user_id = @user_id
       and (sqlc.narg('from_day')::date is null or measured_on >= sqlc.narg('from_day'))
       and (sqlc.narg('to_day')::date is null or measured_on <= sqlc.narg('to_day'))
     order by measured_on desc
     limit @max_entries
) newest
order by measured_on;

-- name: UpsertBodyWeight :one
insert into body_weights (user_id, measured_on, weight_kg)
values (@user_id, @measured_on, @weight_kg)
on conflict (user_id, measured_on) do update set weight_kg = excluded.weight_kg
returning measured_on, weight_kg;

-- name: DeleteBodyWeight :execrows
delete from body_weights where user_id = @user_id and measured_on = @measured_on;

-- name: LatestLocalDate :one
-- The furthest ahead any local calendar date can be right now (UTC+14).
select (now() at time zone 'Etc/GMT-14')::date as latest;
