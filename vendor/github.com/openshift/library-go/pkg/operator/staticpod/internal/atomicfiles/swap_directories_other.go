//go:build !linux

package atomicfiles

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
)

// SwapDirectories swaps two directories, but NOT atomically in this case.
// Atomic implementation is only available for Linux.
// This function is essentially a mock for tests, and it simply uses os.Rename.
// In case there is any error, the swapping process is left in an inconsistent state.
func SwapDirectories(dirA, dirB string) error {
	// Still retain the constraints as in the Linux implementation.
	if !filepath.IsAbs(dirA) {
		return fmt.Errorf("not an absolute path: %q", dirA)
	}
	if !filepath.IsAbs(dirB) {
		return fmt.Errorf("not an absolute path: %q", dirB)
	}

	// Rename dirA -> prevDirA.
	prevDirA := fmt.Sprintf("%s-%d", dirA, rand.Int64())
	if err := os.Rename(dirA, prevDirA); err != nil {
		return fmt.Errorf("failed to rename %q to %q: %w", dirA, prevDirA, err)
	}

	// Rename dirB -> dirA.
	if err := os.Rename(dirB, dirA); err != nil {
		return fmt.Errorf("failed to rename %q to %q: %w", dirB, dirA, err)
	}

	// Rename prevDirA -> dirB.
	if err := os.Rename(prevDirA, dirB); err != nil {
		return fmt.Errorf("failed to rename %q to %q: %w", prevDirA, dirB, err)
	}
	return nil
}
