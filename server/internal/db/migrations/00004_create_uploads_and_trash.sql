-- +goose Up
-- Uploads in progress. The browser picks the ID (a Nano ID), so an upload can
-- start with a single request. The bytes received so far live in
-- <user folder>/.ashcloud-uploads/<id>.part; its size is the resume point.
CREATE TABLE uploads (
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    id         TEXT    NOT NULL,
    path       TEXT    NOT NULL,  -- where it goes, e.g. /Photos/IMG_0001.jpg
    size       INTEGER NOT NULL,  -- total bytes
    modified   INTEGER NOT NULL,  -- the original file's last-modified time, Unix ms (0 = unknown)
    synced     INTEGER NOT NULL DEFAULT 0,  -- bytes known to be safely on disk (survive a power cut)
    created_at INTEGER NOT NULL,  -- Unix seconds
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, id)
) STRICT;

CREATE INDEX uploads_updated_at ON uploads (updated_at);

-- Deleted files and folders, kept in <user folder>/.ashcloud-trash/<id>/
-- for 30 days so a mistaken delete can be undone.
CREATE TABLE trash (
    id            TEXT    NOT NULL PRIMARY KEY,  -- Nano ID, also the folder name in .ashcloud-trash
    user_id       TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    original_path TEXT    NOT NULL,
    is_folder     INTEGER NOT NULL,
    deleted_at    INTEGER NOT NULL               -- Unix seconds
) STRICT;

CREATE INDEX trash_deleted_at ON trash (deleted_at);

-- +goose Down
DROP TABLE trash;
DROP TABLE uploads;
