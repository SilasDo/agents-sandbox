package git

import (
	"os"
	"path/filepath"
	"strings"
)

// findWorktreeRoot returns the nearest directory at or above cwd that contains a
// .git entry (a directory for a normal checkout, a file for a linked worktree or
// a bare layout's worktree), or "" when none is found.
func findWorktreeRoot(cwd string) string {
	dir := cwd
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// within reports whether child is parent or under it.
func within(parent, child string) bool {
	if child == parent {
		return true
	}
	prefix := parent
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(child, prefix)
}
