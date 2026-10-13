-- name: CreateUser :one
INSERT INTO users (username, password_hash, role, created_at)
VALUES (?, ?, ?, ?)
RETURNING id;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: UsernameTaken :one
SELECT EXISTS (SELECT 1 FROM users WHERE username = ?);
