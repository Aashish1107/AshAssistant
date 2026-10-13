// Package media inspects videos and pulls out their audio tracks with
// ffmpeg. Chrome on Android only ever plays a video's first audio track, so
// to switch to another one (say, Hindi instead of English) the server extracts
// that track to its own file and the player plays it in sync with the video.
package media

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ErrNoFFmpeg means ffmpeg or ffprobe couldn't be found.
var ErrNoFFmpeg = errors.New("ffmpeg isn't installed on the laptop (winget install Gyan.FFmpeg)")

// Tools are the ffmpeg and ffprobe programs.
type Tools struct {
	FFmpeg  string
	FFprobe string
}

// FindTools looks for ffmpeg and ffprobe in dir, or on the PATH when dir is "".
func FindTools(dir string) (Tools, error) {
	find := func(name string) (string, error) {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if dir == "" {
			return exec.LookPath(name)
		}
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			return "", err
		}
		return path, nil
	}
	ffmpeg, err := find("ffmpeg")
	if err != nil {
		return Tools{}, ErrNoFFmpeg
	}
	ffprobe, err := find("ffprobe")
	if err != nil {
		return Tools{}, ErrNoFFmpeg
	}
	return Tools{FFmpeg: ffmpeg, FFprobe: ffprobe}, nil
}

// AudioTrack is one audio track in a video.
type AudioTrack struct {
	Track    int    // position among the file's audio tracks: 0, 1, 2…
	Stream   int    // the stream's index in the file, as ffmpeg numbers it
	Codec    string // e.g. "aac", "ac3", "opus"
	Channels int
	Layout   string // e.g. "stereo", "5.1(side)"
	Language string // ISO 639-2, e.g. "hin"; "" when not set
	Title    string
	Default  bool
}

// Info is what ffprobe reports about a video.
type Info struct {
	Duration    time.Duration
	AudioTracks []AudioTrack
}

// ffmpegInput names a local file so ffmpeg can't mistake it for another
// protocol (a name like "http:x" or "concat:a|b" would otherwise be one).
func ffmpegInput(path string) string {
	return "file:" + path
}

// Probe lists a video's audio tracks.
func (t Tools) Probe(ctx context.Context, path string) (Info, error) {
	cmd := exec.CommandContext(ctx, t.FFprobe,
		"-v", "error",
		"-protocol_whitelist", "file",
		"-print_format", "json",
		"-show_entries", "format=duration:stream=index,codec_name,channels,channel_layout:stream_tags=language,title,handler_name:stream_disposition=default",
		"-select_streams", "a",
		ffmpegInput(path),
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return Info{}, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseProbe(out)
}

func parseProbe(out []byte) (Info, error) {
	var probe struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Index         int               `json:"index"`
			CodecName     string            `json:"codec_name"`
			Channels      int               `json:"channels"`
			ChannelLayout string            `json:"channel_layout"`
			Tags          map[string]string `json:"tags"`
			Disposition   map[string]int    `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return Info{}, fmt.Errorf("ffprobe output: %w", err)
	}
	var info Info
	if seconds, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil {
		info.Duration = time.Duration(seconds * float64(time.Second))
	}
	for i, s := range probe.Streams {
		info.AudioTracks = append(info.AudioTracks, AudioTrack{
			Track:    i,
			Stream:   s.Index,
			Codec:    s.CodecName,
			Channels: s.Channels,
			Layout:   s.ChannelLayout,
			Language: tag(s.Tags, "language"),
			Title:    trackTitle(s.Tags),
			Default:  s.Disposition["default"] == 1,
		})
	}
	return info, nil
}

// trackTitle is a track's name. MKV files keep it in "title"; MP4 files in
// "handler_name", which most programs fill with a generic default such as
// "SoundHandler" or "Core Media Audio" that isn't worth showing.
func trackTitle(tags map[string]string) string {
	if title := tag(tags, "title"); title != "" {
		return title
	}
	name := tag(tags, "handler_name")
	lower := strings.ToLower(name)
	for _, generic := range []string{"handler", "core media", "iso media", "media file"} {
		if strings.Contains(lower, generic) {
			return ""
		}
	}
	return name
}

// tag reads a stream tag, ignoring case ("language" vs "LANGUAGE" in MKV)
// and "und" (undetermined).
func tag(tags map[string]string, name string) string {
	for key, value := range tags {
		if strings.EqualFold(key, name) && value != "und" {
			return value
		}
	}
	return ""
}

// Output is how an extracted track is stored.
type Output struct {
	Ext         string // file extension
	Muxer       string // ffmpeg's name for the container
	ContentType string
	Copy        bool // true: the audio is copied as-is; false: converted to AAC
}

// OutputFor picks how to store a track. Codecs Chrome on Android plays are
// copied unchanged (fast, no quality loss); anything else, such as the AC3
// or DTS common in movies, is converted to stereo AAC.
func OutputFor(codec string) Output {
	switch codec {
	case "aac":
		return Output{".m4a", "mp4", "audio/mp4", true}
	case "mp3":
		return Output{".mp3", "mp3", "audio/mpeg", true}
	case "opus", "vorbis":
		return Output{".webm", "webm", "audio/webm", true}
	case "flac":
		return Output{".flac", "flac", "audio/flac", true}
	default:
		return Output{".m4a", "mp4", "audio/mp4", false}
	}
}

// ExtractAudio writes one audio track of src to dst. progress, if not nil,
// is called with values from 0 to 1 as the work goes on. dst only appears
// once the whole track has been written.
func (t Tools) ExtractAudio(ctx context.Context, src string, track AudioTrack, duration time.Duration, dst string, progress func(float64)) error {
	output := OutputFor(track.Codec)
	partial := dst + ".part"
	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-nostats",
		"-protocol_whitelist", "file",
		"-i", ffmpegInput(src),
		"-map", fmt.Sprintf("0:%d", track.Stream),
		"-vn", "-sn", "-dn",
	}
	if output.Copy {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
	}
	if output.Muxer == "mp4" {
		args = append(args, "-movflags", "+faststart") // index first, so playback can start at once
	}
	args = append(args, "-progress", "pipe:1", "-f", output.Muxer, "-y", ffmpegInput(partial))

	cmd := exec.CommandContext(ctx, t.FFmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if progress != nil && duration > 0 {
			if done, ok := parseProgress(scanner.Text()); ok {
				progress(min(1, float64(done)/float64(duration)))
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		os.Remove(partial)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return os.Rename(partial, dst)
}

// parseProgress reads ffmpeg's "-progress" output, whose out_time_us (and,
// despite its name, out_time_ms) lines give microseconds processed so far.
func parseProgress(line string) (time.Duration, bool) {
	key, value, ok := strings.Cut(line, "=")
	if !ok || (key != "out_time_us" && key != "out_time_ms") {
		return 0, false
	}
	us, err := strconv.ParseInt(value, 10, 64)
	if err != nil || us < 0 {
		return 0, false
	}
	return time.Duration(us) * time.Microsecond, true
}
