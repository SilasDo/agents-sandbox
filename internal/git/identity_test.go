package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// newRepo builds a minimal repo layout in a fresh temp dir: a .git directory
// holding a config with the given origin remote (omitted when origin is empty).
// It returns the symlink-resolved path, so assertions compare against exactly
// what ResolveIdentity derives — on macOS the temp dir sits behind the
// /var -> /private/var symlink.
func newRepo(t *testing.T, origin string) string {
	t.Helper()
	root := resolvePath(t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	if origin != "" {
		writeOriginConfig(t, root, origin)
	}
	return root
}

func writeOriginConfig(t *testing.T, root, origin string) {
	t.Helper()
	config := "[remote \"origin\"]\n\turl = " + origin + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "config"), []byte(config), 0o644))
}

// writeGitdirFile writes dir/.git as a file whose gitdir points at gitdir: the
// shape git uses for a linked worktree (a worktrees/ target) and for a
// --separate-git-dir checkout or submodule (any other target).
func writeGitdirFile(t *testing.T, dir, gitdir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))
}

func TestResolveIdentityRepoWithOrigin(t *testing.T) {
	root := newRepo(t, "https://example.com/acme/widget.git")

	id := ResolveIdentity(root)

	// The repo slug is byte-identical to the pre-change value: the per-slug user
	// config dir must not move for existing users.
	wantRepo := projectSlug(sanitizeFolderName("widget"), "https://example.com/acme/widget.git")
	require.Equal(t, wantRepo, id.RepoSlug)
	require.Equal(t, projectSlug(sanitizeFolderName("widget"), resolveGitDir(root)), id.CheckoutSlug)
	require.False(t, id.OutsideWorktree)
	require.Equal(t, root, id.BindRoot)
	require.Equal(t, root, id.CheckoutRoot)
}

func TestResolveIdentityClonesShareRepoSlug(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"

	first := ResolveIdentity(newRepo(t, origin))
	second := ResolveIdentity(newRepo(t, origin))

	require.Equal(t, first.RepoSlug, second.RepoSlug)
	require.NotEqual(t, first.CheckoutSlug, second.CheckoutSlug)
}

func TestResolveIdentityDifferentOriginsDiffer(t *testing.T) {
	first := ResolveIdentity(newRepo(t, "https://example.com/acme/widget.git"))
	second := ResolveIdentity(newRepo(t, "https://example.com/other/widget.git"))

	require.NotEqual(t, first.RepoSlug, second.RepoSlug)
}

func TestResolveIdentityWorktreeSharesCheckoutSlug(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"
	repo := resolvePath(testutil.InitRepo(t))
	testutil.RunGit(t, repo, "remote", "add", "origin", origin)
	main := ResolveIdentity(repo)

	nested := filepath.Join(repo, "nested", "wt")
	require.NoError(t, os.MkdirAll(filepath.Dir(nested), 0o755))
	testutil.RunGit(t, repo, "worktree", "add", nested)
	got := ResolveIdentity(nested)

	require.Equal(t, main.RepoSlug, got.RepoSlug)
	require.Equal(t, main.CheckoutSlug, got.CheckoutSlug)
	require.Equal(t, repo, got.BindRoot)
	require.Equal(t, repo, got.CheckoutRoot)
	require.False(t, got.OutsideWorktree)
}

