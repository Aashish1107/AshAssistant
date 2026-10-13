//go:build !windows

package files

import (
	"errors"

	"golang.org/x/sys/unix"
)

// isInUse reports a file locked by another program. Only Windows locks open
// files against renaming.
func isInUse(err error) bool {
	return false
}

// isDiskFull reports that the drive has no room left.
func isDiskFull(err error) bool {
	return errors.Is(err, unix.ENOSPC) || errors.Is(err, unix.EDQUOT)
}

// freeSpace returns how many bytes can still be written to dir's drive.
func freeSpace(dir string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
