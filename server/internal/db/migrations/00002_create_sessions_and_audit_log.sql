-- +goose Up
CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   BLOB    NOT NULL UNIQUE,  -- SHA-256 of the cookie; the token itself is never stored
    user_agent   TEXT    NOT NULL,
    ip           TEXT    NOT NULL,         -- last address the session was used from
    created_at   INTEGER NOT NULL,         -- Unix seconds
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);

CREATE TABLE audit_log (
    id      INTEGER PRIMARY KEY,
    user_id INTEGER REFERENCES users (id) ON DELETE SET NULL,
    action  TEXT    NOT NULL,             -- login, login_failed, logout, password_reset, ...
    detail  TEXT    NOT NULL DEFAULT '',  -- e.g. the username a failed login tried
    ip      TEXT    NOT NULL,
    at      INTEGER NOT NULL              -- Unix seconds
) STRICT;

CREATE INDEX audit_log_at ON audit_log (at);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE sessions;