// TestResolveIdentityNoOriginWorktreeSharesCheckoutSlug pins the checkout-slug
// derivation when there is no origin: the folder comes from the checkout
// basename, not the worktree's, so an inside-worktree shares its checkout slug.
// The repo slug keeps its documented no-origin fallback (folder and hash both
// from the worktree root), so the inside-worktree still has its own repo slug.
func TestResolveIdentityNoOriginWorktreeSharesCheckoutSlug(t *testing.T) {
	t.Run("plain checkout", func(t *testing.T) {
		repo := resolvePath(testutil.InitRepo(t))
		main := ResolveIdentity(repo)

		nested := filepath.Join(repo, "nested", "wt")
		require.NoError(t, os.MkdirAll(filepath.Dir(nested), 0o755))
		testutil.RunGit(t, repo, "worktree", "add", nested)
		got := ResolveIdentity(nested)

		require.Equal(t, main.CheckoutSlug, got.CheckoutSlug)
		require.False(t, got.OutsideWorktree)
		require.Equal(t, repo, got.BindRoot)
		require.Equal(t, repo, got.CheckoutRoot)
		// The repo slug's unchanged fallback derives from the worktree root, so
		// the inside-worktree does not share its checkout's repo slug.
		require.Equal(t, projectSlug(sanitizeFolderName("wt"), nested), got.RepoSlug)
		require.NotEqual(t, main.RepoSlug, got.RepoSlug)
	})

	t.Run("bare root", func(t *testing.T) {
		origin := testutil.InitRepo(t)
		root := resolvePath(t.TempDir())
		gitDir := filepath.Join(root, ".git")
		testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
		testutil.RunGit(t, gitDir, "remote", "remove", "origin")
		testutil.RunGit(t, gitDir, "worktree", "add", "--detach", filepath.Join(root, "main"))

		main := ResolveIdentity(root)
		got := ResolveIdentity(filepath.Join(root, "main"))

		require.Equal(t, main.CheckoutSlug, got.CheckoutSlug)
		require.False(t, got.OutsideWorktree)
		require.Equal(t, root, got.BindRoot)
		require.Equal(t, root, got.CheckoutRoot)
	})
}

func TestResolveIdentitySubdirectoryMatchesCheckoutRoot(t *testing.T) {
	root := newRepo(t, "https://example.com/acme/widget.git")
	main := ResolveIdentity(root)

	sub := filepath.Join(root, "internal", "foo")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	got := ResolveIdentity(sub)

	require.Equal(t, main.RepoSlug, got.RepoSlug)
	require.Equal(t, main.CheckoutSlug, got.CheckoutSlug)
	require.Equal(t, root, got.BindRoot)
	require.Equal(t, root, got.CheckoutRoot)
	require.False(t, got.OutsideWorktree)
}

func TestResolveIdentityNonRepoDirectory(t *testing.T) {
	dir := resolvePath(t.TempDir())

	id := ResolveIdentity(dir)

	want := projectSlug(sanitizeFolderName(filepath.Base(dir)), dir)
	require.Equal(t, want, id.RepoSlug)
	require.Equal(t, want, id.CheckoutSlug)
	require.False(t, id.OutsideWorktree)
	require.Equal(t, dir, id.BindRoot)
	require.Empty(t, id.CheckoutRoot)
}

func TestResolveIdentityResolvesSymlinks(t *testing.T) {
	physical := newRepo(t, "https://example.com/acme/widget.git")
	link := filepath.Join(resolvePath(t.TempDir()), "link")
	require.NoError(t, os.Symlink(physical, link))

	fromPhysical := ResolveIdentity(physical)
	fromLink := ResolveIdentity(link)

	require.Equal(t, fromPhysical.RepoSlug, fromLink.RepoSlug)
	require.Equal(t, fromPhysical.CheckoutSlug, fromLink.CheckoutSlug)
	require.Equal(t, physical, fromLink.BindRoot)
	require.Equal(t, physical, fromLink.CheckoutRoot)
}

// TestResolveIdentitySeparateGitDirIsNotOutside pins the narrowing from #145:
// a .git file whose gitdir contains no worktrees/ segment is a
// --separate-git-dir checkout or a submodule, not a linked worktree, so it
// keeps its own worktree as the bind root and hashes the common dir like any
// checkout.
func TestResolveIdentitySeparateGitDirIsNotOutside(t *testing.T) {
	for name, gitdir := range map[string]string{
		"separate git dir": "/elsewhere/repo.git",
		"submodule":        "../.git/modules/sub",
	} {
		t.Run(name, func(t *testing.T) {
			root := resolvePath(t.TempDir())
			writeGitdirFile(t, root, gitdir)

			id := ResolveIdentity(root)

			require.False(t, id.OutsideWorktree)
			require.Equal(t, root, id.CheckoutRoot)
			require.Equal(t, root, id.BindRoot)
		})
	}
}

