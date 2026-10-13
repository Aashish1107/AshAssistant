package files

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// uploadIDPattern matches the IDs browsers choose for uploads (Nano IDs).
var uploadIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// ValidUploadID reports whether id can name an upload.
func ValidUploadID(id string) bool {
	return uploadIDPattern.MatchString(id)
}

// MakeFolder creates the folder at path. Its parent must already exist.
func (s *Store) MakeFolder(userID, path string) (Entry, error) {
	canonical, relative, err := Clean(path)
	if err != nil {
		return Entry{}, err
	}
	if relative == "." {
		return Entry{}, ErrExists
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := requireFolder(root, filepathDir(relative)); err != nil {
		return Entry{}, err
	}
	if err := root.Mkdir(filepath.FromSlash(relative), 0o700); err != nil {
		return Entry{}, translate(err)
	}
	return entryAt(root, canonical, relative)
}

// Move renames or moves a file or folder from one path to another. Nothing
// is ever overwritten: if the destination exists, it fails with ErrExists.
func (s *Store) Move(userID, from, to string) (Entry, error) {
	fromCanonical, fromRelative, err := Clean(from)
	if err != nil {
		return Entry{}, err
	}
	toCanonical, toRelative, err := Clean(to)
	if err != nil {
		return Entry{}, err
	}
	if fromRelative == "." || toRelative == "." {
		return Entry{}, ErrBadPath // the top folder itself can't move
	}
	if toCanonical == fromCanonical {
		return Entry{}, ErrExists
	}
	// Windows ignores case, so "a.jpg" → "A.jpg" is a rename of the same item.
	sameItem := strings.EqualFold(fromCanonical, toCanonical)
	if !sameItem && strings.HasPrefix(strings.ToLower(toCanonical)+"/", strings.ToLower(fromCanonical)+"/") {
		return Entry{}, ErrIntoItself
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := root.Lstat(filepath.FromSlash(fromRelative)); err != nil {
		return Entry{}, translate(err)
	}
	if err := requireFolder(root, filepathDir(toRelative)); err != nil {
		return Entry{}, err
	}
	if !sameItem {
		if _, err := root.Lstat(filepath.FromSlash(toRelative)); err == nil {
			return Entry{}, ErrExists
		}
	}
	if err := rename(root, filepath.FromSlash(fromRelative), filepath.FromSlash(toRelative)); err != nil {
		return Entry{}, translate(err)
	}
	return entryAt(root, toCanonical, toRelative)
}

// ErrIntoItself means a folder can't be moved inside itself.
var ErrIntoItself = errors.New("a folder can't be moved into itself")

// Trash moves the file or folder at path into the user's trash, under
// trashID. It's one rename, so it's instant even for a huge folder.
func (s *Store) Trash(userID, path, trashID string) (isFolder bool, err error) {
	_, relative, err := Clean(path)
	if err != nil {
		return false, err
	}
	if relative == "." {
		return false, ErrBadPath
	}
	if !ValidUploadID(trashID) {
		return false, ErrBadPath
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return false, err
	}
	defer root.Close()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	info, err := root.Lstat(filepath.FromSlash(relative))
	if err != nil {
		return false, translate(err)
	}
	holder := filepath.Join(trashDir, trashID)
	if err := root.MkdirAll(holder, 0o700); err != nil {
		return false, translate(err)
	}
	if err := rename(root, filepath.FromSlash(relative), filepath.Join(holder, info.Name())); err != nil {
		root.Remove(holder)
		return false, translate(err)
	}
	return info.IsDir(), nil
}

// Restore puts a trashed item back at originalPath, recreating its folder if
// that was deleted too. If the name has been taken since, it becomes
// "name (1).ext".
func (s *Store) Restore(userID, trashID, originalPath string) (Entry, error) {
	canonical, relative, err := Clean(originalPath)
	if err != nil {
		return Entry{}, err
	}
	if relative == "." || !ValidUploadID(trashID) {
		return Entry{}, ErrBadPath
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()

	holder := filepath.Join(trashDir, trashID)
	dir, err := root.Open(holder)
	if err != nil {
		return Entry{}, translate(err)
	}
	items, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return Entry{}, translate(err)
	}
	if len(items) != 1 {
		return Entry{}, ErrNotFound
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if parent := filepathDir(relative); parent != "." {
		if err := root.MkdirAll(filepath.FromSlash(parent), 0o700); err != nil {
			return Entry{}, translate(err)
		}
	}
	relative, canonical = freeName(root, relative, canonical)
	if err := rename(root, filepath.Join(holder, items[0].Name()), filepath.FromSlash(relative)); err != nil {
		return Entry{}, translate(err)
	}
	root.Remove(holder)
	return entryAt(root, canonical, relative)
}

// EmptyTrashItem permanently deletes one trashed item.
func (s *Store) EmptyTrashItem(userID, trashID string) error {
	if !ValidUploadID(trashID) {
		return ErrBadPath
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return err
	}
	defer root.Close()
	return translate(root.RemoveAll(filepath.Join(trashDir, trashID)))
}

// ---- uploads -----------------------------------------------------------------

// OpenUpload opens an upload's partial file for appending, creating it when
// create is true, and returns how many bytes it already holds.
func (s *Store) OpenUpload(userID, uploadID string, create bool) (*os.File, int64, error) {
	if !ValidUploadID(uploadID) {
		return nil, 0, ErrBadPath
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	if create {
		if err := root.MkdirAll(uploadsDir, 0o700); err != nil {
			return nil, 0, translate(err)
		}
	}
	flags := os.O_WRONLY | os.O_APPEND
	if create {
		flags |= os.O_CREATE | os.O_TRUNC
	}
	file, err := root.OpenFile(uploadPart(uploadID), flags, 0o600)
	if err != nil {
		return nil, 0, translate(err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, translate(err)
	}
	return file, info.Size(), nil
}

// UploadSize returns how many bytes an upload's partial file holds.
func (s *Store) UploadSize(userID, uploadID string) (int64, error) {
	if !ValidUploadID(uploadID) {
		return 0, ErrBadPath
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	info, err := root.Stat(uploadPart(uploadID))
	if err != nil {
		return 0, translate(err)
	}
	return info.Size(), nil
}

// FreeSpace returns how many bytes can still be stored.
func (s *Store) FreeSpace() (uint64, error) {
	return freeSpace(s.storageDir)
}

// CheckUploadTarget makes sure an upload to path could be finished: the path
// is valid and its folder exists.
func (s *Store) CheckUploadTarget(userID, path string) error {
	_, relative, err := Clean(path)
	if err != nil {
		return err
	}
	if relative == "." {
		return ErrBadPath
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return err
	}
	defer root.Close()
	return requireFolder(root, filepathDir(relative))
}

// FinishUpload moves a complete upload into place at path. If that name is
// taken, it becomes "name (1).ext", "name (2).ext"… — never overwriting.
// modified, if not zero, becomes the file's last-modified time, so photos
// keep the date they were taken.
func (s *Store) FinishUpload(userID, uploadID, path string, modified time.Time) (Entry, error) {
	canonical, relative, err := Clean(path)
	if err != nil {
		return Entry{}, err
	}
	if relative == "." || !ValidUploadID(uploadID) {
		return Entry{}, ErrBadPath
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := requireFolder(root, filepathDir(relative)); err != nil {
		return Entry{}, err
	}
	relative, canonical = freeName(root, relative, canonical)
	if err := rename(root, uploadPart(uploadID), filepath.FromSlash(relative)); err != nil {
		return Entry{}, translate(err)
	}
	if !modified.IsZero() {
		root.Chtimes(filepath.FromSlash(relative), time.Now(), modified)
	}
	return entryAt(root, canonical, relative)
}

// RemoveUpload deletes an upload's partial file.
func (s *Store) RemoveUpload(userID, uploadID string) error {
	if !ValidUploadID(uploadID) {
		return ErrBadPath
	}
	root, err := s.openRoot(userID)
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.Remove(uploadPart(uploadID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return translate(err)
}

func uploadPart(uploadID string) string {
	return filepath.Join(uploadsDir, uploadID+".part")
}

// ---- helpers -----------------------------------------------------------------

// rename renames from to to. On Windows a file can't be renamed while any
// program has it open, and the antivirus briefly opens every new file to scan
// it, so a rename that hits that is retried for up to about a second.
func rename(root *os.Root, from, to string) error {
	err := root.Rename(from, to)
	for attempt := 1; attempt <= 8 && isInUse(err); attempt++ {
		time.Sleep(time.Duration(attempt) * 25 * time.Millisecond)
		err = root.Rename(from, to)
	}
	return err
}

func filepathDir(relative string) string {
	dir := path.Dir(relative)
	if dir == "" {
		return "."
	}
	return dir
}

// requireFolder checks that relative (slash-separated) is an existing folder.
func requireFolder(root *os.Root, relative string) error {
	info, err := root.Stat(filepath.FromSlash(relative))
	if err != nil {
		return translate(err)
	}
	if !info.IsDir() {
		return ErrNotAFolder
	}
	return nil
}

// freeName returns relative unchanged if nothing has that name, or the
// first free "name (n).ext" otherwise.
func freeName(root *os.Root, relative, canonical string) (string, string) {
	dir, name := path.Split(relative)
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	candidate := relative
	for n := 1; ; n++ {
		if _, err := root.Lstat(filepath.FromSlash(candidate)); errors.Is(err, fs.ErrNotExist) {
			break
		}
		candidate = dir + stem + " (" + strconv.Itoa(n) + ")" + ext
	}
	if candidate == relative {
		return relative, canonical
	}
	return candidate, "/" + candidate
}

func entryAt(root *os.Root, canonical, relative string) (Entry, error) {
	info, err := root.Stat(filepath.FromSlash(relative))
	if err != nil {
		return Entry{}, translate(err)
	}
	entry := Entry{Name: info.Name(), Path: canonical, IsFolder: info.IsDir(), Modified: info.ModTime()}
	if !entry.IsFolder {
		entry.Size = info.Size()
		entry.ContentType = ContentType(info.Name())
	}
	return entry, nil
}
