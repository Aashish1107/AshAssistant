package files

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClean(t *testing.T) {
	for in, want := range map[string][2]string{
		"":                        {"/", "."},
		"/":                       {"/", "."},
		"//":                      {"/", "."},
		"Photos":                  {"/Photos", "Photos"},
		"/Photos/":                {"/Photos", "Photos"},
		"/Photos//2024/beach.jpg": {"/Photos/2024/beach.jpg", "Photos/2024/beach.jpg"},
		"/My Files/résumé.pdf":    {"/My Files/résumé.pdf", "My Files/résumé.pdf"},
		"/.hidden":                {"/.hidden", ".hidden"},
		"/COMMENTS.txt":           {"/COMMENTS.txt", "COMMENTS.txt"},
	} {
		canonical, relative, err := Clean(in)
		if err != nil || canonical != want[0] || relative != want[1] {
			t.Errorf("Clean(%q) = %q, %q, %v; want %q, %q", in, canonical, relative, err, want[0], want[1])
		}
	}
}

func TestCleanRefusesUnsafePaths(t *testing.T) {
	for _, in := range []string{
		"..", "/..", "/Photos/../..", "/./Photos", `\Windows`, `Photos\..\..`,
		"/a\x00b", "/C:", "/notes.txt:secret", "/CON", "/nul.txt", "/com1", "/LPT9.log", "/COM¹",
		"/trailing.", "/trailing ", "/what?", "/a*b", "/<x>", "/a|b", "/\"q\"", "/tab\there",
		"/" + strings.Repeat("a", 256), "/\xff\xfe",
	} {
		if canonical, _, err := Clean(in); !errors.Is(err, ErrBadPath) {
			t.Errorf("Clean(%q) = %q, %v; want ErrBadPath", in, canonical, err)
		}
	}
}

func TestContentTypeAndViewing(t *testing.T) {
	for name, want := range map[string][2]string{
		"IMG_0001.MOV": {"video/quicktime", "media"},
		"clip.mp4":     {"video/mp4", "media"},
		"song.mp3":     {"audio/mpeg", "media"},
		"photo.HEIC":   {"image/heic", "media"},
		"scan.pdf":     {"application/pdf", "media"},
		"notes.txt":    {"text/plain; charset=utf-8", "text"},
		"page.html":    {"text/html; charset=utf-8", "text"}, // shown as text, never run
		"data.json":    {"application/json", "text"},
		"archive.zip":  {"application/zip", "download"},
		"setup.exe":    {"", "download"},
		"no-extension": {"application/octet-stream", "download"},
	} {
		got := ContentType(name)
		if want[0] != "" && got != want[0] {
			t.Errorf("ContentType(%q) = %q, want %q", name, got, want[0])
		}
		if view := Viewing(got); view != want[1] {
			t.Errorf("Viewing(%q) = %q, want %q", got, view, want[1])
		}
	}
}

// newTestStore makes a storage folder with user "1" holding:
//
//	Photos/ (beach.jpg, Zebra.png, apple.png, 2024/)
//	Videos/trip.mov
//	notes.txt, desktop.ini, Thumbs.db
func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	storageDir := t.TempDir()
	home := filepath.Join(storageDir, "users", "1")
	for path, body := range map[string]string{
		"Photos/beach.jpg": "jpeg bytes",
		"Photos/Zebra.png": "png",
		"Photos/apple.png": "png",
		"Videos/trip.mov":  strings.Repeat("v", 1000),
		"notes.txt":        "hello",
		"desktop.ini":      "[.ShellClassInfo]",
		"Thumbs.db":        "cache",
	} {
		full := filepath.Join(home, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(home, "Photos", "2024"), 0o700); err != nil {
		t.Fatal(err)
	}
	return NewStore(storageDir), home
}

func names(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func TestListTopFolder(t *testing.T) {
	store, _ := newTestStore(t)
	path, entries, err := store.List("1", "/")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/" {
		t.Errorf("path = %q, want /", path)
	}
	// Folders first; Windows' own desktop.ini and Thumbs.db are hidden.
	if got := strings.Join(names(entries), ","); got != "Photos,Videos,notes.txt" {
		t.Errorf("entries = %s", got)
	}
	notes := entries[2]
	if notes.Path != "/notes.txt" || notes.IsFolder || notes.Size != 5 || notes.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("notes.txt entry = %+v", notes)
	}
	if time.Since(notes.Modified) > time.Minute {
		t.Errorf("modified = %v, want recent", notes.Modified)
	}
	if photos := entries[0]; !photos.IsFolder || photos.Path != "/Photos" || photos.Size != 0 || photos.ContentType != "" {
		t.Errorf("Photos entry = %+v", photos)
	}
}

