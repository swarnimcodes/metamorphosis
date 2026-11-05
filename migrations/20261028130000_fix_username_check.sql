-- On a citext column, ~ matches case-insensitively, so the original check let
-- "Swarnim_1" through. Cast to text to enforce lowercase-only handles.
alter table users drop constraint users_username_check;
alter table users add constraint users_username_check
    check (username::text ~ '^[a-z0-9_]{3,30}$');
