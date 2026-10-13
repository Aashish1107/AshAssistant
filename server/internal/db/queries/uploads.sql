-- name: CreateUpload :exec
INSERT INTO uploads (user_id, id, path, size, modified, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetUpload :one
SELECT * FROM uploads WHERE user_id = ? AND id = ?;

-- name: SaveUploadProgress :exec
UPDATE uploads SET synced = ?, updated_at = ? WHERE user_id = ? AND id = ?;

-- name: DeleteUpload :exec
DELETE FROM uploads WHERE user_id = ? AND id = ?;

-- name: AllUploads :many
SELECT user_id, id, synced FROM uploads;

-- name: StaleUploads :many
-- Uploads nobody has continued since the cutoff.
SELECT user_id, id FROM uploads WHERE updated_at < ?;

-- name: AddTrash :exec
INSERT INTO trash (id, user_id, original_path, is_folder, deleted_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetTrash :one
SELECT * FROM trash WHERE user_id = ? AND id = ?;

-- name: ExpiredTrash :many
SELECT user_id, id FROM trash WHERE deleted_at < ?;

-- name: DeleteTrash :exec
DELETE FROM trash WHERE user_id = ? AND id = ?;

-- name: MarkTrashFolder :exec
UPDATE trash SET is_folder = 1 WHERE user_id = ? AND id = ?;
