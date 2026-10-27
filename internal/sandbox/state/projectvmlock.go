package state

import "path/filepath"

// ProjectVMLockDir returns the directory holding the project's VM-creation lock
// file. It lives under the never-deleted locksDir tree, not the removable key
// directory, so pruning a project's state cannot unlink the lock a concurrent
// VM creation holds (#330). It is keyed by agent as well as slug: agents of one
// project use separate VMs, so a shared lock would serialize unrelated creates.
func ProjectVMLockDir(k Key) string {
	return filepath.Join(locksDir(), k.Slug, k.Agent)
}
