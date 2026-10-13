package media

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseProbe(t *testing.T) {
	out := []byte(`{
		"streams": [
			{"index": 1, "codec_name": "aac", "channels": 2, "channel_layout": "stereo",
			 "tags": {"language": "eng", "title": "English"}, "disposition": {"default": 1}},
			{"index": 2, "codec_name": "ac3", "channels": 6, "channel_layout": "5.1(side)",
			 "tags": {"LANGUAGE": "hin"}, "disposition": {"default": 0}},
			{"index": 4, "codec_name": "dts", "channels": 6, "tags": {"language": "und"}}
		],
		"format": {"duration": "7265.123000"}
	}`)
	info, err := parseProbe(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration != 7265123*time.Millisecond {
		t.Errorf("duration = %v", info.Duration)
	}
	want := []AudioTrack{
		{Track: 0, Stream: 1, Codec: "aac", Channels: 2, Layout: "stereo", Language: "eng", Title: "English", Default: true},
		{Track: 1, Stream: 2, Codec: "ac3", Channels: 6, Layout: "5.1(side)", Language: "hin"},
		{Track: 2, Stream: 4, Codec: "dts", Channels: 6},
	}
	if len(info.AudioTracks) != len(want) {
		t.Fatalf("tracks = %+v", info.AudioTracks)
	}
	for i := range want {
		if info.AudioTracks[i] != want[i] {
			t.Errorf("track %d = %+v, want %+v", i, info.AudioTracks[i], want[i])
		}
	}
}

func TestTrackTitle(t *testing.T) {
	for want, tags := range map[string]map[string]string{
		"English":    {"title": "English", "handler_name": "Hindi"}, // title wins
		"Hindi 5.1":  {"handler_name": "Hindi 5.1"},                 // MP4-style name
		"Commentary": {"HANDLER_NAME": "Commentary"},
		"":           {"handler_name": "SoundHandler"},
	} {
		if got := trackTitle(tags); got != want {
			t.Errorf("trackTitle(%v) = %q, want %q", tags, got, want)
		}
	}
	for _, generic := range []string{"SoundHandler", "Core Media Audio", "GPAC ISO Audio Handler", "Apple Sound Media Handler", "ISO Media file produced by Google Inc."} {
		if got := trackTitle(map[string]string{"handler_name": generic}); got != "" {
			t.Errorf("generic handler name %q shown as %q", generic, got)
		}
	}
}

func TestParseProgress(t *testing.T) {
	for line, want := range map[string]time.Duration{
		"out_time_us=1500000": 1500 * time.Millisecond,
		"out_time_ms=2000000": 2 * time.Second, // microseconds despite the name
	} {
		if got, ok := parseProgress(line); !ok || got != want {
			t.Errorf("parseProgress(%q) = %v, %v; want %v", line, got, ok, want)
		}
	}
	for _, line := range []string{"progress=continue", "out_time=00:00:01.5", "out_time_us=N/A", "out_time_us=-5"} {
		if _, ok := parseProgress(line); ok {
			t.Errorf("parseProgress(%q) accepted", line)
		}
	}
}

func TestOutputFor(t *testing.T) {
	for codec, want := range map[string]Output{
		"aac":    {".m4a", "mp4", "audio/mp4", true},
		"mp3":    {".mp3", "mp3", "audio/mpeg", true},
		"opus":   {".webm", "webm", "audio/webm", true},
		"vorbis": {".webm", "webm", "audio/webm", true},
		"flac":   {".flac", "flac", "audio/flac", true},
		"ac3":    {".m4a", "mp4", "audio/mp4", false},
		"eac3":   {".m4a", "mp4", "audio/mp4", false},
		"dts":    {".m4a", "mp4", "audio/mp4", false},
		"truehd": {".m4a", "mp4", "audio/mp4", false},
	} {
		if got := OutputFor(codec); got != want {
			t.Errorf("OutputFor(%q) = %+v, want %+v", codec, got, want)
		}
	}
}

func TestInputsAreAlwaysFiles(t *testing.T) {
	for _, path := range []string{`C:\AshCloud\movie.mp4`, "/data/concat:a|b", "http://example.com/x"} {
		if got := ffmpegInput(path); got != "file:"+path {
			t.Errorf("ffmpegInput(%q) = %q", path, got)
		}
	}
}

func TestFindToolsInMissingFolder(t *testing.T) {
	if _, err := FindTools(t.TempDir()); !errors.Is(err, ErrNoFFmpeg) {
		t.Fatalf("got %v, want ErrNoFFmpeg", err)
	}
}

// ---- with the real ffmpeg --------------------------------------------------

func requireTools(t *testing.T) Tools {
	t.Helper()
	tools, err := FindTools("")
	if err != nil {
		t.Skip("ffmpeg isn't installed")
	}
	return tools
}

var (
	samplesOnce sync.Once
	samplesDir  string
	samplesErr  error
)

// samples makes (once) two 5-second videos with two audio tracks each:
//
//	phone.mp4: H.264, English AAC stereo, Hindi AC3 5.1
//	web.webm:  VP8, English Opus, Hindi Opus
func samples(t *testing.T) string {
	t.Helper()
	requireTools(t)
	samplesOnce.Do(func() {
		samplesDir, samplesErr = os.MkdirTemp("", "ashcloud-media-*")
		if samplesErr != nil {
			return
		}
		run := func(args ...string) {
			if samplesErr != nil {
				return
			}
			cmd := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...)
			cmd.Dir = samplesDir
			if out, err := cmd.CombinedOutput(); err != nil {
				samplesErr = errors.New(string(out))
			}
		}
		sources := []string{
			"-f", "lavfi", "-i", "testsrc=duration=5:size=320x180:rate=25",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=5",
			"-f", "lavfi", "-i", "sine=frequency=880:duration=5",
			"-map", "0:v", "-map", "1:a", "-map", "2:a",
			"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=hin",
		}
		run(append(sources, "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
			"-c:a:0", "aac", "-ac:a:0", "2", "-c:a:1", "ac3", "-ac:a:1", "6", "phone.mp4")...)
		run(append(sources, "-c:v", "libvpx", "-deadline", "realtime", "-c:a", "libopus", "web.webm")...)
	})
	if samplesErr != nil {
		t.Fatalf("making sample videos: %v", samplesErr)
	}
	return samplesDir
}

