-- name: CreateSession :one
INSERT INTO sessions (user_id, token_hash, user_agent, ip, created_at, last_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetSession :one
-- A session that exists and hasn't expired, with its user.
SELECT sessions.id, sessions.user_id, sessions.last_seen_at, sessions.expires_at,
       users.username, users.role
FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.token_hash = ? AND sessions.expires_at > ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ?, expires_at = ?, ip = ? WHERE id = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteUserSessions :execrows
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= ?;
