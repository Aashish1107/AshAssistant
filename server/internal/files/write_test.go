package files

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const uploadID = "V1StGXR8_Z5jdHi6B-myT"

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestMakeFolder(t *testing.T) {
	store, home := newTestStore(t)
	entry, err := store.MakeFolder("1", "/Photos/2025")
	if err != nil {
		t.Fatal(err)
	}
	if !entry.IsFolder || entry.Path != "/Photos/2025" || entry.Name != "2025" {
		t.Errorf("entry = %+v", entry)
	}
	if !exists(filepath.Join(home, "Photos", "2025")) {
		t.Error("folder not created")
	}
	for path, want := range map[string]error{
		"/Photos/2025":       ErrExists,
		"/Photos":            ErrExists,
		"/":                  ErrExists,
		"/Missing/Child":     ErrNotFound,
		"/notes.txt/Child":   ErrNotAFolder,
		"/bad:name":          ErrBadPath,
		"/.ashcloud-trash":   ErrBadPath,
		"/.AshCloud-Uploads": ErrBadPath,
	} {
		if _, err := store.MakeFolder("1", path); !errors.Is(err, want) {
			t.Errorf("MakeFolder(%q) = %v, want %v", path, err, want)
		}
	}
}

func TestMove(t *testing.T) {
	store, home := newTestStore(t)

	// Rename a file.
	entry, err := store.Move("1", "/notes.txt", "/Notes 2026.txt")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Path != "/Notes 2026.txt" || entry.Size != 5 {
		t.Errorf("entry = %+v", entry)
	}
	if exists(filepath.Join(home, "notes.txt")) || !exists(filepath.Join(home, "Notes 2026.txt")) {
		t.Error("rename didn't happen on disk")
	}

	// Move a folder, with its contents, into another folder.
	if _, err := store.Move("1", "/Videos", "/Photos/Videos"); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(home, "Photos", "Videos", "trip.mov")) {
		t.Error("folder contents didn't move")
	}

	// Change only the case of a name (the same file on Windows).
	if _, err := store.Move("1", "/Photos/apple.png", "/Photos/Apple.png"); err != nil {
		t.Errorf("case-only rename: %v", err)
	}

	for _, tc := range []struct {
		from, to string
		want     error
	}{
		{"/Photos/beach.jpg", "/Photos/Zebra.png", ErrExists}, // never overwrites
		{"/Photos/beach.jpg", "/Photos/beach.jpg", ErrExists},
		{"/Photos", "/Photos/2024/Photos", ErrIntoItself},
		{"/Photos", "/photos/inside", ErrIntoItself}, // case-insensitively too
		{"/missing.txt", "/x.txt", ErrNotFound},
		{"/Photos/beach.jpg", "/Missing/beach.jpg", ErrNotFound},
		{"/", "/Root", ErrBadPath},
		{"/Photos/beach.jpg", "/", ErrBadPath},
		{"/Photos/beach.jpg", "/../escape.jpg", ErrBadPath},
		{"/Photos/beach.jpg", "/.ashcloud-trash/x.jpg", ErrBadPath},
	} {
		if _, err := store.Move("1", tc.from, tc.to); !errors.Is(err, tc.want) {
			t.Errorf("Move(%q, %q) = %v, want %v", tc.from, tc.to, err, tc.want)
		}
	}
}

func TestTrashIsHiddenAndCanBeEmptied(t *testing.T) {
	store, home := newTestStore(t)
	isFolder, err := store.Trash("1", "/Photos", uploadID)
	if err != nil {
		t.Fatal(err)
	}
	if !isFolder {
		t.Error("Photos reported as a file")
	}
	if exists(filepath.Join(home, "Photos")) {
		t.Error("Photos still in place")
	}
	if !exists(filepath.Join(home, trashDir, uploadID, "Photos", "beach.jpg")) {
		t.Error("Photos not in the trash with its contents")
	}

	_, entries, err := store.List("1", "/")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(entries), ","); got != "Videos,notes.txt" {
		t.Errorf("listing = %s; the trash folder must be hidden", got)
	}
	if _, _, err := store.List("1", "/.ashcloud-trash"); !errors.Is(err, ErrBadPath) {
		t.Errorf("listing the trash directly = %v, want ErrBadPath", err)
	}

	if err := store.EmptyTrashItem("1", uploadID); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(home, trashDir, uploadID)) {
		t.Error("trash item not deleted")
	}

	for path, want := range map[string]error{"/": ErrBadPath, "/missing": ErrNotFound} {
		if _, err := store.Trash("1", path, "AAAAAAAAAAAAAAAAAAAAA"); !errors.Is(err, want) {
			t.Errorf("Trash(%q) = %v, want %v", path, err, want)
		}
	}
	if _, err := store.Trash("1", "/notes.txt", "../escape"); !errors.Is(err, ErrBadPath) {
		t.Errorf("unsafe trash id accepted: %v", err)
	}
}

