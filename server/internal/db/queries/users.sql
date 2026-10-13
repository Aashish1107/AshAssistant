-- name: CreateUser :exec
INSERT INTO users (id, username, password_hash, role, created_at)
VALUES (?, ?, ?, ?, ?);

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: UsernameTaken :one
SELECT EXISTS (SELECT 1 FROM users WHERE username = ?);

-- name: GetUserByUsername :one
SELECT id, username, password_hash, role FROM users WHERE username = ?;

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = ? WHERE id = ?;