func TestMain(m *testing.M) {
	code := m.Run()
	if samplesDir != "" {
		os.RemoveAll(samplesDir)
	}
	os.Exit(code)
}

func TestProbeRealVideo(t *testing.T) {
	tools := requireTools(t)
	info, err := tools.Probe(context.Background(), filepath.Join(samples(t), "phone.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration < 4900*time.Millisecond || info.Duration > 5200*time.Millisecond {
		t.Errorf("duration = %v, want about 5s", info.Duration)
	}
	if len(info.AudioTracks) != 2 {
		t.Fatalf("tracks = %+v", info.AudioTracks)
	}
	english, hindi := info.AudioTracks[0], info.AudioTracks[1]
	if english.Track != 0 || english.Stream != 1 || english.Codec != "aac" || english.Channels != 2 || english.Language != "eng" {
		t.Errorf("English track = %+v", english)
	}
	if hindi.Track != 1 || hindi.Stream != 2 || hindi.Codec != "ac3" || hindi.Channels != 6 || hindi.Language != "hin" {
		t.Errorf("Hindi track = %+v", hindi)
	}
}

func TestProbeRejectsNonVideo(t *testing.T) {
	tools := requireTools(t)
	path := filepath.Join(t.TempDir(), "notes.mp4")
	os.WriteFile(path, []byte("not a video"), 0o600)
	if _, err := tools.Probe(context.Background(), path); err == nil {
		t.Fatal("probing a text file succeeded")
	}
}

func TestExtractAudio(t *testing.T) {
	tools := requireTools(t)
	dir := samples(t)
	ctx := context.Background()
	for _, tc := range []struct {
		video     string
		track     int
		wantCodec string
		wantChans int
		wantExt   string
	}{
		{"phone.mp4", 0, "aac", 2, ".m4a"},  // copied as-is
		{"phone.mp4", 1, "aac", 2, ".m4a"},  // AC3 5.1 converted to stereo AAC
		{"web.webm", 1, "opus", 1, ".webm"}, // copied as-is
	} {
		src := filepath.Join(dir, tc.video)
		info, err := tools.Probe(ctx, src)
		if err != nil {
			t.Fatal(err)
		}
		track := info.AudioTracks[tc.track]
		dst := filepath.Join(t.TempDir(), "track"+OutputFor(track.Codec).Ext)
		var last float64
		if err := tools.ExtractAudio(ctx, src, track, info.Duration, dst, func(p float64) { last = p }); err != nil {
			t.Fatalf("%s track %d: %v", tc.video, tc.track, err)
		}
		if filepath.Ext(dst) != tc.wantExt {
			t.Errorf("%s track %d: extension %s, want %s", tc.video, tc.track, filepath.Ext(dst), tc.wantExt)
		}
		if _, err := os.Stat(dst + ".part"); !os.IsNotExist(err) {
			t.Errorf("partial file left behind")
		}
		out, err := tools.Probe(ctx, dst)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.AudioTracks) != 1 || out.AudioTracks[0].Codec != tc.wantCodec || out.AudioTracks[0].Channels != tc.wantChans {
			t.Errorf("%s track %d: extracted %+v, want one %s track with %d channel(s)", tc.video, tc.track, out.AudioTracks, tc.wantCodec, tc.wantChans)
		}
		if out.AudioTracks[0].Language != track.Language {
			t.Errorf("%s track %d: language %q, want %q", tc.video, tc.track, out.AudioTracks[0].Language, track.Language)
		}
		if diff := out.Duration - info.Duration; diff < -300*time.Millisecond || diff > 300*time.Millisecond {
			t.Errorf("%s track %d: %v long, want about %v", tc.video, tc.track, out.Duration, info.Duration)
		}
		if last < 0.9 {
			t.Errorf("%s track %d: last progress %.2f, want close to 1", tc.video, tc.track, last)
		}
	}
}

