package drive

// Uploads are fast because there's nothing between the network and the disk:
// a file is sent in one request, and its bytes stream straight into
// <user folder>/.ashcloud-uploads/<id>.part, 1 MiB at a time, with flushes to
// disk running in the background. When the last byte arrives the file is
// flushed and renamed into place, which is instant.
//
// They're resumable because the part file keeps whatever arrived. The browser
// picks each upload's ID from the destination and the file's name, size and
// date, so after a dropped connection, a closed tab or a restarted server,
// sending the same file again asks where it stopped (Open) and sends only the
// rest (Write).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/db"
	"github.com/Aashish1107/AshAssistant/server/internal/files"
)

var (
	ErrUploadNotFound = errors.New("there's no unfinished upload with that ID - start it from the beginning")
	ErrUploadMismatch = errors.New("that upload ID belongs to a different file")
	ErrBadSize        = errors.New("the size or offset isn't valid")
	ErrTakenOver      = errors.New("a newer request is continuing this upload")
	ErrInterrupted    = errors.New("the upload was interrupted")
)

// OffsetError means a request continues an upload from the wrong byte.
type OffsetError struct {
	Offset int64 // where it has to continue from
}

func (e *OffsetError) Error() string {
	return fmt.Sprintf("the upload continues from byte %d", e.Offset)
}

const (
	// bufferSize is how much is received before each write to disk. Large
	// writes keep the per-write cost (and the antivirus's) low.
	bufferSize = 1 << 20
	// rememberFinished is how long a finished upload is remembered, so that
	// a browser that never got the "done" reply (the connection dropped just
	// as the last byte arrived) learns it's done instead of sending it again.
	rememberFinished = 15 * time.Minute
)

var buffers = sync.Pool{New: func() any { buf := make([]byte, bufferSize); return &buf }}

// Upload is a file being uploaded.
type Upload struct {
	ID       string
	Path     string    // where it's going, e.g. /Photos/IMG_0001.jpg
	Size     int64     // total bytes
	Modified time.Time // the original file's last-modified time; zero if unknown
	Offset   int64     // bytes received so far
}

// Result is where an upload stands after a request.
type Result struct {
	Upload Upload
	File   *files.Entry // the finished file, once every byte has arrived
}

type uploadKey struct{ userID, id string }

// writer is a request receiving an upload's bytes.
type writer struct {
	stop      func()        // makes the request's body reads fail at once
	takenOver atomic.Bool   // a newer request stopped this one
	done      chan struct{} // closed when the request is finished with the upload
}

type finished struct {
	entry files.Entry
	at    time.Time
}

// Open starts an upload, or finds where an unfinished one stopped. up.Offset
// is ignored.
func (s *Service) Open(ctx context.Context, userID string, up Upload) (Result, error) {
	if err := checkUpload(up); err != nil {
		return Result{}, err
	}
	key := uploadKey{userID, up.ID}
	current, row, err := s.lookup(ctx, userID, up)
	if errors.Is(err, ErrUploadNotFound) {
		if entry, ok := s.recall(key); ok {
			return doneResult(current, entry), nil
		}
		current, _, err = s.create(ctx, userID, up)
	}
	if err != nil {
		return Result{}, err
	}
	if row != nil {
		size, err := s.files.UploadSize(userID, up.ID)
		switch {
		case errors.Is(err, files.ErrNotFound):
			size = 0 // nothing has arrived yet
		case err != nil:
			return Result{}, err
		}
		current.Offset = size
	}
	return Result{Upload: current}, nil
}

