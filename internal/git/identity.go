package git

import (
	"path/filepath"
)

// Identity is the host-side identity of the project a directory belongs to:
// where to bind /workspace, the enclosing checkout, and the two slugs that key
// the per-project artifacts.
//
// The repo slug derives from the origin remote URL, so every clone of one remote
// shares it; it keys the per-slug user config dir and the sandbox grouping
// label. The checkout slug derives from the resolved git common dir for a
// checkout and its inside-worktrees, so they share it while two clones do not;
// it keys state, the VM name, the home volume, locks and the runner image tag. A
// linked worktree outside its checkout cannot share that VM — its /workspace
// bind can only be one of the two — so it is scoped as its own checkout by
// hashing its worktree root instead.
type Identity struct {
	BindRoot        string // host dir to bind at /workspace
	CheckoutRoot    string // enclosing checkout root; "" when cwd is not in a repo
	WorktreeRoot    string // nearest dir holding a .git entry; "" when cwd is not in a repo
	OutsideWorktree bool   // cwd is a linked worktree outside CheckoutRoot; gets its own checkout slug
	Bare            bool   // the shared git dir is a bare repository
	RepoSlug        string // origin-derived; shared by every clone of a remote
	CheckoutSlug    string // common-dir-derived, or worktree-root-derived when outside
	// LegacyRepoSlugs lists additional origin-keyed identities whose project state
	// and per-slug config predate this one and must still be adopted. It is only
	// non-empty for the bare root itself: a bare repository has no worktree, so
	// the old ProjectSlug fell back to the cwd path there while every worktree
	// inside it kept the origin-derived slug. Adopting both keeps a bare-root
	// user's saved home and config instead of stranding them.
	LegacyRepoSlugs []string
}

// ResolveIdentity derives the workspace layout and both project slugs for cwd in
// a single pass. It never returns an error: a directory outside a repository, or
// one whose .git entry is malformed, resolves to the non-repo identity (both
// slugs derived from the resolved path). A linked worktree outside its checkout
// is reported through OutsideWorktree and scoped as its own checkout (its
// checkout slug hashes the worktree root), so it simply runs as its own project
// and nothing refuses on the flag.
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

	checkoutRoot, bindRoot, outsideWorktree := bindLayout(worktreeRoot, commonGitDir)
	// An outside worktree hashes its own root: hashing the shared common dir
	// would give it its checkout's slug and therefore its checkout's VM, whose
	// /workspace bind can only be one of the two.
	checkoutHashInput := commonGitDir
	if outsideWorktree {
		checkoutHashInput = worktreeRoot
	}
	repoSlug, checkoutSlug := identitySlugs(worktreeRoot, bindRoot, checkoutHashInput)
	bare := isBare(commonGitDir)
	identity := Identity{
		BindRoot:        bindRoot,
		CheckoutRoot:    checkoutRoot,
		WorktreeRoot:    worktreeRoot,
		OutsideWorktree: outsideWorktree,
		Bare:            bare,
		RepoSlug:        repoSlug,
		CheckoutSlug:    checkoutSlug,
		LegacyRepoSlugs: nil,
	}
	// The bare root is not a linked worktree, so bindRoot is the worktree root
	// itself: this is the cwd that predates the split with a path-derived slug.
	if bare && bindRoot == worktreeRoot {
		if legacy := projectSlug(
			sanitizeFolderName(filepath.Base(worktreeRoot)),
			worktreeRoot,
		); legacy != repoSlug &&
			legacy != checkoutSlug { // gomutants:disable EXPRESSION_REMOVE reason="when reached, commonGitDir differs from the worktree root, so this legacy slug never equals the common-dir-derived checkout slug"
			identity.LegacyRepoSlugs = []string{legacy}
		}
	}
	return identity
}

// RepoSlug returns the repo slug of the current working directory: the value
// shared by every clone of the same origin.
//
//deadcode:ignore reason="cwd-derived convenience for tests; production reads the ResolveIdentity fields"
func RepoSlug() string {
	return ResolveIdentity(".").RepoSlug
}

// CheckoutSlug returns the checkout slug of the current working directory.
//
//deadcode:ignore reason="cwd-derived convenience for tests; production reads the ResolveIdentity fields"
func CheckoutSlug() string {
	return ResolveIdentity(".").CheckoutSlug
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
		WorktreeRoot:    "",
		OutsideWorktree: false,
		Bare:            false,
		RepoSlug:        slug,
		CheckoutSlug:    slug,
		LegacyRepoSlugs: nil,
	}
}

// bindLayout decides which host directory to bind at /workspace and where the
// enclosing checkout is. A normal checkout, a --separate-git-dir checkout and a
// submodule bind their own worktree; a linked worktree inside its checkout binds
// the checkout root so sibling worktrees stay visible; and a linked worktree
// outside its checkout binds its own root, since under checkout identity the
// worktree is the "smallest enclosing checkout that contains the CWD". A
// subdirectory of any of these binds the same root, so the agent starts in the
// subdirectory with the repository visible above it.
func bindLayout(worktreeRoot, commonGitDir string) (string, string, bool) {
	if !isLinkedWorktree(worktreeRoot) {
		return worktreeRoot, worktreeRoot, false
	}
	checkoutRoot := checkoutRootOf(commonGitDir)
	if within(checkoutRoot, worktreeRoot) {
		return checkoutRoot, checkoutRoot, false
	}
	return checkoutRoot, worktreeRoot, true
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

// identitySlugs builds the repo slug and the checkout slug. The repo slug
// derives entirely from the worktree root: the origin's last path segment and
// the origin URL when there is one, otherwise the worktree root's own name and
// path (today's ProjectSlug fallback, unchanged). The checkout slug's folder
// comes from checkoutFolderSource — the bind root, i.e. the checkout root for a
// plain or inside-worktree checkout — and its hash from checkoutHashInput, the
// resolved common git dir those share (or the worktree root for an outside
// worktree, which is its own checkout). Both folders come from the origin when
// there is one, so two clones read "foo-<hashA>"/"foo-<hashB>".
func identitySlugs(worktreeRoot, checkoutFolderSource, checkoutHashInput string) (string, string) {
	url := originURL(worktreeRoot)
	repoFolder := sanitizeFolderName(filepath.Base(worktreeRoot))
	checkoutFolder := sanitizeFolderName(filepath.Base(checkoutFolderSource))
	repoHash := worktreeRoot
	if url != "" {
		repoFolder = sanitizeFolderName(lastPathSegment(url))
		checkoutFolder = repoFolder
		repoHash = url
	}
	return projectSlug(repoFolder, repoHash), projectSlug(checkoutFolder, checkoutHashInput)
}
