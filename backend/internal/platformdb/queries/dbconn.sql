-- name: ListConnections :many
SELECT * FROM database_connections
ORDER BY id
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountConnections :one
SELECT count(*) FROM database_connections;

-- name: GetConnectionByID :one
SELECT * FROM database_connections WHERE id = $1;

-- name: InsertConnection :one
INSERT INTO database_connections (name, kind, host, port, database_name, username, password_ciphertext, ssl_mode, is_active, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, sqlc.narg('created_by'))
RETURNING *;

-- name: UpdateConnection :one
UPDATE database_connections
SET name               = $2,
    kind               = $3,
    host               = $4,
    port               = $5,
    database_name      = $6,
    username           = $7,
    password_ciphertext = CASE
                             WHEN sqlc.arg('update_password')::boolean THEN $8
                             ELSE password_ciphertext
        END,
    ssl_mode           = $9,
    is_active          = $10,
    updated_at         = now()
WHERE id = $1
RETURNING *;

-- name: DeleteConnection :exec
DELETE FROM database_connections WHERE id = $1;
