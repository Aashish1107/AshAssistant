// Package drive changes users' files: resumable uploads, new folders, renames
// and moves, and deletes into a trash that empties itself after 30 days.
// Every change is recorded in the audit log.
package drive

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/db"
	"github.com/Aashish1107/AshAssistant/server/internal/files"
	"github.com/Aashish1107/AshAssistant/server/internal/ids"
)

const (
	// TrashAge is how long deleted items stay restorable.
	TrashAge = 30 * 24 * time.Hour
	// StaleUploadAge is how long an unfinished upload is kept with nobody
	// continuing it. Picking the same file again within that time continues
	// where it stopped.
	StaleUploadAge = 7 * 24 * time.Hour
	// cleanupInterval is how often expired trash and stale uploads are removed.
	cleanupInterval = time.Hour
	// maxDetailLength matches the audit log's limit.
	maxDetailLength = 64
)

// ErrTrashNotFound means there's no trashed item with that ID (it may have
// been emptied already).
var ErrTrashNotFound = errors.New("that item isn't in the trash any more")

// Options tunes the Service. Zero values get the defaults shown.
type Options struct {
	Logger *slog.Logger // where problems are reported (discarded)
	// SyncEvery is how many bytes an upload receives between flushes to
	// disk (32 MiB). After a power cut, an upload continues from the last
	// flush. Flushing often also makes uploads faster: unflushed data
	// never piles up to where the operating system slows writes down
	// (measured: 1 GiB in 0.8 s with 32 MiB, 5 s with one flush at the end).
	SyncEvery int64
	// KeepFree is how much of the drive an upload must leave free, so
	// Windows keeps working (512 MiB).
	KeepFree int64
}

// Service changes users' files.
type Service struct {
	queries   *db.Queries
	files     *files.Store
	logger    *slog.Logger
	syncEvery int64
	keepFree  int64
	now       func() time.Time

	mu     sync.Mutex
	active map[uploadKey]*writer  // uploads receiving data right now
	recent map[uploadKey]finished // uploads finished in the last few minutes
}

// NewService returns a Service that stores files in store and keeps its
// records in database.
func NewService(database *sql.DB, store *files.Store, opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.SyncEvery <= 0 {
		opts.SyncEvery = 32 << 20
	}
	if opts.KeepFree <= 0 {
		opts.KeepFree = 512 << 20
	}
	return &Service{
		queries:   db.New(database),
		files:     store,
		logger:    opts.Logger,
		syncEvery: opts.SyncEvery,
		keepFree:  opts.KeepFree,
		now:       time.Now,
		active:    make(map[uploadKey]*writer),
		recent:    make(map[uploadKey]finished),
	}
}

// MakeFolder creates a folder.
func (s *Service) MakeFolder(ctx context.Context, userID string, client auth.Client, path string) (files.Entry, error) {
	entry, err := s.files.MakeFolder(userID, path)
	if err != nil {
		return files.Entry{}, err
	}
	s.audit(ctx, userID, "folder_created", entry.Path, client)
	return entry, nil
}

// Move renames or moves a file or folder. It never overwrites anything.
func (s *Service) Move(ctx context.Context, userID string, client auth.Client, from, to string) (files.Entry, error) {
	entry, err := s.files.Move(userID, from, to)
	if err != nil {
		return files.Entry{}, err
	}
	s.audit(ctx, userID, "moved", entry.Path, client)
	return entry, nil
}

// Delete moves a file or folder to the trash and returns the trash ID that
// Restore takes. It's a single rename, so it's instant even for a huge folder.
func (s *Service) Delete(ctx context.Context, userID string, client auth.Client, path string) (string, error) {
	canonical, _, err := files.Clean(path)
	if err != nil {
		return "", err
	}
	trashID := ids.New()
	// The record is written first: if the server stopped between the two
	// steps, an item in the trash with no record would never be emptied.
	if err := s.queries.AddTrash(ctx, db.AddTrashParams{
		ID: trashID, UserID: userID, OriginalPath: canonical, DeletedAt: s.now().Unix(),
	}); err != nil {
		return "", err
	}
	isFolder, err := s.files.Trash(userID, canonical, trashID)
	if err != nil {
		s.queries.DeleteTrash(context.WithoutCancel(ctx), db.DeleteTrashParams{UserID: userID, ID: trashID})
		return "", err
	}
	if isFolder {
		// Only informational; a failure doesn't matter.
		s.queries.MarkTrashFolder(ctx, db.MarkTrashFolderParams{UserID: userID, ID: trashID})
	}
	s.audit(ctx, userID, "deleted", canonical, client)
	return trashID, nil
}

