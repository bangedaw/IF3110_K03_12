-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: CreateSessionFamily :exec
INSERT INTO session_families (id, user_id, created_at, absolute_expires_at, last_seen_at, last_rotated_at, user_agent, ip)
VALUES (@id, @user_id, @now, @absolute_expires_at, @now, @now, @user_agent, @ip);

-- name: CreateSessionToken :exec
INSERT INTO session_tokens (token_hash, family_id, issued_at)
VALUES (@token_hash, @family_id, @now);

-- name: GetSessionByTokenHash :one
SELECT t.family_id,
       t.superseded_at,
       f.user_id,
       f.absolute_expires_at,
       f.last_seen_at,
       f.last_rotated_at,
       f.rotate_required,
       f.revoked_at,
       u.is_active,
       u.is_platform_admin
FROM session_tokens t
         JOIN session_families f ON f.id = t.family_id
         JOIN users u ON u.id = f.user_id
WHERE t.token_hash = $1;

-- name: SupersedeSessionToken :execrows
UPDATE session_tokens
SET superseded_at = @now
WHERE token_hash = @token_hash
  AND superseded_at IS NULL;

-- name: MarkSessionRotated :exec
UPDATE session_families
SET last_rotated_at = @now,
    last_seen_at    = @now,
    rotate_required = FALSE
WHERE id = @id;

-- name: TouchSessionFamily :exec
UPDATE session_families SET last_seen_at = @now WHERE id = @id;

-- name: RevokeSessionFamily :exec
UPDATE session_families SET revoked_at = @now WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeUserSessions :exec
UPDATE session_families SET revoked_at = @now WHERE user_id = @user_id AND revoked_at IS NULL;

-- name: UpsertPlatformAdmin :one
INSERT INTO users (username, password_hash, is_platform_admin, is_active)
VALUES (@username, @password_hash, TRUE, TRUE)
ON CONFLICT (username) DO UPDATE
    SET password_hash     = EXCLUDED.password_hash,
        is_platform_admin = TRUE,
        is_active         = TRUE,
        updated_at        = now()
RETURNING id;
