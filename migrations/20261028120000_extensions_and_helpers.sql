-- Extensions and shared helpers used by every later migration.

create extension if not exists citext;   -- case-insensitive email / username
create extension if not exists pg_trgm;  -- fuzzy exercise / gym search

-- Bumps updated_at on every UPDATE. Attached per table with a trigger.
create function set_updated_at() returns trigger
language plpgsql as $$
begin
    new.updated_at = now();
    return new;
end;
$$;

create type unit_system as enum ('metric', 'imperial');

-- Who may see a piece of user content. Only 'private' is enforced in phase 1;
-- 'friends' and 'public' take effect once the social tables exist.
create type visibility as enum ('private', 'friends', 'public');