// Restore puts a deleted item back where it was.
func (s *Service) Restore(ctx context.Context, userID string, client auth.Client, trashID string) (files.Entry, error) {
	if !files.ValidUploadID(trashID) {
		return files.Entry{}, ErrTrashNotFound
	}
	item, err := s.queries.GetTrash(ctx, db.GetTrashParams{UserID: userID, ID: trashID})
	if errors.Is(err, sql.ErrNoRows) {
		return files.Entry{}, ErrTrashNotFound
	}
	if err != nil {
		return files.Entry{}, err
	}
	entry, err := s.files.Restore(userID, trashID, item.OriginalPath)
	if errors.Is(err, files.ErrNotFound) {
		return files.Entry{}, ErrTrashNotFound
	}
	if err != nil {
		return files.Entry{}, err
	}
	if err := s.queries.DeleteTrash(context.WithoutCancel(ctx), db.DeleteTrashParams{UserID: userID, ID: trashID}); err != nil {
		s.logger.Error("couldn't remove a restored item's trash record", "trash_id", trashID, "error", err)
	}
	s.audit(ctx, userID, "restored", entry.Path, client)
	return entry, nil
}

// Run removes expired trash and abandoned uploads now and then every hour,
// until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		s.Cleanup(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Cleanup permanently deletes trash older than TrashAge and unfinished
// uploads nobody has continued for StaleUploadAge.
func (s *Service) Cleanup(ctx context.Context) {
	now := s.now()
	if expired, err := s.queries.ExpiredTrash(ctx, now.Add(-TrashAge).Unix()); err != nil {
		s.logger.Warn("couldn't look for expired trash", "error", err)
	} else {
		for _, item := range expired {
			if err := s.files.EmptyTrashItem(item.UserID, item.ID); err != nil {
				s.logger.Warn("couldn't empty a trash item", "user_id", item.UserID, "trash_id", item.ID, "error", err)
				continue
			}
			s.queries.DeleteTrash(ctx, db.DeleteTrashParams{UserID: item.UserID, ID: item.ID})
		}
		if len(expired) > 0 {
			s.logger.Info("emptied expired trash", "items", len(expired))
		}
	}

	stale, err := s.queries.StaleUploads(ctx, now.Add(-StaleUploadAge).Unix())
	if err != nil {
		s.logger.Warn("couldn't look for abandoned uploads", "error", err)
		return
	}
	removed := 0
	for _, upload := range stale {
		if s.receiving(uploadKey{upload.UserID, upload.ID}) {
			continue
		}
		if err := s.files.RemoveUpload(upload.UserID, upload.ID); err != nil {
			s.logger.Warn("couldn't remove an abandoned upload", "user_id", upload.UserID, "upload_id", upload.ID, "error", err)
			continue
		}
		s.queries.DeleteUpload(ctx, db.DeleteUploadParams{UserID: upload.UserID, ID: upload.ID})
		removed++
	}
	if removed > 0 {
		s.logger.Info("removed abandoned uploads", "count", removed)
	}
}

// audit records a change. A failure to record it is logged rather than
// failing the request, since the change itself has already happened.
func (s *Service) audit(ctx context.Context, userID, action, path string, client auth.Client) {
	err := s.queries.AddAuditEntry(context.WithoutCancel(ctx), db.AddAuditEntryParams{
		UserID: sql.NullString{String: userID, Valid: true},
		Action: action,
		Detail: tail(path, maxDetailLength),
		Ip:     client.IP,
		At:     s.now().Unix(),
	})
	if err != nil {
		s.logger.Error("couldn't write audit log", "action", action, "error", err)
	}
}

// tail shortens s to its last n characters, since the end of a path (the
// file's name) says the most.
func tail(s string, n int) string {
	count := utf8.RuneCountInString(s)
	if count <= n {
		return s
	}
	runes := []rune(s)
	return "…" + string(runes[count-n+1:])
}
