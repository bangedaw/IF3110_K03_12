-- name: ListUsers :many
SELECT * FROM users ORDER BY id LIMIT @lim OFFSET @off;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreateUser :one
INSERT INTO users (username, display_name, password_hash, is_platform_admin)
VALUES (@username, @display_name, @password_hash, @is_platform_admin)
RETURNING *;

-- name: UpdateUser :one
UPDATE users
SET display_name      = COALESCE(sqlc.narg('display_name'), display_name),
    is_platform_admin = COALESCE(sqlc.narg('is_platform_admin'), is_platform_admin),
    is_active         = COALESCE(sqlc.narg('is_active'), is_active),
    updated_at        = now()
WHERE id = @id
RETURNING *;

-- name: UpdateUserPassword :execrows
UPDATE users SET password_hash = @password_hash, updated_at = now() WHERE id = @id;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = @id;

-- name: ListGroups :many
SELECT * FROM groups ORDER BY id LIMIT @lim OFFSET @off;

-- name: CountGroups :one
SELECT count(*) FROM groups;

-- name: GetGroupByID :one
SELECT * FROM groups WHERE id = @id;

-- name: CreateGroup :one
INSERT INTO groups (name, description) VALUES (@name, @description) RETURNING *;

-- name: UpdateGroup :one
UPDATE groups
SET name        = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description)
WHERE id = @id
RETURNING *;

-- name: DeleteGroup :execrows
DELETE FROM groups WHERE id = @id;

-- name: ListGroupMembers :many
SELECT u.*
FROM users u
         JOIN group_members m ON m.user_id = u.id
WHERE m.group_id = @group_id
ORDER BY u.id;

-- name: AddGroupMember :exec
INSERT INTO group_members (group_id, user_id) VALUES (@group_id, @user_id) ON CONFLICT DO NOTHING;

-- name: RemoveGroupMember :execrows
DELETE FROM group_members WHERE group_id = @group_id AND user_id = @user_id;

-- name: ListGroupGrants :many
SELECT g.database_id, d.name AS database_name, g.level
FROM group_database_grants g
         JOIN database_connections d ON d.id = g.database_id
WHERE g.group_id = @group_id
ORDER BY d.name;

-- name: GetGroupGrantLevel :one
SELECT level FROM group_database_grants WHERE group_id = @group_id AND database_id = @database_id;

-- name: UpsertGroupGrant :one
WITH upserted AS (
    INSERT INTO group_database_grants (group_id, database_id, level)
        VALUES (@group_id, @database_id, @level)
        ON CONFLICT (group_id, database_id) DO UPDATE SET level = EXCLUDED.level
        RETURNING database_id, level)
SELECT u.database_id, d.name AS database_name, u.level
FROM upserted u
         JOIN database_connections d ON d.id = u.database_id;

-- name: DeleteGroupGrant :execrows
DELETE FROM group_database_grants WHERE group_id = @group_id AND database_id = @database_id;

-- name: RequireRotationForUser :exec
UPDATE session_families SET rotate_required = TRUE WHERE user_id = @user_id AND revoked_at IS NULL;

-- name: RequireRotationForGroup :exec
UPDATE session_families
SET rotate_required = TRUE
WHERE revoked_at IS NULL
  AND user_id IN (SELECT user_id FROM group_members WHERE group_id = @group_id);
