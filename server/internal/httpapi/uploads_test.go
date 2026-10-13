package httpapi

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/media"
)

const testUploadID = "Upl0ad-1D_for-tests00"

func uploadURL(id, path string, size int) string {
	return fmt.Sprintf("/api/v1/uploads/%s?path=%s&size=%d&modified=1735120800000", id, url.QueryEscape(path), size)
}

func TestUploadRoutesNeedLoginAndTheCSRFHeader(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	target := uploadURL(testUploadID, "/Photos/a.jpg", 5)
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		rec := send(t, router, request{method: method, path: target, body: "hello"})
		wantError(t, rec, rec.Body.Bytes(), http.StatusUnauthorized, "not_logged_in")
	}
	cookie := login(t, router, "aashish", adminPassword)
	rec := send(t, router, request{method: "PUT", path: target, body: "hello", cookie: cookie, headers: map[string]string{"X-Requested-With": ""}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("PUT without the CSRF header = %d", rec.Code)
	}
}

func TestUploadInOneRequest(t *testing.T) {
	router, _, home := buildTestRouter(t, auth.Options{}, media.Tools{}, media.ErrNoFFmpeg)
	cookie := login(t, router, "aashish", adminPassword)

	rec := send(t, router, request{method: "PUT", path: uploadURL(testUploadID, "/Photos/new.jpg", 11), body: "hello world",
		cookie: cookie, headers: map[string]string{"Upload-Offset": "0", "Content-Type": "application/octet-stream"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	got := decodeBody[uploadJSON](t, rec.Body.Bytes())
	if got.Offset != 11 || got.File == nil || got.File.Path != "/Photos/new.jpg" || *got.File.Size != 11 || got.File.ContentType != "image/jpeg" {
		t.Errorf("response = %+v", got)
	}
	if !got.File.Modified.Equal(time.UnixMilli(1735120800000)) {
		t.Errorf("modified = %v; the photo's own date should be kept", got.File.Modified)
	}
	if body, _ := os.ReadFile(filepath.Join(home, "Photos", "new.jpg")); string(body) != "hello world" {
		t.Errorf("file = %q", body)
	}

	// The same name again: a copy, never an overwrite.
	rec = send(t, router, request{method: "PUT", path: uploadURL("Another-upload-ID-0001", "/Photos/new.jpg", 3), body: "bye", cookie: cookie})
	if got := decodeBody[uploadJSON](t, rec.Body.Bytes()); rec.Code != http.StatusCreated || got.File.Path != "/Photos/new (1).jpg" {
		t.Errorf("second upload = %d %+v", rec.Code, got)
	}
}

func TestOpenThenResume(t *testing.T) {
	router, _, home := buildTestRouter(t, auth.Options{}, media.Tools{}, media.ErrNoFFmpeg)
	cookie := login(t, router, "aashish", adminPassword)
	target := uploadURL(testUploadID, "/Videos/clip.mp4", 10)

	rec := send(t, router, request{method: "POST", path: target, cookie: cookie})
	if got := decodeBody[uploadJSON](t, rec.Body.Bytes()); rec.Code != http.StatusOK || got.Offset != 0 || got.File != nil {
		t.Fatalf("open = %d %s", rec.Code, rec.Body)
	}
	// A body that ends early is kept.
	rec = send(t, router, request{method: "PUT", path: target, body: "01234", cookie: cookie, headers: map[string]string{"Upload-Offset": "0"}})
	if rec.Code != http.StatusOK || rec.Header().Get("Upload-Offset") != "5" {
		t.Fatalf("first half = %d %s", rec.Code, rec.Body)
	}
	rec = send(t, router, request{method: "POST", path: target, cookie: cookie})
	if got := decodeBody[uploadJSON](t, rec.Body.Bytes()); got.Offset != 5 {
		t.Fatalf("open after first half = %s", rec.Body)
	}
	// Continuing from the wrong place says where to continue.
	rec = send(t, router, request{method: "PUT", path: target, body: "789", cookie: cookie, headers: map[string]string{"Upload-Offset": "7"}})
	wantError(t, rec, rec.Body.Bytes(), http.StatusConflict, "wrong_offset")
	if rec.Header().Get("Upload-Offset") != "5" {
		t.Errorf("Upload-Offset = %q", rec.Header().Get("Upload-Offset"))
	}
	rec = send(t, router, request{method: "PUT", path: target, body: "56789", cookie: cookie, headers: map[string]string{"Upload-Offset": "5"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("second half = %d %s", rec.Code, rec.Body)
	}
	if body, _ := os.ReadFile(filepath.Join(home, "Videos", "clip.mp4")); string(body) != "0123456789" {
		t.Errorf("file = %q", body)
	}
	// Asking again (the "done" reply was lost) says it's done.
	rec = send(t, router, request{method: "POST", path: target, cookie: cookie})
	if got := decodeBody[uploadJSON](t, rec.Body.Bytes()); got.File == nil || got.Offset != 10 {
		t.Errorf("open after finishing = %s", rec.Body)
	}
}

func TestUploadErrors(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	cookie := login(t, router, "aashish", adminPassword)
	for _, tc := range []struct {
		method, path, body, offset string
		status                     int
		code                       string
	}{
		{"PUT", uploadURL(testUploadID, "/Photos/a.jpg", 3), "too long", "0", 400, "too_large"},
		{"PUT", uploadURL(testUploadID, "/Photos/a.jpg", 10), "rest", "5", 404, "upload_not_found"},
		{"PUT", uploadURL(testUploadID, "/Photos/a.jpg", 10), "x", "nope", 400, "bad_offset"},
		{"PUT", "/api/v1/uploads/" + testUploadID + "?path=/a.jpg&size=big", "x", "0", 400, "bad_size"},
		{"POST", "/api/v1/uploads/" + testUploadID + "?path=/a.jpg&size=1&modified=yesterday", "", "", 400, "bad_modified"},
		{"POST", uploadURL(testUploadID, "/Missing/a.jpg", 1), "", "", 404, "not_found"},
		{"POST", uploadURL(testUploadID, "/Videos/trip.mov/a.jpg", 1), "", "", 400, "not_a_folder"},
		{"POST", uploadURL(testUploadID, "/../a.jpg", 1), "", "", 400, "bad_path"},
		{"POST", uploadURL("short", "/a.jpg", 1), "", "", 400, "bad_path"},
		{"POST", uploadURL(testUploadID, "/a.jpg", 1<<62), "", "", 507, "disk_full"},
	} {
		headers := map[string]string{}
		if tc.offset != "" {
			headers["Upload-Offset"] = tc.offset
		}
		rec := send(t, router, request{method: tc.method, path: tc.path, body: tc.body, cookie: cookie, headers: headers})
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"`+tc.code+`"`) {
			t.Errorf("%s %s = %d %s, want %d %s", tc.method, tc.path, rec.Code, rec.Body, tc.status, tc.code)
		}
	}
	// A different file under an ID that's in use.
	send(t, router, request{method: "POST", path: uploadURL(testUploadID, "/Photos/a.jpg", 10), cookie: cookie})
	rec := send(t, router, request{method: "POST", path: uploadURL(testUploadID, "/Photos/a.jpg", 11), cookie: cookie})
	wantError(t, rec, rec.Body.Bytes(), http.StatusConflict, "upload_mismatch")
}

func TestCancelUpload(t *testing.T) {
	router, _, home := buildTestRouter(t, auth.Options{}, media.Tools{}, media.ErrNoFFmpeg)
	cookie := login(t, router, "aashish", adminPassword)
	target := uploadURL(testUploadID, "/Photos/a.jpg", 10)
	send(t, router, request{method: "PUT", path: target, body: "01234", cookie: cookie})
	rec := send(t, router, request{method: "DELETE", path: "/api/v1/uploads/" + testUploadID, cookie: cookie})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel = %d %s", rec.Code, rec.Body)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".ashcloud-uploads")); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestFolderMoveDeleteRestore(t *testing.T) {
	router, _, home := buildTestRouter(t, auth.Options{}, media.Tools{}, media.ErrNoFFmpeg)
	cookie := login(t, router, "aashish", adminPassword)

	rec := send(t, router, request{method: "POST", path: "/api/v1/files/folder", body: `{"path": "/Photos/2026"}`, cookie: cookie})
	if got := decodeBody[entryJSON](t, rec.Body.Bytes()); rec.Code != http.StatusCreated || got.Path != "/Photos/2026" || got.Type != "folder" {
		t.Fatalf("new folder = %d %s", rec.Code, rec.Body)
	}
	rec = send(t, router, request{method: "POST", path: "/api/v1/files/folder", body: `{"path": "/Photos/2026"}`, cookie: cookie})
	wantError(t, rec, rec.Body.Bytes(), http.StatusConflict, "exists")

	rec = send(t, router, request{method: "POST", path: "/api/v1/files/move", body: `{"from": "/Photos/beach.jpg", "to": "/Photos/2026/Beach day.jpg"}`, cookie: cookie})
	if got := decodeBody[entryJSON](t, rec.Body.Bytes()); rec.Code != http.StatusOK || got.Path != "/Photos/2026/Beach day.jpg" {
		t.Fatalf("move = %d %s", rec.Code, rec.Body)
	}
	rec = send(t, router, request{method: "POST", path: "/api/v1/files/move", body: `{"from": "/Photos", "to": "/Photos/2026/Photos"}`, cookie: cookie})
	wantError(t, rec, rec.Body.Bytes(), http.StatusBadRequest, "into_itself")
	rec = send(t, router, request{method: "POST", path: "/api/v1/files/move", body: `{"from": "/Docs/scan.pdf", "to": "/Docs/archive.zip"}`, cookie: cookie})
	wantError(t, rec, rec.Body.Bytes(), http.StatusConflict, "exists")

	rec = send(t, router, request{method: "DELETE", path: filesURL("/Photos"), cookie: cookie})
	deleted := decodeBody[deletedJSON](t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || deleted.TrashID == "" {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	rec = send(t, router, request{method: "GET", path: filesURL("/"), cookie: cookie})
	for _, entry := range decodeBody[listingJSON](t, rec.Body.Bytes()).Entries {
		if entry.Name == "Photos" || strings.HasPrefix(entry.Name, ".ashcloud") {
			t.Errorf("listing shows %q after the delete", entry.Name)
		}
	}
	rec = send(t, router, request{method: "DELETE", path: filesURL("/"), cookie: cookie})
	wantError(t, rec, rec.Body.Bytes(), http.StatusBadRequest, "bad_path")

	rec = send(t, router, request{method: "POST", path: "/api/v1/trash/restore", body: `{"id": "` + deleted.TrashID + `"}`, cookie: cookie})
	if got := decodeBody[entryJSON](t, rec.Body.Bytes()); rec.Code != http.StatusOK || got.Path != "/Photos" {
		t.Fatalf("restore = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(home, "Photos", "2026", "Beach day.jpg")); err != nil {
		t.Errorf("restored folder lost its contents: %v", err)
	}
	rec = send(t, router, request{method: "POST", path: "/api/v1/trash/restore", body: `{"id": "` + deleted.TrashID + `"}`, cookie: cookie})
	wantError(t, rec, rec.Body.Bytes(), http.StatusNotFound, "not_in_trash")

	// Another user can't touch your files, or restore your trash.
	guest := login(t, router, "guest", guestPassword)
	rec = send(t, router, request{method: "DELETE", path: filesURL("/Docs"), cookie: guest})
	wantError(t, rec, rec.Body.Bytes(), http.StatusNotFound, "not_found")
	rec = send(t, router, request{method: "DELETE", path: filesURL("/Docs"), cookie: cookie})
	trashID := decodeBody[deletedJSON](t, rec.Body.Bytes()).TrashID
	rec = send(t, router, request{method: "POST", path: "/api/v1/trash/restore", body: `{"id": "` + trashID + `"}`, cookie: guest})
	wantError(t, rec, rec.Body.Bytes(), http.StatusNotFound, "not_in_trash")
}

// realServer runs the router on a real connection, which the stall and
// takeover tests need.
func realServer(t *testing.T) (string, string, *http.Cookie) {
	t.Helper()
	router, _, home := buildTestRouter(t, auth.Options{}, media.Tools{}, media.ErrNoFFmpeg)
	cookie := login(t, router, "aashish", adminPassword)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server.URL, home, cookie
}

func newUploadRequest(t *testing.T, base string, cookie *http.Cookie, method, target string, offset int64, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, base+target, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", cookie.Name+"="+cookie.Value)
	req.Header.Set("X-Requested-With", "ashcloud")
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	return req
}

func openOffset(t *testing.T, base string, cookie *http.Cookie, target string) int64 {
	t.Helper()
	resp, err := http.DefaultClient.Do(newUploadRequest(t, base, cookie, "POST", target, 0, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open = %d %s", resp.StatusCode, body)
	}
	return decodeBody[uploadJSON](t, body).Offset
}

func TestAStalledConnectionIsNoticed(t *testing.T) {
	defer func(old time.Duration) { uploadIdleTimeout = old }(uploadIdleTimeout)
	uploadIdleTimeout = 300 * time.Millisecond
	base, home, cookie := realServer(t)

	content := make([]byte, 3<<20)
	rand.Read(content)
	target := uploadURL(testUploadID, "/Videos/big.mp4", len(content))

	// The phone sends 1 MiB, then loses signal without the connection closing.
	reader, writer := io.Pipe()
	defer writer.Close()
	go writer.Write(content[:1<<20])
	req := newUploadRequest(t, base, cookie, "PUT", target, 0, reader)
	req.ContentLength = int64(len(content))
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the server took %v to notice", elapsed)
	}

	offset := openOffset(t, base, cookie, target)
	if offset != 1<<20 {
		t.Fatalf("resume point = %d, want %d", offset, 1<<20)
	}
	resp, err = http.DefaultClient.Do(newUploadRequest(t, base, cookie, "PUT", target, offset, bytes.NewReader(content[offset:])))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("resumed upload = %d", resp.StatusCode)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "Videos", "big.mp4")); !bytes.Equal(got, content) {
		t.Error("the resumed file's content is wrong")
	}
}

func TestANewConnectionTakesOverAtOnce(t *testing.T) {
	base, home, cookie := realServer(t) // the idle timeout stays at 60 s
	content := make([]byte, 2<<20)
	rand.Read(content)
	target := uploadURL(testUploadID, "/Videos/big.mp4", len(content))

	// The old connection sent half, then went quiet.
	reader, writer := io.Pipe()
	defer writer.Close()
	go writer.Write(content[:1<<20])
	old := newUploadRequest(t, base, cookie, "PUT", target, 0, reader)
	old.ContentLength = int64(len(content))
	oldStatus := make(chan int, 1)
	go func() {
		resp, err := http.DefaultClient.Do(old)
		if err != nil {
			oldStatus <- 0
			return
		}
		resp.Body.Close()
		oldStatus <- resp.StatusCode
	}()
	deadline := time.Now().Add(5 * time.Second)
	for openOffset(t, base, cookie, target) < 1<<20 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// The phone, now on mobile data, continues on a new connection.
	start := time.Now()
	resp, err := http.DefaultClient.Do(newUploadRequest(t, base, cookie, "PUT", target, 1<<20, bytes.NewReader(content[1<<20:])))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("new connection = %d", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("taking over took %v; it should not wait for the old connection to time out", elapsed)
	}
	if status := <-oldStatus; status != http.StatusConflict && status != 0 {
		t.Errorf("old connection got %d, want 409 or a closed connection", status)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "Videos", "big.mp4")); !bytes.Equal(got, content) {
		t.Error("content is wrong after a takeover")
	}
}
