-- +goose Up
-- The unit a lifter logged an exercise in (a gym with lb plates, a home rack in kg).
--
-- Weights stay in sets.weight_kg, the only canonical value, so charts and stats never mix units. This column
-- is a display hint: the app shows (and takes input for) this exercise's sets in this unit. Null means
-- "follow the account's unit_system".

alter table workout_exercises add column weight_unit unit_system;
comment on column workout_exercises.weight_unit is
    'Unit the exercise was logged in. Display hint only; sets.weight_kg is canonical. Null follows users.unit_system.';
