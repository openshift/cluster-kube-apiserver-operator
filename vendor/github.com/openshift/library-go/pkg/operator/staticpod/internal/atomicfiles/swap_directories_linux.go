//go:build linux

package atomicfiles

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// SwapDirectories can be used to swap two directories atomically.
//
// This function requires absolute paths and will return an error if that's not the case.
func SwapDirectories(dirA, dirB string) error {
	if !filepath.IsAbs(dirA) {
		return fmt.Errorf("not an absolute path: %q", dirA)
	}
	if !filepath.IsAbs(dirB) {
		return fmt.Errorf("not an absolute path: %q", dirB)
	}

	// Renameat2 can be used to exchange two directories atomically when RENAME_EXCHANGE flag is specified.
	// The paths to be exchanged can be specified in multiple ways:
	//
	//   * You can specify a file descriptor and a relative path to that descriptor.
	//   * You can specify an absolute path, in which case the file descriptor is ignored.
	//
	// We make sure the path is absolute, hence we pass 0 as the file descriptor as it is ignored.
	//
	// For more details, see `man renameat2` as that is the associated C library function.
	return unix.Renameat2(0, dirA, 0, dirB, unix.RENAME_EXCHANGE)
}
