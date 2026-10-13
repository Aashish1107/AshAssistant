package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// States of an extracted audio track.
const (
	StateReady     = "ready"
	StatePreparing = "preparing"
	StateFailed    = "failed"
)

const (
	// A failed extraction is reported for this long before Prepare tries again.
	retryAfterFailure = time.Minute
	// Remember this many probed videos before starting over.
	maxCachedProbes = 500
)

// Source is a video on disk.
type Source struct {
	UserID   string
	Path     string // the path the browser uses, e.g. "/Videos/movie.mp4"
	Local    string // where it is on disk, for ffmpeg
	Size     int64
	Modified time.Time
}

// version identifies this exact version of the file: if it's replaced or
// edited, its size or time changes and old extracted tracks no longer match.
func (s Source) version() string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d", s.UserID, s.Path, s.Size, s.Modified.UnixNano())
}

// Status is where an audio track's extraction stands.
type Status struct {
	State    string
	Progress float64 // 0 to 1, while preparing
	Error    string  // when failed
}

type job struct {
	progress atomic.Uint64 // math.Float64bits of 0..1
	done     bool
	err      error
	finished time.Time
}

// Library probes videos and extracts their audio tracks into a cache folder,
// one extraction at a time so a movie doesn't take over the laptop.
type Library struct {
	tools    Tools
	toolsErr error
	cacheDir string
	logger   *slog.Logger
	ctx      context.Context // extractions stop when this is cancelled
	slots    chan struct{}

	mu     sync.Mutex
	probes map[string]Info
	jobs   map[string]*job
}

// NewLibrary keeps extracted tracks in cacheDir. toolsErr is what FindTools
// returned; when it's not nil, every method reports it.
func NewLibrary(ctx context.Context, tools Tools, toolsErr error, cacheDir string, logger *slog.Logger) (*Library, error) {
	if toolsErr == nil {
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return nil, err
		}
		removePartials(cacheDir) // left by a run that was interrupted
	}
	return &Library{
		tools:    tools,
		toolsErr: toolsErr,
		cacheDir: cacheDir,
		logger:   logger,
		ctx:      ctx,
		slots:    make(chan struct{}, 1),
		probes:   map[string]Info{},
		jobs:     map[string]*job{},
	}, nil
}

// Available is nil when ffmpeg was found, or the reason it can't be used.
func (l *Library) Available() error {
	return l.toolsErr
}

// Info lists a video's audio tracks. Results are remembered per file version.
func (l *Library) Info(ctx context.Context, src Source) (Info, error) {
	if l.toolsErr != nil {
		return Info{}, l.toolsErr
	}
	key := src.version()
	l.mu.Lock()
	info, ok := l.probes[key]
	l.mu.Unlock()
	if ok {
		return info, nil
	}
	info, err := l.tools.Probe(ctx, src.Local)
	if err != nil {
		return Info{}, err
	}
	l.mu.Lock()
	if len(l.probes) >= maxCachedProbes {
		l.probes = map[string]Info{}
	}
	l.probes[key] = info
	l.mu.Unlock()
	return info, nil
}

// Prepare starts extracting a track unless that's already done or under way,
// and reports where it stands. Call it again to follow progress.
func (l *Library) Prepare(src Source, info Info, track AudioTrack) Status {
	if l.toolsErr != nil {
		return Status{State: StateFailed, Error: l.toolsErr.Error()}
	}
	file := l.trackFile(src, track)
	if touch(file) {
		return Status{State: StateReady, Progress: 1}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if j, ok := l.jobs[file]; ok {
		switch {
		case !j.done:
			return Status{State: StatePreparing, Progress: math.Float64frombits(j.progress.Load())}
		case j.err != nil && time.Since(j.finished) < retryAfterFailure:
			return Status{State: StateFailed, Error: j.err.Error()}
		}
		// An old failure, or a success whose file was since deleted: start again.
	}
	j := &job{}
	l.jobs[file] = j
	go l.extract(j, src, info, track, file)
	return Status{State: StatePreparing}
}

// Ready returns the extracted track's file and content type, if it's done.
func (l *Library) Ready(src Source, track AudioTrack) (string, string, bool) {
	if l.toolsErr != nil {
		return "", "", false
	}
	file := l.trackFile(src, track)
	if !touch(file) {
		return "", "", false
	}
	return file, OutputFor(track.Codec).ContentType, true
}

// Prune deletes extracted tracks that haven't been used for maxAge.
func (l *Library) Prune(maxAge time.Duration) (int, error) {
	if l.toolsErr != nil {
		return 0, nil
	}
	entries, err := os.ReadDir(l.cacheDir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) < maxAge {
			continue
		}
		if os.Remove(filepath.Join(l.cacheDir, entry.Name())) == nil {
			removed++
		}
	}
	return removed, nil
}

func (l *Library) extract(j *job, src Source, info Info, track AudioTrack, file string) {
	finish := func(err error) {
		l.mu.Lock()
		j.done, j.err, j.finished = true, err, time.Now()
		if err == nil {
			delete(l.jobs, file)
		}
		l.mu.Unlock()
	}
	select {
	case l.slots <- struct{}{}:
		defer func() { <-l.slots }()
	case <-l.ctx.Done():
		finish(l.ctx.Err())
		return
	}

	start := time.Now()
	attrs := []any{"user_id", src.UserID, "path", src.Path, "track", track.Track, "codec", track.Codec, "copy", OutputFor(track.Codec).Copy}
	l.logger.Info("extracting audio track", attrs...)
	err := l.tools.ExtractAudio(l.ctx, src.Local, track, info.Duration, file, func(p float64) {
		j.progress.Store(math.Float64bits(p))
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			l.logger.Error("audio track extraction failed", append(attrs, "error", err)...)
		}
		finish(errors.New("couldn't prepare that audio track"))
		return
	}
	l.logger.Info("audio track ready", append(attrs, "took", time.Since(start).Round(time.Millisecond))...)
	finish(nil)
}

// trackFile is where a track of a file version is cached. The name is a
// hash, so it reveals nothing about the video.
func (l *Library) trackFile(src Source, track AudioTrack) string {
	sum := sha256.Sum256([]byte(src.version() + "\x00" + strconv.Itoa(track.Stream)))
	return filepath.Join(l.cacheDir, hex.EncodeToString(sum[:16])+OutputFor(track.Codec).Ext)
}

// touch reports whether file exists, marking it as recently used so Prune
// keeps it.
func touch(file string) bool {
	now := time.Now()
	return os.Chtimes(file, now, now) == nil
}

func removePartials(dir string) {
	filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".part") {
			os.Remove(path)
		}
		return nil
	})
}
