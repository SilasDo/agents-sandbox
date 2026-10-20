package git

import (
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceLayout describes which host directory a session should bind at
// /workspace and where its checkout lives.
//
// TODO(#144): replaced by ResolveIdentity; deleted by the cmd wiring task.
type WorkspaceLayout struct {
	BindRoot        string // host dir to bind at /workspace
	CheckoutRoot    string // enclosing checkout root; "" when cwd is not in a repo
	WorktreeRoot    string // nearest dir holding a .git entry; "" when cwd is not in a repo
	OutsideWorktree bool   // cwd is a linked worktree outside CheckoutRoot
	Bare            bool   // the shared git dir is a bare repository
}

// ResolveWorkspaceLayout returns the layout fields of ResolveIdentity. It is kept
// as a shim for configpaths' bare project-config resolution (#247) until the
// #144 wiring migrates that caller; deleted with the cmd wiring task.
//
// TODO(#144): replaced by ResolveIdentity; deleted by the cmd wiring task.
func ResolveWorkspaceLayout(cwd string) WorkspaceLayout {
	identity := ResolveIdentity(cwd)
	return WorkspaceLayout{
		BindRoot:        identity.BindRoot,
		CheckoutRoot:    identity.CheckoutRoot,
		WorktreeRoot:    identity.WorktreeRoot,
		OutsideWorktree: identity.OutsideWorktree,
		Bare:            identity.Bare,
	}
}

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
