-- name: AddAuditEntry :exec
INSERT INTO audit_log (user_id, action, detail, ip, at)
VALUES (?, ?, ?, ?, ?);
