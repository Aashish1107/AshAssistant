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
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/storage"
)

var (
	ErrBadPath    = errors.New("that isn't a valid path")
	ErrNotFound   = errors.New("not found")
	ErrNotAFolder = errors.New("that's a file, not a folder")
	ErrIsAFolder  = errors.New("that's a folder, not a file")
	ErrNoAccess   = errors.New("Windows won't let the server read that")
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

// Store reads users' files from the storage folder.
type Store struct {
	storageDir string
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
		if !ValidName(name) || slices.Contains(hiddenNames, strings.ToLower(name)) {
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

// openRoot opens the user's folder, creating it if it doesn't exist yet.
func (s *Store) openRoot(userID string) (*os.Root, error) {
	dir, err := storage.CreateUserDir(s.storageDir, userID)
	if err != nil {
		return nil, err
	}
	return os.OpenRoot(dir)
}

// translate turns file system errors into this package's errors.
func translate(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
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