// newSubmoduleUnderWorktreesAncestor creates a real submodule whose superproject
// lives under a path component literally named "worktrees", and returns the
// submodule's working-tree root. git records the submodule gitdir relative
// (../.git/modules/sub), so this is the shape a joined-then-stripped gitdir
// misclassifies: the "worktrees" ancestor leaks into the joined path.
func newSubmoduleUnderWorktreesAncestor(t *testing.T) string {
	t.Helper()
	origin := testutil.InitRepo(t)
	super := filepath.Join(resolvePath(t.TempDir()), "worktrees", "super")
	require.NoError(t, os.MkdirAll(super, 0o755))
	testutil.RunGit(t, super, "init")
	testutil.ConfigureRepo(t, super)
	testutil.WriteFile(t, super, "s", "s")
	testutil.RunGit(t, super, "add", ".")
	testutil.RunGit(t, super, "commit", "-m", "init")
	testutil.RunGit(t, super, "-c", "protocol.file.allow=always", "submodule", "add", origin, "sub")
	return filepath.Join(super, "sub")
}

// newRepoUnderWorktreesAncestor creates a real repo whose path contains a
// component literally named "worktrees" (so its .git path does too) and returns
// its working-tree root.
func newRepoUnderWorktreesAncestor(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(resolvePath(t.TempDir()), "worktrees", "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	testutil.RunGit(t, repo, "init")
	testutil.ConfigureRepo(t, repo)
	testutil.WriteFile(t, repo, "f", "f")
	testutil.RunGit(t, repo, "add", ".")
	testutil.RunGit(t, repo, "commit", "-m", "init")
	return repo
}

// newSeparateGitDirUnderWorktreesAncestor creates a real --separate-git-dir
// checkout whose git dir is "<base>/worktrees/<name>.git": the shape of a
// worktree registration but with no commondir marker. It returns the checkout
// root and the recorded git dir.
func newSeparateGitDirUnderWorktreesAncestor(t *testing.T) (string, string) {
	t.Helper()
	base := resolvePath(t.TempDir())
	root := filepath.Join(base, "checkout")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "worktrees"), 0o755))
	gitdir := filepath.Join(base, "worktrees", "proj.git")
	testutil.RunGit(t, root, "init", "--separate-git-dir="+gitdir)
	return root, gitdir
}

// TestResolveIdentityGitDirFileUnderWorktreesAncestorIsNotOutside pins the
// pre-refactor order: the recorded gitdir is classified and stripped while still
// relative, before it is joined to the worktree root. A "worktrees" component in
// the ancestor path must not turn a submodule or --separate-git-dir checkout into
// a linked worktree, which would bind an unrelated ancestor at /workspace.
func TestResolveIdentityGitDirFileUnderWorktreesAncestorIsNotOutside(t *testing.T) {
	t.Run("submodule", func(t *testing.T) {
		root := newSubmoduleUnderWorktreesAncestor(t)

		id := ResolveIdentity(root)

		require.False(t, id.OutsideWorktree)
		require.Equal(t, root, id.CheckoutRoot)
		require.Equal(t, root, id.BindRoot)
	})

	t.Run("relative separate git dir", func(t *testing.T) {
		// git records --separate-git-dir as an absolute path, so the relative
		// form is hand-written to exercise the same strip-before-join order.
		root := filepath.Join(resolvePath(t.TempDir()), "worktrees", "proj", "sub")
		writeGitdirFile(t, root, "../shared.git")

		id := ResolveIdentity(root)

		require.False(t, id.OutsideWorktree)
		require.Equal(t, root, id.CheckoutRoot)
		require.Equal(t, root, id.BindRoot)
	})

	t.Run("absolute separate git dir", func(t *testing.T) {
		// An absolute gitdir carries its ancestors in the recorded text, so a
		// "worktrees" component there must not look like a worktree registration
		// (it would strip to the ancestor and bind the wrong root).
		root := filepath.Join(resolvePath(t.TempDir()), "proj")
		shared := filepath.Join(resolvePath(t.TempDir()), "worktrees", "proj", "shared.git")
		writeGitdirFile(t, root, shared)

		id := ResolveIdentity(root)

		require.False(t, id.OutsideWorktree)
		require.Equal(t, root, id.CheckoutRoot)
		require.Equal(t, root, id.BindRoot)
	})
}

