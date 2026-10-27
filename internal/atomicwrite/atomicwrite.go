// Package atomicwrite writes launcher state and configuration files atomically.
package atomicwrite

import (
	"fmt"
	"os"
	"path/filepath"
)

// Filesystem seams. Writing to, flushing, and closing a freshly created temp
// file only fail in situations (e.g. a full disk) that a test filesystem cannot
// produce, so tests substitute them to exercise the error handling.
var (
	writeTempFile = func(f *os.File, data []byte) error {
		_, err := f.Write(data)
		return err
	}
	syncTempFile  = (*os.File).Sync
	closeTempFile = (*os.File).Close
	openDirectory = os.Open
	syncDirectory = (*os.File).Sync
)

// WriteFile atomically replaces path with data, creating the parent directory
// (mode 0700) if needed. It writes a uniquely named temporary file in the
// destination directory, flushes the temp file with fsync, then renames it into
// place: a reader never observes a partial file, and two concurrent writers
// cannot collide on the temporary name (the last writer wins). The directory
// entry the rename creates is fsynced too, so a completed write survives a
// crash or power loss. The file is written with mode 0600.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if err := writeTempFile(tmp, data); err != nil {
		_ = closeTempFile(tmp)
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := syncTempFile(tmp); err != nil {
		_ = closeTempFile(tmp)
		_ = os.Remove(tmpName)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := closeTempFile(tmp); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return syncParentDirectory(dir)
}

// syncParentDirectory flushes the directory entry the rename created, so the
// file replacing it survives a crash or power loss.
func syncParentDirectory(dir string) error {
	handle, err := openDirectory(dir)
	if err != nil {
		return fmt.Errorf("open dir %s: %w", dir, err)
	}
	defer func() { _ = handle.Close() }()
	if err := syncDirectory(handle); err != nil {
		return fmt.Errorf("sync dir %s: %w", dir, err)
	}
	return nil
}
