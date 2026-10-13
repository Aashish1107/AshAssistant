// Package storage manages the folder that holds users' files.
//
// Layout: <storage_dir>/users/<user id>/... so adding more users later never
// requires moving anyone's files.
package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// UserDir is the folder that holds one user's files.
func UserDir(root string, userID int64) string {
	return filepath.Join(root, "users", strconv.FormatInt(userID, 10))
}

// CreateUserDir creates the user's folder if it doesn't exist and returns its path.
func CreateUserDir(root string, userID int64) (string, error) {
	dir := UserDir(root, userID)
	return dir, os.MkdirAll(dir, 0o700)
}

// CheckWritable proves new files can be created in dir by writing and
// deleting a small temporary file. Used by /readyz.
func CheckWritable(dir string) error {
	file, err := os.CreateTemp(dir, ".ashcloud-check-*")
	if err != nil {
		return err
	}
	_, writeErr := file.Write([]byte("ok"))
	closeErr := file.Close()
	removeErr := os.Remove(file.Name())
	return errors.Join(writeErr, closeErr, removeErr)
}
