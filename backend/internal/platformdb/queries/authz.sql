-- name: EffectiveAccessLevel :one
SELECT COALESCE(MAX(g.level)::text, '')::text AS level
FROM group_database_grants g
         JOIN group_members m ON m.group_id = g.group_id
WHERE m.user_id = @user_id
  AND g.database_id = @database_id;
