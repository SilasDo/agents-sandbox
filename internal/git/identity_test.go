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
	root := newRepo(t, "https://example.com/acme/widget.git")
	main := ResolveIdentity(root)

	wt := filepath.Join(root, "nested", "wt")
	writeGitdirFile(t, wt, filepath.Join(root, ".git", "worktrees", "wt"))
	got := ResolveIdentity(wt)

	require.Equal(t, main.RepoSlug, got.RepoSlug)
	require.Equal(t, main.CheckoutSlug, got.CheckoutSlug)
	require.Equal(t, root, got.BindRoot)
	require.Equal(t, root, got.CheckoutRoot)
	require.False(t, got.OutsideWorktree)
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
// keeps its own worktree as the bind root and is never refused.
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
}

func TestRepoSlugOutsideWorktree(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"
	root := newRepo(t, origin)
	outside := filepath.Join(resolvePath(t.TempDir()), "wt")
	writeGitdirFile(t, outside, filepath.Join(root, ".git", "worktrees", "wt"))
	t.Chdir(outside)

	require.Equal(t, projectSlug(sanitizeFolderName("widget"), origin), RepoSlug())
}

func TestCheckoutSlugOutsideWorktreeFailsFast(t *testing.T) {
	root := newRepo(t, "https://example.com/acme/widget.git")
	outside := filepath.Join(resolvePath(t.TempDir()), "wt")
	writeGitdirFile(t, outside, filepath.Join(root, ".git", "worktrees", "wt"))
	t.Chdir(outside)

	slug, err := CheckoutSlug()

	require.Error(t, err)
	require.Empty(t, slug)
	require.Contains(t, err.Error(), outside)
	require.Contains(t, err.Error(), root)
}

func TestRequireCheckoutSlugReturnsCheckoutSlug(t *testing.T) {
	root := newRepo(t, "https://example.com/acme/widget.git")

	slug, err := ResolveIdentity(root).RequireCheckoutSlug()

	require.NoError(t, err)
	require.Equal(t, ResolveIdentity(root).CheckoutSlug, slug)
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
