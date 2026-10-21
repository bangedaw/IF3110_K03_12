-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;
