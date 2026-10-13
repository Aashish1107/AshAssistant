package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestOpenSetsPragmas(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	for pragma, want := range map[string]string{
		"journal_mode": "wal",
		"synchronous":  "1", // NORMAL
		"busy_timeout": "5000",
		"foreign_keys": "1",
	} {
		var got string
		if err := database.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("PRAGMA %s = %s, want %s", pragma, got, want)
		}
	}
}

func TestOpenFailsForUnwritableLocation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-folder", "test.db")
	if _, err := Open(context.Background(), missing); err == nil {
		t.Fatal("Open succeeded in a folder that doesn't exist")
	}
}

func TestMigrateAppliesOnceThenIsUpToDate(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	applied, err := Migrate(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(applied, 1) {
		t.Fatalf("first Migrate applied %v, want it to include 1", applied)
	}

	applied, err = Migrate(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Fatalf("second Migrate applied %v, want nothing", applied)
	}
}

func TestCreateAndCountUsers(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	if _, err := Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	queries := New(database)

	if err := queries.CreateUser(ctx, CreateUserParams{ID: "1", Username: "aashish", PasswordHash: "h", Role: "admin", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	count, err := queries.CountUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("CountUsers = %d, want 1", count)
	}

	err = queries.CreateUser(ctx, CreateUserParams{ID: "V1StGXR8_Z5jdHi6B-myT", Username: "aashish", PasswordHash: "h", Role: "admin", CreatedAt: 2})
	if !IsUniqueViolation(err) {
		t.Errorf("duplicate username error = %v, want a unique violation", err)
	}
}

func TestUsersTableRejectsUnknownRole(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	if _, err := Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	err := New(database).CreateUser(ctx, CreateUserParams{ID: "x", Username: "x", PasswordHash: "h", Role: "superuser", CreatedAt: 1})
	if err == nil {
		t.Fatal("role 'superuser' was accepted")
	}
}

func TestCheck(t *testing.T) {
	database := openTestDB(t)
	if err := Check(context.Background(), database); err != nil {
		t.Fatalf("Check on a healthy database = %v", err)
	}
	database.Close()
	if err := Check(context.Background(), database); err == nil {
		t.Fatal("Check on a closed database = nil, want an error")
	}
}

// A database from before Nano IDs (schema version 2, integer user IDs) keeps
// all its data when upgraded, and user 1 is still "1".
func TestMigrationToTextUserIDsKeepsData(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	migrator, err := newMigrator(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.UpTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at) VALUES (1, 'aashish', 'hash', 'admin', 100)`,
		`INSERT INTO sessions (user_id, token_hash, user_agent, ip, created_at, last_seen_at, expires_at) VALUES (1, x'abcd', 'Safari', '100.64.0.7', 1, 2, 9999999999)`,
		`INSERT INTO audit_log (user_id, action, detail, ip, at) VALUES (1, 'login', '', '100.64.0.7', 3)`,
		`INSERT INTO audit_log (user_id, action, detail, ip, at) VALUES (NULL, 'login_failed', 'nobody', '100.64.0.7', 4)`,
	} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	applied, err := Migrate(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(applied, 3) {
		t.Fatalf("applied %v, want 3", applied)
	}

	queries := New(database)
	user, err := queries.GetUserByUsername(ctx, "aashish")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "1" || user.PasswordHash != "hash" || user.Role != "admin" {
		t.Errorf("user after upgrade = %+v", user)
	}
	session, err := queries.GetSession(ctx, GetSessionParams{TokenHash: []byte{0xab, 0xcd}, ExpiresAt: 0})
	if err != nil {
		t.Fatalf("session lost in upgrade: %v", err)
	}
	if session.UserID != "1" || session.Username != "aashish" {
		t.Errorf("session after upgrade = %+v", session)
	}
	var withUser, withoutUser int
	database.QueryRow(`SELECT count(*) FROM audit_log WHERE user_id = '1'`).Scan(&withUser)
	database.QueryRow(`SELECT count(*) FROM audit_log WHERE user_id IS NULL`).Scan(&withoutUser)
	if withUser != 1 || withoutUser != 1 {
		t.Errorf("audit rows after upgrade: %d for user 1, %d without user; want 1 and 1", withUser, withoutUser)
	}

	// Foreign keys still hold, and still point at the new users table.
	_, err = database.ExecContext(ctx, `INSERT INTO sessions (user_id, token_hash, user_agent, ip, created_at, last_seen_at, expires_at) VALUES ('nobody', x'01', '', '', 1, 1, 1)`)
	if err == nil {
		t.Error("a session for a missing user was accepted")
	}
	var schema string
	database.QueryRow(`SELECT sql FROM sqlite_schema WHERE name = 'sessions'`).Scan(&schema)
	if !strings.Contains(schema, `REFERENCES "users"`) && !strings.Contains(schema, "REFERENCES users") {
		t.Errorf("sessions no longer references users:\n%s", schema)
	}
	for _, index := range []string{"sessions_user_id", "sessions_expires_at", "audit_log_at"} {
		var found int
		database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = ?`, index).Scan(&found)
		if found != 1 {
			t.Errorf("index %s missing after upgrade", index)
		}
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM users WHERE id = '1'`); err != nil {
		t.Fatal(err)
	}
	var sessions, orphaned int
	database.QueryRow(`SELECT count(*) FROM sessions`).Scan(&sessions)
	database.QueryRow(`SELECT count(*) FROM audit_log WHERE user_id IS NULL`).Scan(&orphaned)
	if sessions != 0 || orphaned != 2 {
		t.Errorf("after deleting the user: %d sessions (want 0), %d audit rows without user (want 2)", sessions, orphaned)
	}
}
