-- name: GetMe :one
select id, username, display_name, email, avatar_url, unit_system, timezone
  from users
 where id = @id and deleted_at is null;

-- name: UpdateMe :one
-- A NULL parameter leaves the column as it is.
update users
   set display_name = coalesce(sqlc.narg('display_name'), display_name),
       unit_system = coalesce(sqlc.narg('unit_system')::unit_system, unit_system),
       timezone = coalesce(sqlc.narg('timezone'), timezone)
 where id = @id and deleted_at is null
returning id, username, display_name, email, avatar_url, unit_system, timezone;

-- name: TimezoneKnown :one
-- Anything stored and later used in `at time zone` must be a zone the database knows.
select exists (select 1 from pg_catalog.pg_timezone_names where name = @name) as known;

-- name: GetUsername :one
select username from users where id = @id and deleted_at is null;

-- name: LockUserForDelete :one
select username from users where id = @id and deleted_at is null for update;

-- name: DeleteUser :exec
-- Everything the user owns cascades from the user row.
delete from users where id = @id;

-- name: UpsertDevUser :one
insert into users (email, username, display_name)
values (@email, @username, @display_name)
on conflict (username) do update set username = excluded.username
returning id, username;

-- name: UsernameTaken :one
select exists (select 1 from users where username = @username) as taken;

-- name: TouchGoogleLogin :one
-- Records the sign-in and returns the account, if this Google identity is registered to a live one.
update auth_identities ai set last_login_at = now()
  from users u
 where ai.provider = 'google' and ai.provider_subject = @subject
   and u.id = ai.user_id and u.deleted_at is null
returning u.id, u.username;

-- name: GoogleIdentityExists :one
select exists (
    select 1 from auth_identities where provider = 'google' and provider_subject = @subject
) as found;

-- name: InsertUser :one
insert into users (email, username, display_name, avatar_url)
values (@email, @username, @display_name, @avatar_url)
returning id;

-- name: InsertGoogleIdentity :exec
insert into auth_identities (user_id, provider, provider_subject, email_at_signup, last_login_at)
values (@user_id, 'google', @subject, @email, now());