func TestUploadLifecycle(t *testing.T) {
	store, home := newTestStore(t)
	if err := store.CheckUploadTarget("1", "/Photos/new.jpg"); err != nil {
		t.Fatal(err)
	}

	file, size, err := store.OpenUpload("1", uploadID, true)
	if err != nil || size != 0 {
		t.Fatalf("create: %v, size %d", err, size)
	}
	file.Write([]byte("hello "))
	file.Close()

	// Resume: reopen and append.
	file, size, err = store.OpenUpload("1", uploadID, false)
	if err != nil || size != 6 {
		t.Fatalf("resume: %v, size %d", err, size)
	}
	file.Write([]byte("world"))
	file.Close()
	if size, err := store.UploadSize("1", uploadID); err != nil || size != 11 {
		t.Fatalf("UploadSize = %d, %v", size, err)
	}

	// The partial file is invisible.
	_, entries, _ := store.List("1", "/")
	if strings.Contains(strings.Join(names(entries), ","), "ashcloud") {
		t.Errorf("uploads folder visible: %v", names(entries))
	}

	taken := time.Date(2024, 7, 14, 9, 30, 0, 0, time.UTC)
	entry, err := store.FinishUpload("1", uploadID, "/Photos/new.jpg", taken)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Path != "/Photos/new.jpg" || entry.Size != 11 || entry.ContentType != "image/jpeg" || !entry.Modified.Equal(taken) {
		t.Errorf("entry = %+v", entry)
	}
	body, _ := os.ReadFile(filepath.Join(home, "Photos", "new.jpg"))
	if string(body) != "hello world" {
		t.Errorf("file = %q", body)
	}
	if _, err := store.UploadSize("1", uploadID); !errors.Is(err, ErrNotFound) {
		t.Errorf("partial file still there: %v", err)
	}
}

func TestFinishUploadNeverOverwrites(t *testing.T) {
	store, home := newTestStore(t)
	upload := func(id, content string) Entry {
		t.Helper()
		file, _, err := store.OpenUpload("1", id, true)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(file, content)
		file.Close()
		entry, err := store.FinishUpload("1", id, "/Photos/beach.jpg", time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	first := upload("AAAAAAAAAAAAAAAAAAAAA", "second beach")
	second := upload("BBBBBBBBBBBBBBBBBBBBB", "third beach")
	if first.Path != "/Photos/beach (1).jpg" || second.Path != "/Photos/beach (2).jpg" {
		t.Errorf("paths = %q, %q", first.Path, second.Path)
	}
	if body, _ := os.ReadFile(filepath.Join(home, "Photos", "beach.jpg")); string(body) != "jpeg bytes" {
		t.Errorf("the original was overwritten: %q", body)
	}
}

func TestParallelUploadsWithTheSameNameAllSurvive(t *testing.T) {
	store, home := newTestStore(t)
	ids := []string{"AAAAAAAAAAAAAAAAAAAA1", "AAAAAAAAAAAAAAAAAAAA2", "AAAAAAAAAAAAAAAAAAAA3", "AAAAAAAAAAAAAAAAAAAA4"}
	for _, id := range ids {
		file, _, _ := store.OpenUpload("1", id, true)
		io.WriteString(file, id)
		file.Close()
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Go(func() {
			if _, err := store.FinishUpload("1", id, "/Videos/same.mov", time.Time{}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	matches, _ := filepath.Glob(filepath.Join(home, "Videos", "same*.mov"))
	if len(matches) != len(ids) {
		t.Errorf("%d files after %d uploads with one name: %v", len(matches), len(ids), matches)
	}
}

func TestUploadErrors(t *testing.T) {
	store, _ := newTestStore(t)
	if err := store.CheckUploadTarget("1", "/Missing/x.jpg"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing folder: %v", err)
	}
	if err := store.CheckUploadTarget("1", "/notes.txt/x.jpg"); !errors.Is(err, ErrNotAFolder) {
		t.Errorf("file as folder: %v", err)
	}
	if err := store.CheckUploadTarget("1", "/"); !errors.Is(err, ErrBadPath) {
		t.Errorf("top folder as file: %v", err)
	}
	if _, _, err := store.OpenUpload("1", uploadID, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("resume of an unknown upload: %v", err)
	}
	for _, bad := range []string{"short", "../../../etc/passwd", "has space in it ok?", strings.Repeat("a", 65)} {
		if _, _, err := store.OpenUpload("1", bad, true); !errors.Is(err, ErrBadPath) {
			t.Errorf("upload id %q accepted: %v", bad, err)
		}
	}
	if err := store.RemoveUpload("1", uploadID); err != nil {
		t.Errorf("removing a missing upload: %v", err)
	}
}

func TestRestore(t *testing.T) {
	store, home := newTestStore(t)
	trashID := "TTTTTTTTTTTTTTTTTTTTT"
	if _, err := store.Trash("1", "/Photos/beach.jpg", trashID); err != nil {
		t.Fatal(err)
	}
	entry, err := store.Restore("1", trashID, "/Photos/beach.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Path != "/Photos/beach.jpg" || !exists(filepath.Join(home, "Photos", "beach.jpg")) {
		t.Errorf("restored to %+v", entry)
	}
	if exists(filepath.Join(home, trashDir, trashID)) {
		t.Error("empty trash holder left behind")
	}

	// The name was taken meanwhile, and the folder was deleted: both handled.
	if _, err := store.Trash("1", "/Videos", trashID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MakeFolder("1", "/Videos"); err != nil {
		t.Fatal(err)
	}
	if entry, err := store.Restore("1", trashID, "/Videos"); err != nil || entry.Path != "/Videos (1)" {
		t.Errorf("Restore over a taken name = %+v, %v", entry, err)
	}
	if _, err := store.Trash("1", "/Photos", trashID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Trash("1", "/notes.txt", "UUUUUUUUUUUUUUUUUUUUU"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore("1", "UUUUUUUUUUUUUUUUUUUUU", "/Photos/2024/notes.txt"); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(home, "Photos", "2024", "notes.txt")) {
		t.Error("restore didn't recreate the deleted folder")
	}
	if _, err := store.Restore("1", "VVVVVVVVVVVVVVVVVVVVV", "/x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("restoring an unknown item: %v", err)
	}
}

func TestFreeSpace(t *testing.T) {
	store, _ := newTestStore(t)
	free, err := store.FreeSpace()
	if err != nil || free == 0 {
		t.Errorf("FreeSpace = %d, %v", free, err)
	}
}
