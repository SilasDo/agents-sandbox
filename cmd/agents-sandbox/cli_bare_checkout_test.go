package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// setupBareCheckoutFixtures builds a bare checkout whose single worktree lives
// inside the bare root, installs the standard run/shell command fixture inside
// that worktree, and returns the command, UI, and bare root. The bare layout
// needs the real path resolver, so HOME and every XDG directory are pinned to
// temp dirs to keep host state out of reach. Not parallel: t.Setenv/t.Chdir
// forbid it.
func setupBareCheckoutFixtures(t *testing.T, args ...string) (*cobra.Command, *termio.Mock, string) {
	t.Helper()
	origin := testutil.InitRepo(t)
	root := t.TempDir()
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	cmd, ui := setupRunMocks(t, &sandboxmsb.MockMsbClient{}, &sandboxmsb.MockSandbox{}, args...)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Override the command fixture's mock paths with the real resolver; cleanup
	// runs LIFO, so the mock (and then the fail-fast default) is restored.
	configpaths.WithRealConfigPaths(t)
	t.Chdir(filepath.Join(root, "main"))
	return cmd, ui, root
}

// TestBuildDockerfileCommandReadsBareRoot asserts that a bare checkout launched
// from a worktree prints the bare root's project Dockerfile, never the ignored
// worktree copy, and warns about that ignored directory.
func TestBuildDockerfileCommandReadsBareRoot(t *testing.T) {
	cmd, ui, root := setupBareCheckoutFixtures(t, cmdBuild, cmdDockerfile)
	ignoredDir := filepath.Join(root, "main", ".agents-sandbox")
	writeBareFixtureFile(t, filepath.Join(root, ".agents-sandbox", "Dockerfile"), "FROM bare-root\n")
	writeBareFixtureFile(t, filepath.Join(ignoredDir, "Dockerfile"), "FROM worktree\n")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := ui.StdOutBuffer.String()
	if !strings.Contains(out, "bare-root") {
		t.Errorf("dockerfile output missing the bare-root project Dockerfile; got:\n%s", out)
	}
	if strings.Contains(out, "worktree") {
		t.Errorf("dockerfile output must not contain the ignored worktree copy; got:\n%s", out)
	}
	assertWarningNames(t, ui, ignoredDir)
}

// TestRunCommandReadsBareRoot asserts the real run path resolves the bare root
// (reaching EnsureImage) by warning about the ignored worktree .agents-sandbox.
func TestRunCommandReadsBareRoot(t *testing.T) {
	cmd, ui, root := setupBareCheckoutFixtures(t, cmdRun, flagDryRun)
	ignoredDir := filepath.Join(root, "main", ".agents-sandbox")
	if err := os.MkdirAll(ignoredDir, 0o750); err != nil {
		t.Fatalf("MkdirAll(%q): %v", ignoredDir, err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertWarningNames(t, ui, ignoredDir)
}

// assertWarningNames fails unless a warning names the ignored directory,
// resolving symlinks the way the resolver derives the bare base.
func assertWarningNames(t *testing.T, ui *termio.Mock, ignoredDir string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(ignoredDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", ignoredDir, err)
	}
	for _, warning := range ui.WarnCalls {
		if strings.Contains(warning, resolved) {
			return
		}
	}
	t.Errorf("no warning names the ignored directory %q; got: %v", resolved, ui.WarnCalls)
}

func writeBareFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
