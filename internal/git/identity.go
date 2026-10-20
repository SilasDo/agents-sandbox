package git

import (
	"fmt"
	"path/filepath"
)

// Identity is the host-side identity of the project a directory belongs to:
// where to bind /workspace, the enclosing checkout, and the two slugs that key
// the per-project artifacts.
//
// The repo slug derives from the origin remote URL, so every clone of one remote
// shares it; it keys the per-slug user config dir and the sandbox grouping
// label. The checkout slug derives from the resolved git common dir, so a
// checkout and all of its worktrees share it while two clones do not; it keys
// state, the VM name, the home volume, locks and the runner image tag.
type Identity struct {
	BindRoot        string // host dir to bind at /workspace
	CheckoutRoot    string // enclosing checkout root; "" when cwd is not in a repo
	WorktreeRoot    string // nearest dir holding a .git entry; "" when cwd is not in a repo
	OutsideWorktree bool   // cwd is a linked worktree outside CheckoutRoot
	Bare            bool   // the shared git dir is a bare repository
	RepoSlug        string // origin-derived; shared by every clone of a remote
	CheckoutSlug    string // common-dir-derived; shared by a checkout and its worktrees
}

// ResolveIdentity derives the workspace layout and both project slugs for cwd in
// a single pass. It never returns an error: a directory outside a repository, or
// one whose .git entry is malformed, resolves to the non-repo identity (both
// slugs derived from the resolved path). A linked worktree outside its checkout
// is reported through OutsideWorktree, so commands that need no checkout
// identity (config, prune, df, doctor, tree, version) keep working there; the
// checkout-slug accessor refuses instead (see RequireCheckoutSlug).
//
// Symlinks are resolved internally so every entry point (run, stop, kill, volume
// ops, build) hashes the same physical path — otherwise /tmp and /private/tmp on
// macOS would produce different checkout slugs. Derivation is plain-file
// inspection; no git binary is invoked.
func ResolveIdentity(cwd string) Identity {
	resolved := resolvePath(cwd)
	worktreeRoot := findWorktreeRoot(resolved)
	if worktreeRoot == "" {
		return nonRepoIdentity(resolved)
	}
	commonGitDir := resolveGitDir(worktreeRoot)
	if commonGitDir == "" {
		return nonRepoIdentity(resolved)
	}
	// Resolve the common dir too: git writes a linked worktree's gitdir path as
	// given, so an unresolved temp path on macOS would not match the path the
	// main checkout hashes, and the worktree would not share its checkout slug.
	commonGitDir = resolvePath(commonGitDir)

	checkoutRoot, bindRoot, outsideWorktree := bindLayout(resolved, worktreeRoot, commonGitDir)
	repoSlug, checkoutSlug := identitySlugs(worktreeRoot, commonGitDir)
	return Identity{
		BindRoot:        bindRoot,
		CheckoutRoot:    checkoutRoot,
		WorktreeRoot:    worktreeRoot,
		OutsideWorktree: outsideWorktree,
		Bare:            isBare(commonGitDir),
		RepoSlug:        repoSlug,
		CheckoutSlug:    checkoutSlug,
	}
}

// RequireCheckoutSlug returns the checkout slug, or a fail-fast error when cwd
// is a linked worktree outside its checkout. Such a worktree shares its main
// clone's checkout slug but can only bind one of their directories at
// /workspace, so proceeding would reproduce the two-checkouts-one-VM collision
// of #144. Refusing is deterministic.
func (i Identity) RequireCheckoutSlug() (string, error) {
	if i.OutsideWorktree {
		return "", fmt.Errorf(
			"worktree %q is outside its checkout %q; keep worktrees inside the checkout so they share one project VM",
			i.BindRoot,
			i.CheckoutRoot,
		)
	}
	return i.CheckoutSlug, nil
}

// RepoSlug returns the repo slug of the current working directory: the value
// shared by every clone of the same origin.
func RepoSlug() string {
	return ResolveIdentity(".").RepoSlug
}

// CheckoutSlug returns the checkout slug of the current working directory, or a
// fail-fast error when it is a linked worktree outside its checkout; see
// RequireCheckoutSlug.
func CheckoutSlug() (string, error) {
	return ResolveIdentity(".").RequireCheckoutSlug()
}

// resolvePath returns path as an absolute, symlink-resolved path. Both steps
// matter: an absolute result keeps the cwd-derived callers (RepoSlug and
// CheckoutSlug pass ".") from hashing a relative path, and resolving symlinks
// makes /tmp and /private/tmp hash alike.
func resolvePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return resolved
}

// nonRepoIdentity is the identity of a directory that is not in a repository:
// both slugs derive from the resolved path, and the directory itself is bound.
func nonRepoIdentity(resolved string) Identity {
	slug := projectSlug(sanitizeFolderName(filepath.Base(resolved)), resolved)
	return Identity{
		BindRoot:        resolved,
		CheckoutRoot:    "",
		OutsideWorktree: false,
		RepoSlug:        slug,
		CheckoutSlug:    slug,
	}
}

// bindLayout decides which host directory to bind at /workspace and where the
// enclosing checkout is. A normal checkout, a --separate-git-dir checkout and a
// submodule keep their own worktree as the bind root; only a linked worktree
// whose shared git dir lies outside its enclosing checkout is "outside" — it
// shares its main clone's checkout slug but cannot share its /workspace bind, so
// callers refuse it (see RequireCheckoutSlug).
func bindLayout(resolved, worktreeRoot, commonGitDir string) (string, string, bool) {
	if !isLinkedWorktree(worktreeRoot) {
		return worktreeRoot, worktreeRoot, false
	}
	checkoutRoot := checkoutRootOf(commonGitDir)
	if within(checkoutRoot, worktreeRoot) {
		return checkoutRoot, checkoutRoot, false
	}
	return checkoutRoot, resolved, true
}

// checkoutRootOf returns the directory that contains the git dir and any nested
// worktrees: the parent of a ".git" dir, or the dir itself under another name
// (a bare root, or a --separate-git-dir target).
func checkoutRootOf(commonGitDir string) string {
	if filepath.Base(commonGitDir) == ".git" {
		return filepath.Dir(commonGitDir)
	}
	return commonGitDir
}

// identitySlugs builds both slugs from one folder name. The folder comes from
// the origin's last path segment so two clones read "foo-<hashA>"/"foo-<hashB>";
// without an origin it falls back to the worktree's own name. The repo slug
// hashes the origin URL (or the worktree root without one); the checkout slug
// hashes the resolved common git dir, which is exactly what a checkout and its
// worktrees share and two clones do not.
func identitySlugs(worktreeRoot, commonGitDir string) (string, string) {
	url := originURL(worktreeRoot)
	folder := sanitizeFolderName(filepath.Base(worktreeRoot))
	repoHash := worktreeRoot
	if url != "" {
		folder = sanitizeFolderName(lastPathSegment(url))
		repoHash = url
	}
	return projectSlug(folder, repoHash), projectSlug(folder, commonGitDir)
}
