package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// SlugDir returns the state directory holding everything one project slug owns:
// the per-agent state files, the per-project claims, and the per-agent client
// leases. It is the agent-agnostic parent of KeyDir.
func SlugDir(slug string) string {
	return slugDir(slug)
}

// MoveSlugDir moves every entry of SlugDir(fromSlug) into SlugDir(toSlug) and
// removes the source directory once it is left empty.
//
// Entries the destination already has are neither overwritten nor merged: they
// are left in place and their names reported in skipped. Adoption of a legacy
// project identity (#144) needs exactly this tolerance, because the trigger
// deliberately ignores a stray lock file or clients/ directory an aborted run
// left at the destination — a whole-directory rename would fail there with
// ENOTEMPTY. A skipped entry is not an error; the caller warns and continues.
//
// A missing source directory is a no-op. Both directories live under the one
// state root, so os.Rename cannot cross a filesystem and no copy fallback is
// needed.
//
//nolint:nonamedreturns // the documented (skipped, err) signature is the contract
func MoveSlugDir(fromSlug, toSlug string) (skipped []string, err error) {
	if fromSlug == toSlug {
		return nil, nil
	}
	fromDir, toDir := SlugDir(fromSlug), SlugDir(toSlug)

	entries, err := os.ReadDir(fromDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read state dir %s: %w", fromDir, err)
	}

	for _, entry := range entries {
		name := entry.Name()
		dst := filepath.Join(toDir, name)
		if _, statErr := os.Lstat(dst); statErr == nil {
			skipped = append(skipped, name)
			continue
		} else if !os.IsNotExist(statErr) {
			return skipped, fmt.Errorf("stat %s: %w", dst, statErr)
		}
		if err := os.MkdirAll(toDir, 0o700); err != nil {
			return skipped, fmt.Errorf("create state dir %s: %w", toDir, err)
		}
		if err := os.Rename(filepath.Join(fromDir, name), dst); err != nil {
			return skipped, fmt.Errorf("move %s to %s: %w", name, dst, err)
		}
	}

	if err := os.Remove(fromDir); err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTEMPTY) {
		return skipped, fmt.Errorf("remove state dir %s: %w", fromDir, err)
	}
	return skipped, nil
}
