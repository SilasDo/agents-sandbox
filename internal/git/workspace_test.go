package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestResolveIdentityNonRepoLayout(t *testing.T) {
	dir := resolvePath(t.TempDir())

	got := ResolveIdentity(dir)

	if got.BindRoot != dir {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, dir)
	}
	if got.CheckoutRoot != "" {
		t.Errorf("CheckoutRoot = %q, want empty", got.CheckoutRoot)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

func TestResolveIdentityRepoRoot(t *testing.T) {
	repo := resolvePath(testutil.InitRepo(t))

	got := ResolveIdentity(repo)

	if got.BindRoot != repo {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, repo)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

func TestResolveIdentityNestedWorktree(t *testing.T) {
	repo := resolvePath(testutil.InitRepo(t))
	wt := filepath.Join(repo, "wt")
	testutil.RunGit(t, repo, "worktree", "add", wt)

	got := ResolveIdentity(wt)

	if got.BindRoot != repo {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, repo)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

func TestResolveIdentityOutsideWorktree(t *testing.T) {
	repo := resolvePath(testutil.InitRepo(t))
	wt := filepath.Join(resolvePath(t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", wt)

	got := ResolveIdentity(wt)

	if got.BindRoot != wt {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, wt)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if !got.OutsideWorktree {
		t.Errorf("OutsideWorktree = false, want true")
	}
}

func TestResolveIdentityMalformedGitFile(t *testing.T) {
	dir := resolvePath(t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("this is not a gitdir pointer"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	got := ResolveIdentity(dir)

	if got.BindRoot != dir {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, dir)
	}
	if got.CheckoutRoot != "" {
		t.Errorf("CheckoutRoot = %q, want empty", got.CheckoutRoot)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

// TestResolveIdentityOutsideWorktreeOfBareGitDir builds a bare clone at
// tmp/bare.git and adds a worktree at tmp/wt, a sibling of the bare dir. The
// worktree is a linked worktree placed outside its checkout, so no containment
// is possible and it is refused (RequireCheckoutSlug) rather than silently
// sharing the checkout slug with a different /workspace bind.
func TestResolveIdentityOutsideWorktreeOfBareGitDir(t *testing.T) {
	origin := testutil.InitRepo(t)
	tmp := resolvePath(t.TempDir())
	bare := filepath.Join(tmp, "bare.git")
	testutil.RunGit(t, tmp, "clone", "--bare", origin, "bare.git")
	wt := filepath.Join(tmp, "wt")
	testutil.RunGit(t, bare, "worktree", "add", wt)

	got := ResolveIdentity(wt)

	if !got.OutsideWorktree {
		t.Errorf("OutsideWorktree = false, want true")
	}
	if got.BindRoot != wt {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, wt)
	}
	if got.CheckoutRoot != bare {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, bare)
	}
}

func TestResolveIdentityBareWorktree(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := resolvePath(t.TempDir())
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	// The bare root and every worktree inside it share one checkout slug.
	rootIdentity := ResolveIdentity(root)

	t.Run("bare root", func(t *testing.T) {
		got := ResolveIdentity(root)
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
	})

	t.Run("worktree root", func(t *testing.T) {
		got := ResolveIdentity(filepath.Join(root, "main"))
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
		if got.CheckoutSlug != rootIdentity.CheckoutSlug {
			t.Errorf("CheckoutSlug = %q, want the bare root's %q", got.CheckoutSlug, rootIdentity.CheckoutSlug)
		}
	})

	t.Run("worktree subdirectory", func(t *testing.T) {
		sub := filepath.Join(root, "main", "internal")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		got := ResolveIdentity(sub)
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
	})
}

func TestResolveIdentityRelativeGitDir(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := resolvePath(t.TempDir())
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "config", "worktree.useRelativePaths", "true")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	got := ResolveIdentity(filepath.Join(root, "main"))

	if got.BindRoot != root {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}
