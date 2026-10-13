-- +goose Up
-- User IDs become text: the first admin keeps "1" and every user created
-- from now on gets a random 21-character Nano ID, so IDs can't be guessed or
-- counted. SQLite can't change a column's type, so the three tables are
-- rebuilt and every row is copied across. This can't be undone.

CREATE TABLE users_new (
    id            TEXT    NOT NULL PRIMARY KEY,  -- "1" for the first admin, otherwise a Nano ID
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,              -- argon2id, PHC string format
    role          TEXT    NOT NULL CHECK (role IN ('admin', 'user')),
    created_at    INTEGER NOT NULL               -- Unix seconds
) STRICT;

INSERT INTO users_new (id, username, password_hash, role, created_at)
SELECT CAST(id AS TEXT), username, password_hash, role, created_at FROM users;

CREATE TABLE sessions_new (
    id           INTEGER PRIMARY KEY,
    user_id      TEXT    NOT NULL REFERENCES users_new (id) ON DELETE CASCADE,
    token_hash   BLOB    NOT NULL UNIQUE,  -- SHA-256 of the cookie; the token itself is never stored
    user_agent   TEXT    NOT NULL,
    ip           TEXT    NOT NULL,         -- last address the session was used from
    created_at   INTEGER NOT NULL,         -- Unix seconds
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
) STRICT;

INSERT INTO sessions_new (id, user_id, token_hash, user_agent, ip, created_at, last_seen_at, expires_at)
SELECT id, CAST(user_id AS TEXT), token_hash, user_agent, ip, created_at, last_seen_at, expires_at FROM sessions;

CREATE TABLE audit_log_new (
    id      INTEGER PRIMARY KEY,
    user_id TEXT    REFERENCES users_new (id) ON DELETE SET NULL,
    action  TEXT    NOT NULL,             -- login, login_failed, logout, password_reset, user_created, ...
    detail  TEXT    NOT NULL DEFAULT '',  -- e.g. the username a failed login tried
    ip      TEXT    NOT NULL,
    at      INTEGER NOT NULL              -- Unix seconds
) STRICT;

INSERT INTO audit_log_new (id, user_id, action, detail, ip, at)
SELECT id, CAST(user_id AS TEXT), action, detail, ip, at FROM audit_log;

-- Drop the old child tables first, so dropping the old users table can't
-- cascade into anything.
DROP TABLE sessions;
DROP TABLE audit_log;
DROP TABLE users;

-- Renaming users_new also updates the REFERENCES in the new child tables.
ALTER TABLE users_new RENAME TO users;
ALTER TABLE sessions_new RENAME TO sessions;
ALTER TABLE audit_log_new RENAME TO audit_log;

CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);
CREATE INDEX audit_log_at ON audit_log (at);
