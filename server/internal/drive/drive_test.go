package drive

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/db"
	"github.com/Aashish1107/AshAssistant/server/internal/files"
)

const (
	userID   = auth.FirstAdminID
	uploadID = "Upl0ad-1D_for-tests00"
)

var client = auth.Client{IP: "100.64.0.7"}

type fixture struct {
	service  *Service
	queries  *db.Queries
	database *sql.DB
	home     string
}

func newFixture(t *testing.T, opts Options) fixture {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := db.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	queries := db.New(database)
	if err := queries.CreateUser(ctx, db.CreateUserParams{ID: userID, Username: "aashish", PasswordHash: "x", Role: auth.RoleAdmin, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	storageDir := t.TempDir()
	home := filepath.Join(storageDir, "users", userID)
	if err := os.MkdirAll(filepath.Join(home, "Photos"), 0o700); err != nil {
		t.Fatal(err)
	}
	if opts.SyncEvery == 0 {
		opts.SyncEvery = 1000
	}
	return fixture{service: NewService(database, files.NewStore(storageDir), opts), queries: queries, database: database, home: home}
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func photo(size int64) Upload {
	return Upload{ID: uploadID, Path: "/Photos/IMG_0001.jpg", Size: size}
}

func noStop() {}

func (f fixture) write(t *testing.T, up Upload, body io.Reader) (Result, error) {
	t.Helper()
	return f.service.Write(context.Background(), userID, client, up, body, noStop)
}

func (f fixture) partSize(t *testing.T) int64 {
	t.Helper()
	info, err := os.Stat(filepath.Join(f.home, ".ashcloud-uploads", uploadID+".part"))
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestUploadInOneRequest(t *testing.T) {
	f := newFixture(t, Options{})
	content := randomBytes(5000)
	up := photo(int64(len(content)))
	up.Modified = time.Date(2025, 12, 25, 10, 0, 0, 0, time.UTC)

	opened, err := f.service.Open(context.Background(), userID, up)
	if err != nil || opened.Upload.Offset != 0 || opened.File != nil {
		t.Fatalf("Open = %+v, %v", opened, err)
	}
	result, err := f.write(t, up, bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if result.File == nil || result.File.Path != "/Photos/IMG_0001.jpg" || result.Upload.Offset != up.Size {
		t.Fatalf("result = %+v", result)
	}
	got, _ := os.ReadFile(filepath.Join(f.home, "Photos", "IMG_0001.jpg"))
	if !bytes.Equal(got, content) {
		t.Error("the file's content is wrong")
	}
	if !result.File.Modified.Equal(up.Modified) {
		t.Errorf("modified = %v, want %v", result.File.Modified, up.Modified)
	}
	if _, err := f.queries.GetUpload(context.Background(), db.GetUploadParams{UserID: userID, ID: uploadID}); err == nil {
		t.Error("the finished upload's record is still there")
	}
	if entries, _ := os.ReadDir(filepath.Join(f.home, ".ashcloud-uploads")); len(entries) != 0 {
		t.Errorf("leftovers in the uploads folder: %v", entries)
	}
}

func TestEmptyFile(t *testing.T) {
	f := newFixture(t, Options{})
	result, err := f.write(t, photo(0), strings.NewReader(""))
	if err != nil || result.File == nil || result.File.Size != 0 {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestResumeAfterADroppedConnection(t *testing.T) {
	f := newFixture(t, Options{})
	content := randomBytes(10_000)
	up := photo(int64(len(content)))

	// The connection drops after 6000 bytes.
	dropped := io.MultiReader(bytes.NewReader(content[:6000]), iotest.ErrReader(io.ErrUnexpectedEOF))
	result, err := f.write(t, up, dropped)
	if !errors.Is(err, ErrInterrupted) || result.Upload.Offset != 6000 {
		t.Fatalf("dropped write = %+v, %v", result, err)
	}
	row, _ := f.queries.GetUpload(context.Background(), db.GetUploadParams{UserID: userID, ID: uploadID})
	if row.Synced != 6000 {
		t.Errorf("synced = %d, want 6000: an interrupted upload must flush what it got", row.Synced)
	}

	// The browser asks where to continue, then sends the rest.
	opened, err := f.service.Open(context.Background(), userID, up)
	if err != nil || opened.Upload.Offset != 6000 {
		t.Fatalf("Open = %+v, %v", opened, err)
	}
	up.Offset = 6000
	result, err = f.write(t, up, bytes.NewReader(content[6000:]))
	if err != nil || result.File == nil {
		t.Fatalf("resumed write = %+v, %v", result, err)
	}
	got, _ := os.ReadFile(filepath.Join(f.home, "Photos", "IMG_0001.jpg"))
	if !bytes.Equal(got, content) {
		t.Error("the resumed file's content is wrong")
	}
}

func TestUploadInParts(t *testing.T) {
	f := newFixture(t, Options{})
	content := randomBytes(3000)
	up := photo(3000)
	for offset := int64(0); offset < 3000; offset += 1000 {
		up.Offset = offset
		result, err := f.write(t, up, bytes.NewReader(content[offset:offset+1000]))
		if err != nil {
			t.Fatal(err)
		}
		if done := result.File != nil; done != (offset == 2000) || result.Upload.Offset != offset+1000 {
			t.Fatalf("after part at %d: %+v", offset, result)
		}
	}
	got, _ := os.ReadFile(filepath.Join(f.home, "Photos", "IMG_0001.jpg"))
	if !bytes.Equal(got, content) {
		t.Error("content is wrong")
	}
}

func TestOffsets(t *testing.T) {
	f := newFixture(t, Options{})
	content := randomBytes(4000)
	up := photo(4000)
	if _, err := f.write(t, up, bytes.NewReader(content[:3000])); err != nil {
		t.Fatal(err)
	}

	// Ahead of what arrived: refused, with the right offset.
	up.Offset = 3500
	var wrong *OffsetError
	if _, err := f.write(t, up, bytes.NewReader(content[3500:])); !errors.As(err, &wrong) || wrong.Offset != 3000 {
		t.Fatalf("write ahead = %v", err)
	}

	// Behind it (the browser can't know exactly what arrived): the extra
	// bytes are replaced.
	up.Offset = 1000
	result, err := f.write(t, up, bytes.NewReader(content[1000:]))
	if err != nil || result.File == nil {
		t.Fatalf("write behind = %+v, %v", result, err)
	}
	got, _ := os.ReadFile(filepath.Join(f.home, "Photos", "IMG_0001.jpg"))
	if !bytes.Equal(got, content) {
		t.Error("content is wrong after resending")
	}
}

func TestUploadErrors(t *testing.T) {
	f := newFixture(t, Options{})
	ctx := context.Background()
	check := func(name string, err, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", name, err, want)
		}
	}

	up := photo(10)
	up.Offset = 5
	_, err := f.write(t, up, strings.NewReader("12345"))
	check("continuing an unknown upload", err, ErrUploadNotFound)

	missing := Upload{ID: uploadID, Path: "/Missing/x.jpg", Size: 10}
	_, err = f.service.Open(ctx, userID, missing)
	check("missing folder", err, files.ErrNotFound)
	_, err = f.service.Open(ctx, userID, Upload{ID: "bad id", Path: "/x.jpg", Size: 1})
	check("bad ID", err, files.ErrBadPath)
	_, err = f.service.Open(ctx, userID, Upload{ID: uploadID, Path: "/.ashcloud-uploads/x", Size: 1})
	check("reserved name", err, files.ErrBadPath)
	_, err = f.service.Open(ctx, userID, Upload{ID: uploadID, Path: "/x.jpg", Size: -1})
	check("negative size", err, ErrBadSize)

	if _, err := f.service.Open(ctx, userID, photo(10)); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Open(ctx, userID, photo(11))
	check("same ID, different size", err, ErrUploadMismatch)
	other := photo(10)
	other.Path = "/Photos/other.jpg"
	_, err = f.service.Open(ctx, userID, other)
	check("same ID, different path", err, ErrUploadMismatch)

	full := newFixture(t, Options{KeepFree: 1 << 62})
	_, err = full.service.Open(ctx, userID, photo(10))
	check("drive full", err, files.ErrDiskFull)
}

func TestFinishedUploadIsRemembered(t *testing.T) {
	f := newFixture(t, Options{})
	up := photo(5)
	if _, err := f.write(t, up, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	// The "done" reply was lost; the browser asks again.
	opened, err := f.service.Open(context.Background(), userID, up)
	if err != nil || opened.File == nil || opened.Upload.Offset != 5 {
		t.Fatalf("Open after finishing = %+v, %v", opened, err)
	}
	result, err := f.write(t, up, strings.NewReader("hello"))
	if err != nil || result.File == nil || result.File.Path != "/Photos/IMG_0001.jpg" {
		t.Fatalf("Write after finishing = %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(f.home, "Photos", "IMG_0001 (1).jpg")); err == nil {
		t.Error("the file was uploaded twice")
	}

	// After a while it's forgotten, and the same file uploads as a copy.
	f.service.now = func() time.Time { return time.Now().Add(rememberFinished + time.Minute) }
	result, err = f.write(t, up, strings.NewReader("hello"))
	if err != nil || result.File == nil || result.File.Path != "/Photos/IMG_0001 (1).jpg" {
		t.Fatalf("upload much later = %+v, %v", result, err)
	}
}

// blockingBody is a request body whose reads wait until stop is called, like
// a connection that died without closing.
type blockingBody struct {
	first   []byte
	stopped chan struct{}
}

func (b *blockingBody) Read(p []byte) (int, error) {
	if len(b.first) > 0 {
		n := copy(p, b.first)
		b.first = b.first[n:]
		return n, nil
	}
	<-b.stopped
	return 0, errors.New("read deadline exceeded")
}

func TestANewRequestTakesOver(t *testing.T) {
	f := newFixture(t, Options{})
	content := randomBytes(8000)
	up := photo(8000)

	stuck := &blockingBody{first: content[:2000], stopped: make(chan struct{})}
	firstDone := make(chan error, 1)
	go func() {
		_, err := f.service.Write(context.Background(), userID, client, up, stuck, func() { close(stuck.stopped) })
		firstDone <- err
	}()
	// Wait until the first request is stuck after its 2000 bytes.
	deadline := time.Now().Add(5 * time.Second)
	for !f.service.receiving(uploadKey{userID, uploadID}) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	// The phone switched networks and sends everything again.
	result, err := f.write(t, up, bytes.NewReader(content))
	if err != nil || result.File == nil {
		t.Fatalf("second request = %+v, %v", result, err)
	}
	if err := <-firstDone; !errors.Is(err, ErrTakenOver) {
		t.Errorf("first request = %v, want ErrTakenOver", err)
	}
	got, _ := os.ReadFile(filepath.Join(f.home, "Photos", "IMG_0001.jpg"))
	if !bytes.Equal(got, content) {
		t.Error("content is wrong after a takeover")
	}
}

func TestStopUploads(t *testing.T) {
	f := newFixture(t, Options{})
	stuck := &blockingBody{first: randomBytes(1500), stopped: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := f.service.Write(context.Background(), userID, client, photo(5000), stuck, func() { close(stuck.stopped) })
		done <- err
	}()
	for !f.service.receiving(uploadKey{userID, uploadID}) {
		time.Sleep(time.Millisecond)
	}
	f.service.StopUploads()
	if err := <-done; !errors.Is(err, ErrInterrupted) {
		t.Errorf("stopped upload = %v, want ErrInterrupted", err)
	}
	row, _ := f.queries.GetUpload(context.Background(), db.GetUploadParams{UserID: userID, ID: uploadID})
	if row.Synced != 1500 {
		t.Errorf("synced = %d, want 1500", row.Synced)
	}
}

func TestRecoverCutsBackToWhatWasFlushed(t *testing.T) {
	f := newFixture(t, Options{SyncEvery: 1 << 30}) // no flushes while receiving
	up := photo(10_000)
	dropped := io.MultiReader(bytes.NewReader(randomBytes(4000)), iotest.ErrReader(io.ErrUnexpectedEOF))
	if _, err := f.write(t, up, dropped); !errors.Is(err, ErrInterrupted) {
		t.Fatal(err)
	}
	// Simulate a power cut: the part file grew, but its record says only
	// 4000 bytes reached the disk.
	part, _ := os.OpenFile(filepath.Join(f.home, ".ashcloud-uploads", uploadID+".part"), os.O_WRONLY|os.O_APPEND, 0)
	part.Write(make([]byte, 3000))
	part.Close()

	if err := f.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if size := f.partSize(t); size != 4000 {
		t.Errorf("after Recover the part file has %d bytes, want 4000", size)
	}
	opened, _ := f.service.Open(context.Background(), userID, up)
	if opened.Upload.Offset != 4000 {
		t.Errorf("resume point = %d", opened.Upload.Offset)
	}
}

func TestBackgroundFlushesRecordProgress(t *testing.T) {
	f := newFixture(t, Options{SyncEvery: 1})
	stuck := &blockingBody{first: randomBytes(2 * bufferSize), stopped: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.service.Write(context.Background(), userID, client, photo(3*bufferSize), stuck, func() { close(stuck.stopped) })
	}()
	defer func() { f.service.StopUploads(); <-done }()

	// The body hasn't ended and nothing has been interrupted, yet the
	// progress made so far is already safe.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		row, _ := f.queries.GetUpload(context.Background(), db.GetUploadParams{UserID: userID, ID: uploadID})
		if row.Synced >= bufferSize {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("no progress recorded while receiving")
}

func TestCancel(t *testing.T) {
	f := newFixture(t, Options{})
	up := photo(100)
	if _, err := f.write(t, up, strings.NewReader("partial")); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Cancel(context.Background(), userID, uploadID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".ashcloud-uploads", uploadID+".part")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("part file still there: %v", err)
	}
	up.Offset = 7
	if _, err := f.write(t, up, strings.NewReader("rest")); !errors.Is(err, ErrUploadNotFound) {
		t.Errorf("continuing a cancelled upload: %v", err)
	}
}

func TestFolderRemovedDuringUpload(t *testing.T) {
	f := newFixture(t, Options{})
	up := photo(10)
	if _, err := f.write(t, up, strings.NewReader("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Delete(context.Background(), userID, client, "/Photos"); err != nil {
		t.Fatal(err)
	}
	up.Offset = 5
	result, err := f.write(t, up, strings.NewReader("67890"))
	if err != nil || result.File == nil || result.File.Path != "/IMG_0001.jpg" {
		t.Fatalf("result = %+v, %v; want the file in the top folder", result, err)
	}
}

func TestDeleteRestoreAndCleanup(t *testing.T) {
	f := newFixture(t, Options{})
	ctx := context.Background()
	os.WriteFile(filepath.Join(f.home, "Photos", "a.jpg"), []byte("a"), 0o600)

	trashID, err := f.service.Delete(ctx, userID, client, "/Photos/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := f.service.Restore(ctx, userID, client, trashID)
	if err != nil || entry.Path != "/Photos/a.jpg" {
		t.Fatalf("Restore = %+v, %v", entry, err)
	}
	if _, err := f.service.Restore(ctx, userID, client, trashID); !errors.Is(err, ErrTrashNotFound) {
		t.Errorf("restoring twice: %v", err)
	}

	// Deleted 31 days ago: emptied for good.
	folderID, err := f.service.Delete(ctx, userID, client, "/Photos")
	if err != nil {
		t.Fatal(err)
	}
	item, _ := f.queries.GetTrash(ctx, db.GetTrashParams{UserID: userID, ID: folderID})
	if item.IsFolder != 1 || item.OriginalPath != "/Photos" {
		t.Errorf("trash record = %+v", item)
	}
	f.service.now = func() time.Time { return time.Now().Add(TrashAge + 24*time.Hour) }
	f.service.Cleanup(ctx)
	if _, err := os.Stat(filepath.Join(f.home, ".ashcloud-trash", folderID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expired trash still on disk: %v", err)
	}
	if _, err := f.service.Restore(ctx, userID, client, folderID); !errors.Is(err, ErrTrashNotFound) {
		t.Errorf("restoring emptied trash: %v", err)
	}
}

func TestCleanupRemovesAbandonedUploads(t *testing.T) {
	f := newFixture(t, Options{})
	ctx := context.Background()
	if _, err := f.write(t, photo(100), strings.NewReader("partial")); err != nil {
		t.Fatal(err)
	}
	f.service.Cleanup(ctx)
	if f.partSize(t) != 7 {
		t.Error("a recent upload was removed")
	}
	f.service.now = func() time.Time { return time.Now().Add(StaleUploadAge + time.Hour) }
	f.service.Cleanup(ctx)
	if _, err := os.Stat(filepath.Join(f.home, ".ashcloud-uploads", uploadID+".part")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("abandoned upload still there: %v", err)
	}
}

func TestChangesAreAudited(t *testing.T) {
	f := newFixture(t, Options{})
	ctx := context.Background()
	f.service.MakeFolder(ctx, userID, client, "/Photos/2026")
	f.service.Move(ctx, userID, client, "/Photos/2026", "/Photos/Christmas 2026")
	f.write(t, photo(2), strings.NewReader("hi"))
	f.service.Delete(ctx, userID, client, "/Photos/IMG_0001.jpg")

	rows, err := f.database.Query("SELECT action FROM audit_log ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action string
		rows.Scan(&action)
		actions = append(actions, action)
	}
	if got := strings.Join(actions, ","); got != "folder_created,moved,uploaded,deleted" {
		t.Errorf("audit log = %s", got)
	}
}

func TestTail(t *testing.T) {
	if got := tail("/short.jpg", 64); got != "/short.jpg" {
		t.Error(got)
	}
	long := "/" + strings.Repeat("folder/", 20) + "IMG_0001.jpg"
	got := tail(long, 64)
	if !strings.HasSuffix(got, "/IMG_0001.jpg") || !strings.HasPrefix(got, "…") || len([]rune(got)) != 64 {
		t.Errorf("tail = %q", got)
	}
}
