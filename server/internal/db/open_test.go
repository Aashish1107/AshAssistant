package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
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

	id, err := queries.CreateUser(ctx, CreateUserParams{Username: "aashish", PasswordHash: "h", Role: "admin", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Errorf("first user id = %d, want 1", id)
	}
	count, err := queries.CountUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("CountUsers = %d, want 1", count)
	}

	_, err = queries.CreateUser(ctx, CreateUserParams{Username: "aashish", PasswordHash: "h", Role: "admin", CreatedAt: 2})
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
	_, err := New(database).CreateUser(ctx, CreateUserParams{Username: "x", PasswordHash: "h", Role: "superuser", CreatedAt: 1})
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
