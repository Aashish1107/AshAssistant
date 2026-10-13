// Package files lets a user browse and read the files in their own folder,
// <storage_dir>/users/<user id>/. Every access goes through os.Root, which
// refuses anything that would leave that folder, including via shortcuts
// (symbolic links) that point outside it.
package files

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/storage"
)

var (
	ErrBadPath    = errors.New("that isn't a valid path")
	ErrNotFound   = errors.New("not found")
	ErrNotAFolder = errors.New("that's a file, not a folder")
	ErrIsAFolder  = errors.New("that's a folder, not a file")
	ErrNoAccess   = errors.New("Windows won't let the server read that")
	ErrExists     = errors.New("something with that name already exists")
	ErrInUse      = errors.New("that file is open in another program (or still playing); try again in a moment")
	ErrDiskFull   = errors.New("the laptop's drive is full")
)

// hiddenNames are files the operating system creates in folders by itself.
var hiddenNames = []string{"desktop.ini", "thumbs.db", ".ds_store"}

// Entry is one file or folder in a listing.
type Entry struct {
	Name        string
	Path        string // canonical path, e.g. "/Photos/beach.jpg"
	IsFolder    bool
	Size        int64 // bytes; 0 for folders
	Modified    time.Time
	ContentType string // "" for folders
}

// File is an open file, ready to be sent to the browser. Close it when done.
type File struct {
	*os.File
	Path        string
	Size        int64
	Modified    time.Time
	ContentType string
}

// Store reads and changes users' files in the storage folder.
type Store struct {
	storageDir string
	// writeMu makes "check the name is free, then use it" atomic for
	// creating, moving, deleting and finishing uploads. Each is a quick
	// rename or mkdir, so holding it is cheap.
	writeMu sync.Mutex
}

func NewStore(storageDir string) *Store {
	return &Store{storageDir: storageDir}
}

// List returns the folder at path: its canonical path and its contents,
// folders first, then files, each sorted by name (ignoring case).
func (s *Store) List(userID, path string) (string, []Entry, error) {
	canonical, relative, err := Clean(path)
	if err != nil {
		return "", nil, err
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()

	dir, err := root.Open(filepath.FromSlash(relative))
	if err != nil {
		return "", nil, translate(err)
	}
	defer dir.Close()
	if info, err := dir.Stat(); err != nil {
		return "", nil, translate(err)
	} else if !info.IsDir() {
		return "", nil, ErrNotAFolder
	}
	items, err := dir.ReadDir(-1)
	if err != nil {
		return "", nil, translate(err)
	}

	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		name := item.Name()
		if !ValidName(name) || isReserved(name) || slices.Contains(hiddenNames, strings.ToLower(name)) {
			continue
		}
		info, err := item.Info()
		if err == nil && item.Type()&fs.ModeSymlink != 0 {
			// Follow the shortcut, but only if it stays inside the folder.
			info, err = root.Stat(filepath.Join(filepath.FromSlash(relative), name))
		}
		if err != nil || !(info.IsDir() || info.Mode().IsRegular()) {
			continue // vanished, points outside, or isn't a plain file or folder
		}
		entry := Entry{
			Name:     name,
			Path:     strings.TrimSuffix(canonical, "/") + "/" + name,
			IsFolder: info.IsDir(),
			Modified: info.ModTime(),
		}
		if !entry.IsFolder {
			entry.Size = info.Size()
			entry.ContentType = ContentType(name)
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		if a.IsFolder != b.IsFolder {
			if a.IsFolder {
				return -1
			}
			return 1
		}
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return canonical, entries, nil
}

// Open opens the file at path for reading.
func (s *Store) Open(userID, path string) (*File, error) {
	canonical, relative, err := Clean(path)
	if err != nil {
		return nil, err
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return nil, err
	}
	// The open file stays valid after the root handle is closed.
	defer root.Close()

	file, err := root.Open(filepath.FromSlash(relative))
	if err != nil {
		return nil, translate(err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, translate(err)
	}
	if info.IsDir() {
		file.Close()
		return nil, ErrIsAFolder
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, ErrNotFound
	}
	return &File{
		File:        file,
		Path:        canonical,
		Size:        info.Size(),
		Modified:    info.ModTime(),
		ContentType: ContentType(info.Name()),
	}, nil
}

// LocalFile is where a file is on disk, for programs such as ffmpeg that
// need a real path rather than an open file.
type LocalFile struct {
	Path        string // canonical path, e.g. "/Videos/movie.mp4"
	Local       string // absolute path on disk
	Size        int64
	Modified    time.Time
	ContentType string
}

// Local finds the file at path on disk. It's opened through os.Root first,
// so the path is known to stay inside the user's folder, shortcuts included.
func (s *Store) Local(userID, path string) (LocalFile, error) {
	file, err := s.Open(userID, path)
	if err != nil {
		return LocalFile{}, err
	}
	file.Close()
	_, relative, _ := Clean(path)
	dir, err := storage.UserDir(s.storageDir, userID)
	if err != nil {
		return LocalFile{}, err
	}
	local, err := filepath.Abs(filepath.Join(dir, filepath.FromSlash(relative)))
	if err != nil {
		return LocalFile{}, err
	}
	return LocalFile{Path: file.Path, Local: local, Size: file.Size, Modified: file.Modified, ContentType: file.ContentType}, nil
}

// openRoot opens the user's folder, creating it if it doesn't exist yet.
func (s *Store) openRoot(userID string) (*os.Root, error) {
	dir, err := storage.CreateUserDir(s.storageDir, userID)
	if err != nil {
		return nil, err
	}
	return os.OpenRoot(dir)
}

// TranslateError turns an error from reading or writing a user's file into
// this package's errors.
func TranslateError(err error) error {
	return translate(err)
}

// translate turns file system errors into this package's errors.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, fs.ErrExist):
		return ErrExists
	case isInUse(err):
		return ErrInUse
	case isDiskFull(err):
		return ErrDiskFull
	case errors.Is(err, fs.ErrPermission):
		return ErrNoAccess
	case strings.Contains(err.Error(), "path escapes from parent"):
		// os.Root refused a shortcut pointing outside the user's folder.
		// (Its error value isn't exported; a test pins this message.)
		return ErrNotFound
	default:
		return err
	}
}
