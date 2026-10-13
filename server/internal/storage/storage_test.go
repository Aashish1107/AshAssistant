package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateUserDir(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"1", "V1StGXR8_Z5jdHi6B-myT"} {
		dir, err := CreateUserDir(root, id)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(root, "users", id); dir != want {
			t.Errorf("dir = %q, want %q", dir, want)
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("user folder not created: %v", err)
		}
		// Creating it again is fine.
		if _, err := CreateUserDir(root, id); err != nil {
			t.Fatalf("second CreateUserDir = %v", err)
		}
	}
}

func TestUserDirRejectsUnsafeIDs(t *testing.T) {
	for _, id := range []string{"", "..", "../2", "a/b", `a\b`, "a b", "1:stream"} {
		if dir, err := UserDir("/data", id); err == nil {
			t.Errorf("UserDir(%q) = %q, want an error", id, dir)
		}
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