// TestResolveIdentityWorktreeUnderWorktreesAncestor keeps the linked-worktree
// strip correct when the shared git dir itself sits under a "worktrees" path
// component: the registration is the last /worktrees/ segment, not the ancestor.
func TestResolveIdentityWorktreeUnderWorktreesAncestor(t *testing.T) {
	repo := newRepoUnderWorktreesAncestor(t)
	wt := filepath.Join(repo, "wt")
	testutil.RunGit(t, repo, "worktree", "add", wt)

	id := ResolveIdentity(wt)

	require.False(t, id.OutsideWorktree)
	require.Equal(t, repo, id.CheckoutRoot)
	require.Equal(t, repo, id.BindRoot)
}

// TestResolveIdentitySeparateGitDirShapedLikeRegistrationIsNotOutside pins the
// discriminator: a --separate-git-dir checkout whose git dir is
// "<ancestor>/worktrees/<name>.git" matches a registration's shape but has no
// commondir marker, so it is a plain checkout and must not be stripped or bound
// to the ancestor.
func TestResolveIdentitySeparateGitDirShapedLikeRegistrationIsNotOutside(t *testing.T) {
	root, gitdir := newSeparateGitDirUnderWorktreesAncestor(t)

	require.Equal(t, gitdir, resolveGitDir(root))
	require.False(t, isLinkedWorktree(root))

	id := ResolveIdentity(root)

	require.False(t, id.OutsideWorktree)
	require.Equal(t, root, id.CheckoutRoot)
	require.Equal(t, root, id.BindRoot)
}

