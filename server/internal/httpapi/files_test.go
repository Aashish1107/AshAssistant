package httpapi

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
)

func filesURL(path string, extra ...string) string {
	return "/api/v1/files?path=" + url.QueryEscape(path) + strings.Join(extra, "")
}

func contentURL(path string, extra ...string) string {
	return "/api/v1/files/content?path=" + url.QueryEscape(path) + strings.Join(extra, "")
}

func TestFileRoutesNeedLogin(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	for _, path := range []string{filesURL("/"), contentURL("/Videos/trip.mov")} {
		rec := send(t, router, request{method: "GET", path: path})
		wantError(t, rec, rec.Body.Bytes(), http.StatusUnauthorized, "not_logged_in")
	}
}

func TestListFiles(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	cookie := login(t, router, "aashish", adminPassword)

	rec := send(t, router, request{method: "GET", path: filesURL("/"), cookie: cookie})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	listing := decodeBody[listingJSON](t, rec.Body.Bytes())
	if listing.Path != "/" || len(listing.Entries) != 3 {
		t.Fatalf("listing = %+v", listing)
	}
	docs := listing.Entries[0]
	if docs.Name != "Docs" || docs.Type != "folder" || docs.Path != "/Docs" || docs.Size != nil || docs.ContentType != "" {
		t.Errorf("first entry = %+v", docs)
	}
	// Folders have no size or content type in the JSON at all.
	if strings.Contains(rec.Body.String(), `"size": 0`) {
		t.Errorf("folders shouldn't report a size:\n%s", rec.Body)
	}

	rec = send(t, router, request{method: "GET", path: filesURL("/Videos"), cookie: cookie})
	listing = decodeBody[listingJSON](t, rec.Body.Bytes())
	video := listing.Entries[0]
	if video.Name != "trip.mov" || video.Type != "file" || video.Size == nil || *video.Size != 1000 || video.ContentType != "video/quicktime" || video.Path != "/Videos/trip.mov" {
		t.Errorf("video entry = %+v", video)
	}
}

func TestListFilesErrors(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	cookie := login(t, router, "aashish", adminPassword)
	for path, want := range map[string]struct {
		status int
		code   string
	}{
		"/Nothing":         {404, "not_found"},
		"/Videos/trip.mov": {400, "not_a_folder"},
		"/../../etc":       {400, "bad_path"},
		`\Windows`:         {400, "bad_path"},
	} {
		rec := send(t, router, request{method: "GET", path: filesURL(path), cookie: cookie})
		wantError(t, rec, rec.Body.Bytes(), want.status, want.code)
	}
}

func TestOtherUsersCantReachYourFiles(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	guest := login(t, router, "guest", guestPassword)

	rec := send(t, router, request{method: "GET", path: filesURL("/"), cookie: guest})
	if listing := decodeBody[listingJSON](t, rec.Body.Bytes()); len(listing.Entries) != 0 {
		t.Errorf("guest sees %d entries in their own empty folder", len(listing.Entries))
	}
	rec = send(t, router, request{method: "GET", path: contentURL("/Videos/trip.mov"), cookie: guest})
	wantError(t, rec, rec.Body.Bytes(), http.StatusNotFound, "not_found")
}

func TestDownloadWholeFile(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	cookie := login(t, router, "aashish", adminPassword)

	rec := send(t, router, request{method: "GET", path: contentURL("/Videos/trip.mov"), cookie: cookie})
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), videoBytes) {
		t.Fatalf("status %d, %d bytes", rec.Code, rec.Body.Len())
	}
	for name, want := range map[string]string{
		"Content-Type":            "video/quicktime",
		"Content-Length":          "1000",
		"Accept-Ranges":           "bytes",
		"Content-Disposition":     `attachment; filename=trip.mov`,
		"Content-Security-Policy": fileSandbox,
		"Cache-Control":           "private, no-cache",
		"X-Content-Type-Options":  "nosniff",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if etag := rec.Header().Get("ETag"); !strings.HasPrefix(etag, `"3e8-`) {
		t.Errorf("ETag = %q, want it to start with the size in hex", etag)
	}
}

