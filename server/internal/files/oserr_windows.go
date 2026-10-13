package files

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// isInUse reports Windows' "the process cannot access the file because it is
// being used by another process" (ERROR_SHARING_VIOLATION), e.g. renaming a
// video that's being played, or a file the antivirus is still scanning.
func isInUse(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == 32 || errno == 33) // ERROR_SHARING_VIOLATION, ERROR_LOCK_VIOLATION
}

// isDiskFull reports that the drive has no room left.
func isDiskFull(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == 112 || errno == 39) // ERROR_DISK_FULL, ERROR_HANDLE_DISK_FULL
}

// freeSpace returns how many bytes can still be written to dir's drive.
func freeSpace(dir string) (uint64, error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(path, &available, &total, &free); err != nil {
		return 0, err
	}
	return available, nil
}