func TestExtractAudioStopsWhenCancelled(t *testing.T) {
	tools := requireTools(t)
	src := filepath.Join(samples(t), "phone.mp4")
	info, _ := tools.Probe(context.Background(), src)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dst := filepath.Join(t.TempDir(), "x.m4a")
	if err := tools.ExtractAudio(ctx, src, info.AudioTracks[1], info.Duration, dst, nil); err == nil {
		t.Fatal("cancelled extraction succeeded")
	}
	for _, path := range []string{dst, dst + ".part"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s left behind", path)
		}
	}
}

// ---- Library -------------------------------------------------------------

func newTestLibrary(t *testing.T) (*Library, Source) {
	t.Helper()
	tools := requireTools(t)
	src := filepath.Join(samples(t), "phone.mp4")
	stat, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	library, err := NewLibrary(context.Background(), tools, nil, filepath.Join(t.TempDir(), "cache", "audio"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return library, Source{UserID: "1", Path: "/Videos/phone.mp4", Local: src, Size: stat.Size(), Modified: stat.ModTime()}
}

func waitReady(t *testing.T, library *Library, src Source, info Info, track AudioTrack) Status {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		status := library.Prepare(src, info, track)
		if status.State != StatePreparing || time.Now().After(deadline) {
			return status
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestLibraryPreparesOnceThenServesFromCache(t *testing.T) {
	library, src := newTestLibrary(t)
	info, err := library.Info(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	hindi := info.AudioTracks[1]

	if _, _, ok := library.Ready(src, hindi); ok {
		t.Fatal("track ready before being prepared")
	}
	if status := library.Prepare(src, info, hindi); status.State != StatePreparing {
		t.Fatalf("first Prepare = %+v, want preparing", status)
	}
	if status := waitReady(t, library, src, info, hindi); status.State != StateReady || status.Progress != 1 {
		t.Fatalf("final status = %+v, want ready", status)
	}
	file, contentType, ok := library.Ready(src, hindi)
	if !ok || contentType != "audio/mp4" || filepath.Ext(file) != ".m4a" {
		t.Fatalf("Ready = %q %q %v", file, contentType, ok)
	}
	if strings.Contains(file, "phone") || strings.Contains(file, "Videos") {
		t.Errorf("cache file name %q reveals the video's name", filepath.Base(file))
	}

	// A new version of the video (different modified time) needs a new extraction.
	changed := src
	changed.Modified = src.Modified.Add(time.Second)
	if _, _, ok := library.Ready(changed, hindi); ok {
		t.Error("the old version's track was used for a changed video")
	}
	// Another user's identical-looking file never shares a cache entry.
	other := src
	other.UserID = "V1StGXR8_Z5jdHi6B-myT"
	if _, _, ok := library.Ready(other, hindi); ok {
		t.Error("another user's request reused this user's cached track")
	}
}

func TestLibraryReportsFailures(t *testing.T) {
	library, src := newTestLibrary(t)
	info, _ := library.Info(context.Background(), src)
	broken := info.AudioTracks[1]
	broken.Stream = 99 // no such stream

	status := waitReady(t, library, src, info, broken)
	if status.State != StateFailed || status.Error == "" {
		t.Fatalf("status = %+v, want failed with a message", status)
	}
	if strings.Contains(status.Error, src.Local) {
		t.Errorf("error %q reveals the file's location", status.Error)
	}
	// Asking again soon reports the failure instead of retrying immediately.
	if again := library.Prepare(src, info, broken); again.State != StateFailed {
		t.Errorf("second Prepare = %+v, want failed", again)
	}
}

func TestLibraryWithoutFFmpeg(t *testing.T) {
	library, err := NewLibrary(context.Background(), Tools{}, ErrNoFFmpeg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(library.Available(), ErrNoFFmpeg) {
		t.Errorf("Available = %v", library.Available())
	}
	if _, err := library.Info(context.Background(), Source{}); !errors.Is(err, ErrNoFFmpeg) {
		t.Errorf("Info = %v", err)
	}
	if status := library.Prepare(Source{}, Info{}, AudioTrack{}); status.State != StateFailed || !strings.Contains(status.Error, "winget install Gyan.FFmpeg") {
		t.Errorf("Prepare = %+v", status)
	}
}

func TestPrune(t *testing.T) {
	library, src := newTestLibrary(t)
	info, _ := library.Info(context.Background(), src)
	if status := waitReady(t, library, src, info, info.AudioTracks[0]); status.State != StateReady {
		t.Fatal(status)
	}
	file, _, _ := library.Ready(src, info.AudioTracks[0])

	if removed, _ := library.Prune(time.Hour); removed != 0 {
		t.Errorf("pruned %d recently used tracks", removed)
	}
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(file, old, old)
	if removed, _ := library.Prune(time.Hour); removed != 1 {
		t.Errorf("pruned %d, want the 1 unused track", removed)
	}
	if _, _, ok := library.Ready(src, info.AudioTracks[0]); ok {
		t.Error("pruned track still reported ready")
	}
	// And it can simply be prepared again.
	if status := waitReady(t, library, src, info, info.AudioTracks[0]); status.State != StateReady {
		t.Errorf("re-prepare after prune = %+v", status)
	}
}

func TestInterruptedExtractionsAreCleanedUp(t *testing.T) {
	cache := t.TempDir()
	leftover := filepath.Join(cache, "abc.m4a.part")
	os.WriteFile(leftover, []byte("half"), 0o600)
	if _, err := NewLibrary(context.Background(), Tools{FFmpeg: "x", FFprobe: "y"}, nil, cache, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Error("leftover .part file not removed at startup")
	}
}