func TestListSubfolderSortsIgnoringCase(t *testing.T) {
	store, _ := newTestStore(t)
	path, entries, err := store.List("1", "Photos/")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/Photos" {
		t.Errorf("path = %q", path)
	}
	if got := strings.Join(names(entries), ","); got != "2024,apple.png,beach.jpg,Zebra.png" {
		t.Errorf("entries = %s", got)
	}
	if entries[1].Path != "/Photos/apple.png" {
		t.Errorf("path = %q", entries[1].Path)
	}
}

func TestListErrors(t *testing.T) {
	store, _ := newTestStore(t)
	for path, want := range map[string]error{
		"/Missing":   ErrNotFound,
		"/notes.txt": ErrNotAFolder,
		"/../2":      ErrBadPath,
	} {
		if _, _, err := store.List("1", path); !errors.Is(err, want) {
			t.Errorf("List(%q) = %v, want %v", path, err, want)
		}
	}
}

func TestNewUserGetsAnEmptyFolder(t *testing.T) {
	store, _ := newTestStore(t)
	path, entries, err := store.List("V1StGXR8_Z5jdHi6B-myT", "/")
	if err != nil || path != "/" || len(entries) != 0 {
		t.Fatalf("got %q %v %v, want an empty top folder", path, entries, err)
	}
}

func TestUsersCantSeeEachOthersFiles(t *testing.T) {
	store, _ := newTestStore(t)
	if _, _, err := store.List("V1StGXR8_Z5jdHi6B-myT", "/Photos"); !errors.Is(err, ErrNotFound) {
		t.Errorf("another user listed /Photos: %v", err)
	}
	if _, err := store.Open("V1StGXR8_Z5jdHi6B-myT", "/notes.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("another user opened /notes.txt: %v", err)
	}
	if _, _, err := store.List("../1", "/"); err == nil {
		t.Error("an unsafe user id was accepted")
	}
}

func TestOpen(t *testing.T) {
	store, _ := newTestStore(t)
	file, err := store.Open("1", "/Videos/trip.mov")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if file.Path != "/Videos/trip.mov" || file.Size != 1000 || file.ContentType != "video/quicktime" {
		t.Errorf("file = %+v", file)
	}
	body, _ := io.ReadAll(file)
	if len(body) != 1000 {
		t.Errorf("read %d bytes, want 1000", len(body))
	}

	for path, want := range map[string]error{
		"/Videos":       ErrIsAFolder,
		"/Videos/x.mov": ErrNotFound,
		"/":             ErrIsAFolder,
		"/C:/Windows":   ErrBadPath,
	} {
		if _, err := store.Open("1", path); !errors.Is(err, want) {
			t.Errorf("Open(%q) = %v, want %v", path, err, want)
		}
	}
}

func TestShortcutsInsideTheFolderWorkButNotOutside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows needs extra privileges")
	}
	store, home := newTestStore(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600)

	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink("Photos", filepath.Join(home, "Pictures")))                      // inside: allowed
	must(os.Symlink(outside, filepath.Join(home, "Escape")))                         // absolute, outside
	must(os.Symlink("../../../../", filepath.Join(home, "Up")))                      // relative, outside
	must(os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(home, "s"))) // file outside

	_, entries, err := store.List("1", "/")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(entries), ","); got != "Photos,Pictures,Videos,notes.txt" {
		t.Errorf("entries = %s (shortcuts pointing outside must not be listed)", got)
	}
	if _, inside, err := store.List("1", "/Pictures"); err != nil || len(inside) != 4 {
		t.Errorf("List(/Pictures) = %v, %v; want the 4 entries of Photos", inside, err)
	}
	for _, path := range []string{"/Escape", "/Escape/secret.txt", "/Up", "/s"} {
		if _, _, err := store.List("1", path); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrNotAFolder) {
			t.Errorf("List(%q) = %v, want it refused as not found", path, err)
		}
		if f, err := store.Open("1", path); err == nil {
			f.Close()
			t.Errorf("Open(%q) succeeded; it points outside the user's folder", path)
		}
	}
}
