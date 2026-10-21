package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestResolveWorkspaceLayoutNonRepo(t *testing.T) {
	dir := t.TempDir()

	got := ResolveWorkspaceLayout(dir)

	if got.BindRoot != dir {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, dir)
	}
	if got.CheckoutRoot != "" {
		t.Errorf("CheckoutRoot = %q, want empty", got.CheckoutRoot)
	}
	if got.WorktreeRoot != "" {
		t.Errorf("WorktreeRoot = %q, want empty", got.WorktreeRoot)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
	if got.Bare {
		t.Errorf("Bare = true, want false")
	}
}

func TestResolveWorkspaceLayoutRepoRoot(t *testing.T) {
	repo := testutil.InitRepo(t)

	got := ResolveWorkspaceLayout(repo)

	if got.BindRoot != repo {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, repo)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if got.WorktreeRoot != repo {
		t.Errorf("WorktreeRoot = %q, want %q", got.WorktreeRoot, repo)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
	if got.Bare {
		t.Errorf("Bare = true, want false")
	}
}

func TestResolveWorkspaceLayoutRepoSubdirectory(t *testing.T) {
	repo := testutil.InitRepo(t)
	sub := filepath.Join(repo, "internal", "foo")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sub, err)
	}

	got := ResolveWorkspaceLayout(sub)

	if got.BindRoot != repo {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, repo)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if got.WorktreeRoot != repo {
		t.Errorf("WorktreeRoot = %q, want %q", got.WorktreeRoot, repo)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
	if got.Bare {
		t.Errorf("Bare = true, want false")
	}
}

func TestResolveWorkspaceLayoutNestedWorktree(t *testing.T) {
	repo := testutil.InitRepo(t)
	wt := filepath.Join(repo, "wt")
	testutil.RunGit(t, repo, "worktree", "add", wt)

	got := ResolveWorkspaceLayout(wt)

	if got.BindRoot != repo {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, repo)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if got.WorktreeRoot != wt {
		t.Errorf("WorktreeRoot = %q, want %q", got.WorktreeRoot, wt)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
	if got.Bare {
		t.Errorf("Bare = true, want false")
	}
}

func TestResolveWorkspaceLayoutOutsideWorktree(t *testing.T) {
	repo := testutil.InitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	testutil.RunGit(t, repo, "worktree", "add", wt)

	got := ResolveWorkspaceLayout(wt)

	if got.BindRoot != wt {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, wt)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if got.WorktreeRoot != wt {
		t.Errorf("WorktreeRoot = %q, want %q", got.WorktreeRoot, wt)
	}
	if !got.OutsideWorktree {
		t.Errorf("OutsideWorktree = false, want true")
	}
	if got.Bare {
		t.Errorf("Bare = true, want false")
	}
}

func TestResolveWorkspaceLayoutMalformedGitFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("this is not a gitdir pointer"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	got := ResolveWorkspaceLayout(dir)

	if got.BindRoot != dir {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, dir)
	}
	if got.CheckoutRoot != "" {
		t.Errorf("CheckoutRoot = %q, want empty", got.CheckoutRoot)
	}
	if got.WorktreeRoot != dir {
		t.Errorf("WorktreeRoot = %q, want %q", got.WorktreeRoot, dir)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
	if got.Bare {
		t.Errorf("Bare = true, want false")
	}
}

func TestResolveWorkspaceLayoutBareGitDirNotNamedDotGit(t *testing.T) {
	origin := testutil.InitRepo(t)
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "bare.git")
	testutil.RunGit(t, tmp, "clone", "--bare", origin, "bare.git")
	wt := filepath.Join(tmp, "wt")
	testutil.RunGit(t, bare, "worktree", "add", wt)

	// The shared git dir is not named ".git" and does not sit under the
	// worktree's parent, so the checkout root cannot contain the worktree: this
	// is the submodule / --separate-git-dir classification, treated as outside.
	got := ResolveWorkspaceLayout(wt)

	if !got.OutsideWorktree {
		t.Errorf("OutsideWorktree = false, want true")
	}
	if got.BindRoot != wt {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, wt)
	}
	if got.CheckoutRoot != bare {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, bare)
	}
	if got.WorktreeRoot != wt {
		t.Errorf("WorktreeRoot = %q, want %q", got.WorktreeRoot, wt)
	}
	if !got.Bare {
		t.Errorf("Bare = false, want true")
	}
}