// What a phone's video player does when you play and then drag the slider.
func TestRangeRequestsForScrubbing(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	cookie := login(t, router, "aashish", adminPassword)
	get := func(headers map[string]string) *http.Response {
		rec := send(t, router, request{method: "GET", path: contentURL("/Videos/trip.mov", "&inline=1"), cookie: cookie, headers: headers})
		return rec.Result()
	}
	body := func(resp *http.Response) []byte {
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		return buf.Bytes()
	}

	// Safari's opening probe: the first two bytes.
	resp := get(map[string]string{"Range": "bytes=0-1"})
	if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") != "bytes 0-1/1000" || !bytes.Equal(body(resp), videoBytes[0:2]) {
		t.Fatalf("probe: %d %q", resp.StatusCode, resp.Header.Get("Content-Range"))
	}
	if got := resp.Header.Get("Content-Disposition"); got != "inline; filename=trip.mov" {
		t.Errorf("Content-Disposition = %q, want inline", got)
	}

	// Jump to the middle.
	resp = get(map[string]string{"Range": "bytes=500-599"})
	if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") != "bytes 500-599/1000" || !bytes.Equal(body(resp), videoBytes[500:600]) {
		t.Fatalf("middle: %d %q", resp.StatusCode, resp.Header.Get("Content-Range"))
	}

	// Read the end, where phone cameras often keep the video's index.
	resp = get(map[string]string{"Range": "bytes=-100"})
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(body(resp), videoBytes[900:]) {
		t.Fatalf("end: %d %q", resp.StatusCode, resp.Header.Get("Content-Range"))
	}

	// Open-ended range from a point.
	resp = get(map[string]string{"Range": "bytes=990-"})
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(body(resp), videoBytes[990:]) {
		t.Fatalf("open-ended: %d", resp.StatusCode)
	}

	// Past the end.
	if resp = get(map[string]string{"Range": "bytes=5000-"}); resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("past the end: %d, want 416", resp.StatusCode)
	}
}

func TestETagLetsTheBrowserReuseAndDetectChanges(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	cookie := login(t, router, "aashish", adminPassword)
	first := send(t, router, request{method: "GET", path: contentURL("/Videos/trip.mov"), cookie: cookie})
	etag := first.Header().Get("ETag")

	// Already have it: 304, no body.
	rec := send(t, router, request{method: "GET", path: contentURL("/Videos/trip.mov"), cookie: cookie, headers: map[string]string{"If-None-Match": etag}})
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("If-None-Match: %d with %d bytes, want 304 and no body", rec.Code, rec.Body.Len())
	}
	// Resume a range of the same version: 206.
	rec = send(t, router, request{method: "GET", path: contentURL("/Videos/trip.mov"), cookie: cookie, headers: map[string]string{"Range": "bytes=100-199", "If-Range": etag}})
	if rec.Code != http.StatusPartialContent {
		t.Errorf("If-Range with the current version: %d, want 206", rec.Code)
	}
	// The file changed since: the whole new file instead of a mismatched piece.
	rec = send(t, router, request{method: "GET", path: contentURL("/Videos/trip.mov"), cookie: cookie, headers: map[string]string{"Range": "bytes=100-199", "If-Range": `"old-version"`}})
	if rec.Code != http.StatusOK || rec.Body.Len() != 1000 {
		t.Errorf("If-Range with an old version: %d with %d bytes, want 200 and the whole file", rec.Code, rec.Body.Len())
	}
}

func TestHeadSendsHeadersOnly(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	cookie := login(t, router, "aashish", adminPassword)
	rec := send(t, router, request{method: "HEAD", path: contentURL("/Videos/trip.mov"), cookie: cookie})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Length") != "1000" || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d, length %q, %d body bytes", rec.Code, rec.Header().Get("Content-Length"), rec.Body.Len())
	}
}

func TestHowFilesOpenInTheBrowser(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	cookie := login(t, router, "aashish", adminPassword)
	for path, want := range map[string]struct{ contentType, disposition, csp string }{
		// Media plays or displays in the browser.
		"/Photos/beach.jpg": {"image/jpeg", "inline; filename=beach.jpg", fileSandbox},
		// HTML is shown as text, never run, and sandboxed regardless.
		"/Docs/page.html": {"text/plain; charset=utf-8", "inline; filename=page.html", fileSandbox},
		// PDF viewers don't work sandboxed, so PDFs have no CSP header.
		"/Docs/scan.pdf": {"application/pdf", "inline; filename=scan.pdf", ""},
		// Anything else downloads even when asked to open.
		"/Docs/archive.zip": {"application/zip", "attachment; filename=archive.zip", fileSandbox},
		// Non-ASCII names are encoded so every browser gets them right.
		"/Docs/résumé 2026.txt": {"text/plain; charset=utf-8", "inline; filename*=utf-8''r%C3%A9sum%C3%A9%202026.txt", fileSandbox},
	} {
		rec := send(t, router, request{method: "GET", path: contentURL(path, "&inline=1"), cookie: cookie})
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, rec.Code)
			continue
		}
		got := [3]string{rec.Header().Get("Content-Type"), rec.Header().Get("Content-Disposition"), rec.Header().Get("Content-Security-Policy")}
		if got != [3]string{want.contentType, want.disposition, want.csp} {
			t.Errorf("%s:\n got %q\nwant %q", path, got, [3]string{want.contentType, want.disposition, want.csp})
		}
	}
}

func TestDownloadErrors(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	cookie := login(t, router, "aashish", adminPassword)
	for path, want := range map[string]struct {
		status int
		code   string
	}{
		"/Videos":           {400, "is_a_folder"},
		"/Videos/gone.mov":  {404, "not_found"},
		"/../users/1/x":     {400, "bad_path"},
		"/notes.txt:hidden": {400, "bad_path"},
		"/Docs/CON":         {400, "bad_path"},
	} {
		rec := send(t, router, request{method: "GET", path: contentURL(path), cookie: cookie})
		wantError(t, rec, rec.Body.Bytes(), want.status, want.code)
	}
}