// Write receives an upload's bytes from body, starting at byte up.Offset,
// and finishes the upload when the last one arrives. A body that ends early
// is fine: what arrived is kept, and another request can continue.
//
// stop must make body's reads fail immediately. It's called when a newer
// request takes over (the phone switched networks, and the old connection
// hasn't noticed it's dead yet) or the server is stopping.
func (s *Service) Write(ctx context.Context, userID string, client auth.Client, up Upload, body io.Reader, stop func()) (Result, error) {
	if err := checkUpload(up); err != nil {
		return Result{}, err
	}
	if up.Offset < 0 || up.Offset > up.Size {
		return Result{}, ErrBadSize
	}
	key := uploadKey{userID, up.ID}
	me := &writer{stop: stop, done: make(chan struct{})}
	if err := s.claim(ctx, key, me); err != nil {
		return Result{}, err
	}
	defer s.release(key, me)

	offset := up.Offset
	current, row, err := s.lookup(ctx, userID, up)
	if errors.Is(err, ErrUploadNotFound) {
		if entry, ok := s.recall(key); ok {
			return doneResult(current, entry), nil
		}
		if offset != 0 {
			return Result{}, ErrUploadNotFound
		}
		current, row, err = s.create(ctx, userID, up)
	}
	if err != nil {
		return Result{}, err
	}

	file, size, err := s.files.OpenUpload(userID, up.ID, false)
	if errors.Is(err, files.ErrNotFound) {
		// Nothing has arrived yet, or it was lost: start from the beginning.
		if offset != 0 {
			current.Offset = 0
			return Result{Upload: current}, &OffsetError{Offset: 0}
		}
		file, size, err = s.files.OpenUpload(userID, up.ID, true)
	}
	if err != nil {
		return Result{}, err
	}
	current.Offset = size
	if offset > size {
		file.Close()
		return Result{Upload: current}, &OffsetError{Offset: size}
	}
	if offset < size {
		// The browser is resending bytes the server already has (it can't
		// know exactly how far the last request got). Cut them off; what
		// follows is the same file, so this is always safe.
		if err := file.Truncate(offset); err != nil {
			file.Close()
			return Result{Upload: current}, files.TranslateError(err)
		}
		current.Offset = offset
		if row.Synced > offset {
			s.saveProgress(ctx, userID, up.ID, offset)
		}
	}
	if err := s.checkSpace(current.Size - current.Offset); err != nil {
		file.Close()
		return Result{Upload: current}, err
	}

	readErr, writeErr := s.receive(ctx, userID, file, body, &current)
	if writeErr != nil || current.Offset < current.Size {
		s.pause(ctx, userID, file, current)
		file.Close()
		switch {
		case writeErr != nil:
			return Result{Upload: current}, files.TranslateError(writeErr)
		case me.takenOver.Load():
			return Result{Upload: current}, ErrTakenOver
		case readErr != nil:
			return Result{Upload: current}, fmt.Errorf("%w: %w", ErrInterrupted, readErr)
		}
		return Result{Upload: current}, nil // this part arrived; the rest comes later
	}

	// Every byte is here. Make sure it's on disk before calling it done.
	if err := file.Sync(); err != nil {
		file.Close()
		return Result{Upload: current}, files.TranslateError(err)
	}
	if err := file.Close(); err != nil {
		return Result{Upload: current}, files.TranslateError(err)
	}
	entry, err := s.finish(ctx, userID, client, current)
	if err != nil {
		return Result{Upload: current}, err
	}
	return doneResult(current, entry), nil
}

// Cancel stops an upload and deletes what it received.
func (s *Service) Cancel(ctx context.Context, userID, uploadID string) error {
	if !files.ValidUploadID(uploadID) {
		return files.ErrBadPath
	}
	key := uploadKey{userID, uploadID}
	me := &writer{stop: func() {}, done: make(chan struct{})}
	if err := s.claim(ctx, key, me); err != nil {
		return err
	}
	defer s.release(key, me)
	if err := s.files.RemoveUpload(userID, uploadID); err != nil {
		return err
	}
	return s.queries.DeleteUpload(ctx, db.DeleteUploadParams{UserID: userID, ID: uploadID})
}

// StopUploads interrupts every upload that's receiving data, so each saves
// its progress before the server stops. Browsers continue them afterwards.
func (s *Service) StopUploads() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.active {
		w.stop()
	}
}