func TestResolveWorkspaceLayoutBareWorktree(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := t.TempDir()
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	t.Run("worktree root", func(t *testing.T) {
		got := ResolveWorkspaceLayout(filepath.Join(root, "main"))
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.WorktreeRoot != filepath.Join(root, "main") {
			t.Errorf("WorktreeRoot = %q, want %q", got.WorktreeRoot, filepath.Join(root, "main"))
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
		if !got.Bare {
			t.Errorf("Bare = false, want true")
		}
	})

	t.Run("worktree subdirectory", func(t *testing.T) {
		sub := filepath.Join(root, "main", "internal")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		got := ResolveWorkspaceLayout(sub)
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.WorktreeRoot != filepath.Join(root, "main") {
			t.Errorf("WorktreeRoot = %q, want the worktree root %q", got.WorktreeRoot, filepath.Join(root, "main"))
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
		if !got.Bare {
			t.Errorf("Bare = false, want true")
		}
	})

	t.Run("bare root", func(t *testing.T) {
		got := ResolveWorkspaceLayout(root)
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.WorktreeRoot != root {
			t.Errorf("WorktreeRoot = %q, want %q", got.WorktreeRoot, root)
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
		if !got.Bare {
			t.Errorf("Bare = false, want true")
		}
	})
}

// TestResolveWorkspaceLayoutBareUnknownWhenConfigMissing covers a shared git dir
// whose config is absent or unparseable: core.bare cannot be read, so the layout
// must report not-bare rather than failing.
func TestResolveWorkspaceLayoutBareUnknownWhenConfigMissing(t *testing.T) {
	newWorktree := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".git", "worktrees", "wt"), 0o755); err != nil {
			t.Fatalf("mkdir git dir: %v", err)
		}
		worktree := filepath.Join(root, "wt")
		if err := os.MkdirAll(worktree, 0o755); err != nil {
			t.Fatalf("mkdir worktree: %v", err)
		}
		gitFile := filepath.Join(worktree, ".git")
		if err := os.WriteFile(gitFile, []byte("gitdir: ../.git/worktrees/wt\n"), 0o644); err != nil {
			t.Fatalf("write .git: %v", err)
		}
		return worktree
	}

	t.Run("missing config", func(t *testing.T) {
		worktree := newWorktree(t)
		got := ResolveWorkspaceLayout(worktree)

		if got.BindRoot != filepath.Dir(worktree) {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, filepath.Dir(worktree))
		}
		if got.CheckoutRoot != filepath.Dir(worktree) {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, filepath.Dir(worktree))
		}
		if got.WorktreeRoot != worktree {
			t.Errorf("WorktreeRoot = %q, want %q", got.WorktreeRoot, worktree)
		}
		if got.Bare {
			t.Errorf("Bare = true, want false")
		}
	})

	t.Run("unparseable config", func(t *testing.T) {
		worktree := newWorktree(t)
		if err := os.WriteFile(
			filepath.Join(filepath.Dir(worktree), ".git", "config"),
			[]byte("not a config"),
			0o644,
		); err != nil {
			t.Fatalf("write config: %v", err)
		}

		got := ResolveWorkspaceLayout(worktree)

		if got.BindRoot != filepath.Dir(worktree) {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, filepath.Dir(worktree))
		}
		if got.CheckoutRoot != filepath.Dir(worktree) {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, filepath.Dir(worktree))
		}
		if got.WorktreeRoot != worktree {
			t.Errorf("WorktreeRoot = %q, want %q", got.WorktreeRoot, worktree)
		}
		if got.Bare {
			t.Errorf("Bare = true, want false")
		}
	})
}

func TestResolveWorkspaceLayoutRelativeGitDir(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := t.TempDir()
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "config", "worktree.useRelativePaths", "true")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	got := ResolveWorkspaceLayout(filepath.Join(root, "main"))

	if got.BindRoot != root {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
	}
	if got.WorktreeRoot != filepath.Join(root, "main") {
		t.Errorf("WorktreeRoot = %q, want %q", got.WorktreeRoot, filepath.Join(root, "main"))
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
	if !got.Bare {
		t.Errorf("Bare = false, want true")
	}
}
