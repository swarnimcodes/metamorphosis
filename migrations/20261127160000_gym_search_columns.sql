-- +goose Up
-- Gym search that can use an index.
--
-- The search normalised name, area and city on the fly (drop apostrophes, squeeze punctuation, lower-case), so the
-- trigram index on gyms.name could never be used and every search read every gym. The same normalisation is now
-- stored in two generated columns, each with its own trigram index, and the queries compare against those.
--   name_norm    the gym's name:               "golds gym aundh"
--   search_norm  name, area and city together: "golds gym aundh aundh pune"

alter table gyms add column name_norm text generated always as (
    btrim(lower(regexp_replace(replace(replace(name, '''', ''), '’', ''), '[^[:alnum:]]+', ' ', 'g')))
) stored;
alter table gyms add column search_norm text generated always as (
    btrim(lower(regexp_replace(replace(replace(name || ' ' || coalesce(area, '') || ' ' || coalesce(city, ''), '''', ''), '’', ''),
                               '[^[:alnum:]]+', ' ', 'g')))
) stored;

create index gyms_name_norm_trgm_idx on gyms using gin (name_norm gin_trgm_ops);
create index gyms_search_norm_trgm_idx on gyms using gin (search_norm gin_trgm_ops);
-- The index on the raw name served no query any more.
drop index gyms_name_trgm_idx;
