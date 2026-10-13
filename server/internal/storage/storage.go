// Package storage manages the folder that holds users' files.
//
// Layout: <storage_dir>/users/<user id>/... ("1" for the first admin, a Nano
// ID for everyone else), so adding users never requires moving anyone's files.
package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// userIDPattern accepts "1" and Nano IDs. Checking it keeps an ID from ever
// being turned into a path like "../other".
var userIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// UserDir is the folder that holds one user's files.
func UserDir(root, userID string) (string, error) {
	if !userIDPattern.MatchString(userID) {
		return "", fmt.Errorf("invalid user id %q", userID)
	}
	return filepath.Join(root, "users", userID), nil
}

// CreateUserDir creates the user's folder if it doesn't exist and returns its path.
func CreateUserDir(root, userID string) (string, error) {
	dir, err := UserDir(root, userID)
	if err != nil {
		return "", err
	}
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
