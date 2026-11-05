-- Reps in reserve per set (replaces the never-used rpe), and a daily bodyweight log.

alter table sets drop column rpe;
alter table sets add column rir smallint check (rir between 0 and 5);
comment on column sets.rir is 'Reps in reserve: 0 = to failure, 5 = five or more left. Null when not recorded.';

create table body_weights (
    id         uuid primary key default uuidv7(),
    user_id    uuid not null references users (id) on delete cascade,
    measured_on date not null,
    weight_kg  numeric(5, 2) not null check (weight_kg > 0 and weight_kg < 500),
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (user_id, measured_on)
);
comment on table body_weights is 'One weigh-in per user per local calendar day (the client sends the date in the user''s timezone).';
create trigger body_weights_set_updated_at before update on body_weights
    for each row execute function set_updated_at();
