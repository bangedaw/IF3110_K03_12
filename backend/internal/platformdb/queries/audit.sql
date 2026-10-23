-- name: InsertAuditLog :exec
INSERT INTO audit_logs (actor_user_id, action, resource_type, resource_id, outcome, metadata)
VALUES (sqlc.narg('actor_user_id'), @action, @resource_type, sqlc.narg('resource_id'), @outcome, @metadata);