func TestRepoSlugOutsideWorktree(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"
	repo := resolvePath(testutil.InitRepo(t))
	testutil.RunGit(t, repo, "remote", "add", "origin", origin)
	outside := filepath.Join(resolvePath(t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", outside)
	t.Chdir(outside)

	require.Equal(t, projectSlug(sanitizeFolderName("widget"), origin), RepoSlug())
}

// TestCheckoutSlugOutsideWorktreeOwnsItsIdentity pins the outside-worktree rule:
// such a worktree cannot share its checkout's VM (its /workspace bind can only be
// one of the two), so it is scoped as its own checkout by hashing its worktree
// root. It keeps the origin-derived repo slug, so it still shares the per-slug
// user config dir with its checkout.
func TestCheckoutSlugOutsideWorktreeOwnsItsIdentity(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"
	repo := resolvePath(testutil.InitRepo(t))
	testutil.RunGit(t, repo, "remote", "add", "origin", origin)
	checkout := ResolveIdentity(repo)

	base := resolvePath(t.TempDir())
	worktree := filepath.Join(base, "wt")
	sibling := filepath.Join(base, "sibling")
	testutil.RunGit(t, repo, "worktree", "add", worktree)
	testutil.RunGit(t, repo, "worktree", "add", sibling)
	t.Chdir(worktree)

	identity := ResolveIdentity(worktree)
	// Built from the hash primitive directly, not from projectSlug: a bug in that
	// helper must not move the expectation along with the implementation.
	wantSlug := sanitizeFolderName("widget") + "-" + HashID(worktree)

	require.True(t, identity.OutsideWorktree)
	require.Equal(t, worktree, identity.BindRoot)
	require.Equal(t, wantSlug, identity.CheckoutSlug)
	require.Equal(t, sanitizeFolderName("widget")+"-"+HashID(origin), RepoSlug())
	require.Equal(t, wantSlug, CheckoutSlug())
	// Collision guard: an outside worktree must share neither its checkout's slug
	// (one VM, wrong /workspace) nor a sibling worktree's.
	require.NotEqual(t, checkout.CheckoutSlug, CheckoutSlug())
	require.NotEqual(t, ResolveIdentity(sibling).CheckoutSlug, CheckoutSlug())
}

// TestCheckoutSlugOutsideWorktreeSubdirectoryUsesWorktreeRoot pins that the bind
// root does not follow the cwd: a run from inside the worktree is scoped to the
// worktree root (keeping the repository visible above the agent) and the slug
// hashes the worktree root, not the subdirectory.
func TestCheckoutSlugOutsideWorktreeSubdirectoryUsesWorktreeRoot(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"
	repo := resolvePath(testutil.InitRepo(t))
	testutil.RunGit(t, repo, "remote", "add", "origin", origin)
	checkout := ResolveIdentity(repo)

	worktree := filepath.Join(resolvePath(t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", worktree)
	sub := filepath.Join(worktree, "sub", "dir")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	t.Chdir(sub)

	identity := ResolveIdentity(sub)
	wantSlug := sanitizeFolderName("widget") + "-" + HashID(worktree)

	require.True(t, identity.OutsideWorktree)
	require.Equal(t, worktree, identity.BindRoot)
	require.Equal(t, wantSlug, CheckoutSlug())
	require.NotEqual(t, checkout.CheckoutSlug, CheckoutSlug())
}

func TestRepoSlugWithoutOriginUnchanged(t *testing.T) {
	root := newRepo(t, "")
	folder := sanitizeFolderName(filepath.Base(root))

	id := ResolveIdentity(root)

	require.Equal(t, projectSlug(folder, root), id.RepoSlug)
	require.Equal(t, projectSlug(folder, resolveGitDir(root)), id.CheckoutSlug)
}

func TestRepoSlugUsesOriginRepoName(t *testing.T) {
	root := newRepo(t, "git@github.com:inoio/agents-sandbox.git")
	t.Chdir(root)

	got := RepoSlug()

	require.True(
		t,
		strings.HasPrefix(got, "agents-sandbox-"),
		"RepoSlug() = %q, want prefix %q",
		got,
		"agents-sandbox-",
	)
	require.Len(t, got, len("agents-sandbox-")+hashIDLen)
}

func TestRepoSlugFallsBackWithoutOrigin(t *testing.T) {
	root := newRepo(t, "")
	t.Chdir(root)

	got := RepoSlug()

	folder := sanitizeFolderName(filepath.Base(root))
	require.True(t, strings.HasPrefix(got, folder+"-"), "RepoSlug() = %q, want prefix %q", got, folder+"-")
}

func TestRepoSlugFormat(t *testing.T) {
	root := newRepo(t, "")
	t.Chdir(root)

	got := RepoSlug()

	// Expected format: <sanitized-folder>-<14 base36 chars>.
	folder := sanitizeFolderName(filepath.Base(root))
	require.True(t, strings.HasPrefix(got, folder+"-"), "RepoSlug() = %q, want prefix %q", got, folder+"-")
	hashPart := strings.TrimPrefix(got, folder+"-")
	require.Len(t, hashPart, hashIDLen)
	for _, r := range hashPart {
		isBase36 := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z')
		require.True(t, isBase36, "hash %q contains non-base36 rune %q", hashPart, r)
	}
}

func TestRepoSlugDeterministic(t *testing.T) {
	root := newRepo(t, "")
	t.Chdir(root)

	first := RepoSlug()
	second := RepoSlug()
	require.Equal(t, first, second)
}

// TestResolvePathFallsBackWhenCwdUnreadable pins the last-resort branch: with the
// process cwd gone, filepath.Abs cannot make a relative path absolute, so
// resolvePath returns the input instead of erroring (it is total by design).
func TestResolvePathFallsBackWhenCwdUnreadable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.RemoveAll(dir))

	require.Equal(t, "relative", resolvePath("relative"))
}
