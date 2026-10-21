package git

import (
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceLayout describes which host directory a session should bind at
// /workspace and where its checkout lives.
type WorkspaceLayout struct {
	BindRoot        string // host dir to bind at /workspace
	CheckoutRoot    string // enclosing checkout root; "" when cwd is not in a repo
	OutsideWorktree bool   // cwd is a linked worktree outside CheckoutRoot
	Bare            bool   // the shared git dir is a bare repository
}

// ResolveWorkspaceLayout decides which host directory to bind at /workspace for
// a session started at cwd. It binds the smallest enclosing checkout that
// contains cwd, so a session in a worktree nested inside its checkout (or in a
// worktree of a bare layout, whose worktrees live inside the bare root) always
// exposes that worktree. A worktree outside its checkout keeps cwd as the bind
// root and is flagged, since its shared git dir would not be reachable in the
// VM. The layout also reports whether the shared git dir is a bare repository
// (see isBare). Derivation is plain-file inspection (see resolveGitDir); no git
// binary is invoked.
func ResolveWorkspaceLayout(cwd string) WorkspaceLayout {
	worktreeRoot := findWorktreeRoot(cwd)
	if worktreeRoot == "" {
		return WorkspaceLayout{BindRoot: cwd, CheckoutRoot: "", OutsideWorktree: false, Bare: false}
	}
	commonGitDir := resolveGitDir(worktreeRoot)
	if commonGitDir == "" {
		return WorkspaceLayout{BindRoot: cwd, CheckoutRoot: "", OutsideWorktree: false, Bare: false}
	}
	checkoutRoot := filepath.Dir(commonGitDir)
	if filepath.Base(commonGitDir) != ".git" {
		checkoutRoot = commonGitDir
	}
	if within(checkoutRoot, worktreeRoot) {
		return WorkspaceLayout{
			BindRoot:        checkoutRoot,
			CheckoutRoot:    checkoutRoot,
			OutsideWorktree: false,
			Bare:            isBare(commonGitDir),
		}
	}
	return WorkspaceLayout{BindRoot: cwd, CheckoutRoot: checkoutRoot, OutsideWorktree: true, Bare: isBare(commonGitDir)}
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
