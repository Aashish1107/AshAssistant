package httpapi

import (
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/media"
)

// routerWithVideo returns a router whose admin has /Videos/dual.mp4: H.264
// with an English AAC track and a Hindi AC3 5.1 track.
func routerWithVideo(t *testing.T) (http.Handler, *http.Cookie) {
	t.Helper()
	tools, err := media.FindTools("")
	if err != nil {
		t.Skip("ffmpeg isn't installed")
	}
	router, _, home := buildTestRouter(t, auth.Options{}, tools, nil)
	cmd := exec.Command(tools.FFmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=320x180:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=4",
		"-map", "0:v", "-map", "1:a", "-map", "2:a",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a:0", "aac", "-c:a:1", "ac3", "-ac:a:1", "6",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=hin", "-metadata:s:a:1", "handler_name=Hindi 5.1",
		filepath.Join(home, "Videos", "dual.mp4"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("making test video: %v\n%s", err, out)
	}
	return router, login(t, router, "aashish", adminPassword)
}

func mediaInfoURL(path string) string {
	return "/api/v1/media/info?path=" + url.QueryEscape(path)
}

func TestMediaInfoListsAudioTracks(t *testing.T) {
	router, cookie := routerWithVideo(t)
	rec := send(t, router, request{method: "GET", path: mediaInfoURL("/Videos/dual.mp4"), cookie: cookie})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d\n%s", rec.Code, rec.Body)
	}
	info := decodeBody[mediaInfoJSON](t, rec.Body.Bytes())
	if !info.CanSwitchAudio || info.Reason != "" || info.Path != "/Videos/dual.mp4" {
		t.Errorf("info = %+v", info)
	}
	if info.DurationSeconds < 3.9 || info.DurationSeconds > 4.2 {
		t.Errorf("duration = %v, want about 4", info.DurationSeconds)
	}
	want := []audioTrackJSON{
		{Track: 0, Codec: "aac", Channels: 1, Layout: "mono", Language: "eng", Default: true, Native: true},
		{Track: 1, Codec: "ac3", Channels: 6, Layout: "5.1(side)", Language: "hin", Title: "Hindi 5.1"},
	}
	if len(info.AudioTracks) != 2 {
		t.Fatalf("tracks = %+v", info.AudioTracks)
	}
	for i := range want {
		got := info.AudioTracks[i]
		got.Layout, want[i].Layout = "", "" // layout names vary between ffmpeg versions
		if got != want[i] {
			t.Errorf("track %d = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestMediaInfoErrors(t *testing.T) {
	router, cookie := routerWithVideo(t)
	for path, want := range map[string]struct {
		status int
		code   string
	}{
		"/Videos/missing.mp4": {404, "not_found"},
		"/Docs/scan.pdf":      {422, "not_a_video"},
		"/Videos":             {400, "is_a_folder"},
		"/../x.mp4":           {400, "bad_path"},
	} {
		rec := send(t, router, request{method: "GET", path: mediaInfoURL(path), cookie: cookie})
		wantError(t, rec, rec.Body.Bytes(), want.status, want.code)
	}
	rec := send(t, router, request{method: "GET", path: mediaInfoURL("/Videos/dual.mp4")})
	wantError(t, rec, rec.Body.Bytes(), http.StatusUnauthorized, "not_logged_in")
}

func TestMediaInfoWithoutFFmpeg(t *testing.T) {
	router, _, _ := buildTestRouter(t, auth.Options{}, media.Tools{}, media.ErrNoFFmpeg)
	cookie := login(t, router, "aashish", adminPassword)
	rec := send(t, router, request{method: "GET", path: mediaInfoURL("/Videos/trip.mov"), cookie: cookie})
	info := decodeBody[mediaInfoJSON](t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || info.CanSwitchAudio || !strings.Contains(info.Reason, "winget install Gyan.FFmpeg") {
		t.Errorf("%d %+v", rec.Code, info)
	}
	rec = send(t, router, request{method: "POST", path: "/api/v1/media/audio", cookie: cookie, body: `{"path": "/Videos/trip.mov", "track": 1}`})
	wantError(t, rec, rec.Body.Bytes(), http.StatusConflict, "no_ffmpeg")
}

func prepare(t *testing.T, router http.Handler, cookie *http.Cookie, path string, track int) audioStatusJSON {
	t.Helper()
	rec := send(t, router, request{method: "POST", path: "/api/v1/media/audio", cookie: cookie,
		body: `{"path": "` + path + `", "track": ` + strconv.Itoa(track) + `}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("prepare: %d\n%s", rec.Code, rec.Body)
	}
	return decodeBody[audioStatusJSON](t, rec.Body.Bytes())
}

func TestSwitchingToAnotherAudioTrack(t *testing.T) {
	router, cookie := routerWithVideo(t)
	trackURL := "/api/v1/media/audio?path=" + url.QueryEscape("/Videos/dual.mp4") + "&track=1"

	// Not prepared yet.
	rec := send(t, router, request{method: "GET", path: trackURL, cookie: cookie})
	wantError(t, rec, rec.Body.Bytes(), http.StatusNotFound, "not_ready")

	status := prepare(t, router, cookie, "/Videos/dual.mp4", 1)
	if status.State != "preparing" {
		t.Fatalf("first status = %+v", status)
	}
	deadline := time.Now().Add(30 * time.Second)
	for status.State == "preparing" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		status = prepare(t, router, cookie, "/Videos/dual.mp4", 1)
	}
	if status.State != "ready" || status.URL != trackURL || status.Progress != 1 {
		t.Fatalf("final status = %+v", status)
	}

	// Play it like the player does: probe the start, then jump.
	rec = send(t, router, request{method: "GET", path: trackURL, cookie: cookie, headers: map[string]string{"Range": "bytes=0-1"}})
	if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Type") != "audio/mp4" || rec.Header().Get("Content-Security-Policy") != fileSandbox {
		t.Fatalf("range request: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Content-Security-Policy"))
	}
	etag := rec.Header().Get("ETag")
	rec = send(t, router, request{method: "GET", path: trackURL, cookie: cookie, headers: map[string]string{"Range": "bytes=100-199", "If-Range": etag}})
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 100 {
		t.Errorf("resumed range: %d with %d bytes", rec.Code, rec.Body.Len())
	}
	// The ETag stays the same on later requests, even though using the
	// cached copy marks it as recently used.
	time.Sleep(10 * time.Millisecond)
	if again := send(t, router, request{method: "GET", path: trackURL, cookie: cookie}); again.Header().Get("ETag") != etag {
		t.Errorf("ETag changed between requests: %q then %q", etag, again.Header().Get("ETag"))
	}

	// Asking again is instant.
	if again := prepare(t, router, cookie, "/Videos/dual.mp4", 1); again.State != "ready" {
		t.Errorf("second prepare = %+v", again)
	}
}

func TestPrepareAudioErrors(t *testing.T) {
	router, cookie := routerWithVideo(t)
	for name, tc := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"no such track":  {`{"path": "/Videos/dual.mp4", "track": 2}`, 400, "no_such_track"},
		"negative track": {`{"path": "/Videos/dual.mp4", "track": -1}`, 400, "no_such_track"},
		"not a video":    {`{"path": "/Docs/scan.pdf", "track": 0}`, 422, "not_a_video"},
		"missing":        {`{"path": "/nope.mp4", "track": 0}`, 404, "not_found"},
	} {
		rec := send(t, router, request{method: "POST", path: "/api/v1/media/audio", cookie: cookie, body: tc.body})
		if rec.Code != tc.status {
			t.Errorf("%s: %d, want %d\n%s", name, rec.Code, tc.status, rec.Body)
			continue
		}
		wantError(t, rec, rec.Body.Bytes(), tc.status, tc.code)
	}
	rec := send(t, router, request{method: "POST", path: "/api/v1/media/audio", cookie: cookie,
		body: `{"path": "/Videos/dual.mp4", "track": 1}`, headers: map[string]string{"X-Requested-With": ""}})
	wantError(t, rec, rec.Body.Bytes(), http.StatusForbidden, "missing_header")
}

func TestOtherUsersCantUseYourAudioTracks(t *testing.T) {
	router, cookie := routerWithVideo(t)
	for prepare(t, router, cookie, "/Videos/dual.mp4", 1).State == "preparing" {
		time.Sleep(50 * time.Millisecond)
	}
	guest := login(t, router, "guest", guestPassword)
	rec := send(t, router, request{method: "GET", path: "/api/v1/media/audio?path=" + url.QueryEscape("/Videos/dual.mp4") + "&track=1", cookie: guest})
	wantError(t, rec, rec.Body.Bytes(), http.StatusNotFound, "not_found")
}
