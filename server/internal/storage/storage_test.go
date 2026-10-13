package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateUserDir(t *testing.T) {
	root := t.TempDir()
	dir, err := CreateUserDir(root, 7)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "users", "7"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("user folder not created: %v", err)
	}
	// Creating it again is fine.
	if _, err := CreateUserDir(root, 7); err != nil {
		t.Fatalf("second CreateUserDir = %v", err)
	}
}

func TestCheckWritableLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	if err := CheckWritable(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("CheckWritable left %d file(s) behind", len(entries))
	}
}

func TestCheckWritableFailsForMissingFolder(t *testing.T) {
	if err := CheckWritable(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Fatal("CheckWritable on a missing folder = nil, want an error")
	}
}
