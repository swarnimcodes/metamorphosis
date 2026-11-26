-- name: AdminActorRole :one
select role from users where id = @id and deleted_at is null;

-- name: AdminListUsers :many
-- People for the admin's role screen: matching a name or username, or only moderators and admins.
select id, username::text as username, display_name, role, created_at
  from users
 where deleted_at is null
   and (@needle::text = '' or position(@needle::text in lower(username::text)) > 0
        or position(@needle::text in lower(display_name)) > 0)
   and (not @staff_only::bool or role in ('moderator', 'admin'))
 order by (case when @staff_only::bool then role::text end), lower(display_name), id
 limit @max_rows;

-- name: AdminLockUser :one
select id, username::text as username, display_name, role, created_at
  from users where id = @id and deleted_at is null for update;

-- name: AdminSetRole :exec
update users set role = @role where id = @id;

-- name: AdminLogRoleChange :exec
insert into role_changes (admin_id, user_id, from_role, to_role) values (@admin_id, @user_id, @from_role, @to_role);