// Recover runs once at startup, before any request. After a power cut, a
// part file can be longer than what reached the disk (the rest reads back as
// zeros), so each unfinished upload is cut back to the bytes known to be
// safe, and continues from there.
func (s *Service) Recover(ctx context.Context) error {
	uploads, err := s.queries.AllUploads(ctx)
	if err != nil {
		return err
	}
	for _, upload := range uploads {
		file, size, err := s.files.OpenUpload(upload.UserID, upload.ID, false)
		if errors.Is(err, files.ErrNotFound) {
			continue // nothing arrived yet
		}
		if err != nil {
			s.logger.Warn("couldn't check an unfinished upload", "user_id", upload.UserID, "upload_id", upload.ID, "error", err)
			continue
		}
		if size > upload.Synced {
			if err := file.Truncate(upload.Synced); err != nil {
				s.logger.Warn("couldn't check an unfinished upload", "user_id", upload.UserID, "upload_id", upload.ID, "error", err)
			}
		}
		file.Close()
	}
	return nil
}

// ---- helpers -----------------------------------------------------------------

func checkUpload(up Upload) error {
	if !files.ValidUploadID(up.ID) {
		return files.ErrBadPath
	}
	if up.Size < 0 {
		return ErrBadSize
	}
	return nil
}

// lookup finds an unfinished upload, checking it's for the same file.
// It returns ErrUploadNotFound if there isn't one.
func (s *Service) lookup(ctx context.Context, userID string, up Upload) (Upload, *db.Upload, error) {
	row, err := s.queries.GetUpload(ctx, db.GetUploadParams{UserID: userID, ID: up.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return up, nil, ErrUploadNotFound
	}
	if err != nil {
		return up, nil, err
	}
	if row.Size != up.Size {
		return up, nil, ErrUploadMismatch
	}
	if up.Path != "" {
		canonical, _, err := files.Clean(up.Path)
		if err != nil {
			return up, nil, err
		}
		if canonical != row.Path {
			return up, nil, ErrUploadMismatch
		}
	}
	return fromRow(row), &row, nil
}

// create records a new upload, after checking it can be finished: the path
// is valid, its folder exists and the file fits on the drive.
func (s *Service) create(ctx context.Context, userID string, up Upload) (Upload, *db.Upload, error) {
	canonical, _, err := files.Clean(up.Path)
	if err != nil {
		return up, nil, err
	}
	if err := s.files.CheckUploadTarget(userID, canonical); err != nil {
		return up, nil, err
	}
	if err := s.checkSpace(up.Size); err != nil {
		return up, nil, err
	}
	now := s.now().Unix()
	row := db.Upload{
		UserID: userID, ID: up.ID, Path: canonical, Size: up.Size,
		Modified: toMillis(up.Modified), CreatedAt: now, UpdatedAt: now,
	}
	err = s.queries.CreateUpload(ctx, db.CreateUploadParams{
		UserID: row.UserID, ID: row.ID, Path: row.Path, Size: row.Size,
		Modified: row.Modified, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	})
	if db.IsUniqueViolation(err) {
		return s.lookup(ctx, userID, up) // another request created it a moment ago
	}
	if err != nil {
		return up, nil, err
	}
	return fromRow(row), &row, nil
}

// checkSpace makes sure needed more bytes fit on the drive, leaving some
// room so Windows keeps working.
func (s *Service) checkSpace(needed int64) error {
	free, err := s.files.FreeSpace()
	if err != nil {
		return nil // can't tell; a full drive is still reported by the write itself
	}
	if uint64(needed)+uint64(s.keepFree) > free {
		return files.ErrDiskFull
	}
	return nil
}

// receive copies body into file until the upload is complete, the body ends
// or a read fails. Every syncEvery bytes it flushes the file to disk in the
// background and records how far is safe, so receiving never waits for the
// disk. It returns read errors (the connection) separately from write errors
// (the disk).
func (s *Service) receive(ctx context.Context, userID string, file *os.File, body io.Reader, up *Upload) (readErr, writeErr error) {
	bufp := buffers.Get().(*[]byte)
	defer buffers.Put(bufp)
	buf := *bufp

	var flushes sync.WaitGroup
	defer flushes.Wait()
	flushing := make(chan struct{}, 1) // one background flush at a time
	lastFlush := up.Offset

	for up.Offset < up.Size {
		n, err := fill(body, buf[:min(int64(len(buf)), up.Size-up.Offset)])
		if n > 0 {
			if _, err := file.Write(buf[:n]); err != nil {
				return nil, err
			}
			up.Offset += int64(n)
			if up.Offset-lastFlush >= s.syncEvery {
				select {
				case flushing <- struct{}{}:
					lastFlush = up.Offset
					offset := up.Offset
					flushes.Go(func() {
						defer func() { <-flushing }()
						if file.Sync() == nil {
							s.saveProgress(ctx, userID, up.ID, offset)
						}
					})
				default: // the previous flush is still going; try after the next write
				}
			}
		}
		if err == io.EOF {
			return nil, nil // the request ended; another can continue
		}
		if err != nil {
			return err, nil
		}
	}
	return nil, nil
}

// fill reads into buf until it's full or the reader fails, returning io.EOF
// only if the body ended cleanly.
func fill(r io.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// pause flushes an unfinished upload to disk and records how far it got.
func (s *Service) pause(ctx context.Context, userID string, file *os.File, up Upload) {
	if err := file.Sync(); err != nil {
		s.logger.Warn("couldn't flush an unfinished upload", "upload_id", up.ID, "error", err)
		return
	}
	s.saveProgress(ctx, userID, up.ID, up.Offset)
}

// saveProgress records that the first synced bytes of an upload are safely on
// disk. It also marks the upload as recently active.
func (s *Service) saveProgress(ctx context.Context, userID, uploadID string, synced int64) {
	err := s.queries.SaveUploadProgress(context.WithoutCancel(ctx), db.SaveUploadProgressParams{
		Synced: synced, UpdatedAt: s.now().Unix(), UserID: userID, ID: uploadID,
	})
	if err != nil {
		s.logger.Warn("couldn't save an upload's progress", "upload_id", uploadID, "error", err)
	}
}

// finish moves a complete upload into place.
func (s *Service) finish(ctx context.Context, userID string, client auth.Client, up Upload) (files.Entry, error) {
	entry, err := s.files.FinishUpload(userID, up.ID, up.Path, up.Modified)
	if errors.Is(err, files.ErrNotFound) || errors.Is(err, files.ErrNotAFolder) {
		// Its folder was moved or deleted during the upload. Rather than
		// lose the file, put it in the top folder.
		entry, err = s.files.FinishUpload(userID, up.ID, "/"+path.Base(up.Path), up.Modified)
	}
	if err != nil {
		return files.Entry{}, err
	}
	ctx = context.WithoutCancel(ctx)
	if err := s.queries.DeleteUpload(ctx, db.DeleteUploadParams{UserID: userID, ID: up.ID}); err != nil {
		s.logger.Error("couldn't remove a finished upload's record", "upload_id", up.ID, "error", err)
	}
	s.remember(uploadKey{userID, up.ID}, entry)
	s.audit(ctx, userID, "uploaded", entry.Path, client)
	return entry, nil
}

// claim makes me the only request working on an upload. A request already
// receiving it is stopped first: a phone that switched networks resends
// from a new connection while the old one may take a minute to time out.
func (s *Service) claim(ctx context.Context, key uploadKey, me *writer) error {
	for {
		s.mu.Lock()
		old, busy := s.active[key]
		if !busy {
			s.active[key] = me
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
		old.takenOver.Store(true)
		old.stop()
		select {
		case <-old.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Service) release(key uploadKey, me *writer) {
	s.mu.Lock()
	if s.active[key] == me {
		delete(s.active, key)
	}
	s.mu.Unlock()
	close(me.done)
}

// receiving reports whether a request is working on an upload right now.
func (s *Service) receiving(key uploadKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, busy := s.active[key]
	return busy
}

func (s *Service) remember(key uploadKey, entry files.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, f := range s.recent {
		if now.Sub(f.at) > rememberFinished {
			delete(s.recent, k)
		}
	}
	s.recent[key] = finished{entry: entry, at: now}
}

func (s *Service) recall(key uploadKey) (files.Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.recent[key]
	if !ok || s.now().Sub(f.at) > rememberFinished {
		return files.Entry{}, false
	}
	return f.entry, true
}

func doneResult(up Upload, entry files.Entry) Result {
	up.Offset = up.Size
	return Result{Upload: up, File: &entry}
}

func fromRow(row db.Upload) Upload {
	up := Upload{ID: row.ID, Path: row.Path, Size: row.Size}
	if row.Modified != 0 {
		up.Modified = time.UnixMilli(row.Modified)
	}
	return up
}

func toMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
