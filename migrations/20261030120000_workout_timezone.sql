-- Each workout remembers the time zone it happened in.
--
-- started_at is an exact instant (UTC) and stays the source of truth. The zone says which calendar day and
-- clock time that instant was *for the person at the time*, so history keeps the day you experienced even
-- after you travel or change your profile zone. Null means "recorded before zones were kept": those fall
-- back to the account's zone, as they always did.

alter table workouts add column timezone text;
comment on column workouts.timezone is
    'IANA zone the workout was done in (from the device). Day bucketing and display use it; null falls back to users.timezone.';

-- A bad zone name would make every stats query for that user fail, so refuse one at the door.
create function workouts_check_timezone() returns trigger language plpgsql as $$
begin
    if new.timezone is not null
       and not exists (select 1 from pg_timezone_names where name = new.timezone) then
        raise exception 'unknown time zone: %', new.timezone using errcode = '22023';
    end if;
    return new;
end $$;

create trigger workouts_check_timezone before insert or update of timezone on workouts
    for each row execute function workouts_check_timezone();
