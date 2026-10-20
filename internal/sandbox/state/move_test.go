package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"

	"github.com/stretchr/testify/require"
)

// writeFile builds a state tree entry, creating the parent directories, so a
// test can lay out a project state directory without repeating MkdirAll.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent dir for %s: %v", path, err)
	}
	testutil.WritePath(t, path, content)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}

// TestSlugDirIsStateRootChild pins that SlugDir is the agent-agnostic parent of
// KeyDir, so the two cannot drift apart.
func TestSlugDirIsStateRootChild(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	projectDir := filepath.Join(stateRoot(), "widget-abc")
	require.Equal(t, projectDir, SlugDir("widget-abc"))
	require.Equal(t, projectDir, filepath.Dir(KeyDir(Key{Slug: "widget-abc", Agent: "opencode"})))
}

func TestMoveSlugDirMovesEveryEntry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "state.yaml"), "home_volume: v1\n")
	writeFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "clients", "lease"), "x")
	writeFile(t, filepath.Join(SlugDir("old-slug"), "claims", "notify.claim"), "y")

	skipped, err := MoveSlugDir("old-slug", "new-slug")
	require.NoError(t, err)
	require.Empty(t, skipped)
	require.FileExists(t, filepath.Join(SlugDir("new-slug"), "opencode", "state.yaml"))
	require.FileExists(t, filepath.Join(SlugDir("new-slug"), "opencode", "clients", "lease"))
	require.FileExists(t, filepath.Join(SlugDir("new-slug"), "claims", "notify.claim"))
	require.NoDirExists(t, SlugDir("old-slug"))

	// The move relocates the tree; it must not rewrite or drop the contents.
	require.Equal(t, "home_volume: v1\n", readFile(t, filepath.Join(SlugDir("new-slug"), "opencode", "state.yaml")))
	require.Equal(t, "x", readFile(t, filepath.Join(SlugDir("new-slug"), "opencode", "clients", "lease")))
}

// TestMoveSlugDirMergesIntoAnExistingDestinationDirectory covers the collision
// that would otherwise silently lose a project: the destination already owns the
// agent's directory, holding a stray lock from an aborted run. The legacy state
// file must still arrive, or the adoption trigger re-fires forever and the run
// proceeds under the checkout identity with no state at all.
func TestMoveSlugDirMergesIntoAnExistingDestinationDirectory(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "state.yaml"), "home_volume: v1\n")
	writeFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "ensure-vm.lock"), "legacy lock")
	writeFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "clients", "lease"), "old lease")
	writeFile(t, filepath.Join(SlugDir("old-slug"), "claims", "notify.claim"), "y")
	writeFile(t, filepath.Join(SlugDir("new-slug"), "opencode", "ensure-vm.lock"), "stray lock")
	writeFile(t, filepath.Join(SlugDir("new-slug"), "opencode", "clients", "keep"), "keep")

	skipped, err := MoveSlugDir("old-slug", "new-slug")
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join("opencode", "ensure-vm.lock")}, skipped)

	require.Equal(t, "home_volume: v1\n", readFile(t, filepath.Join(SlugDir("new-slug"), "opencode", "state.yaml")))
	require.NoFileExists(t, filepath.Join(SlugDir("old-slug"), "opencode", "state.yaml"))

	// Both pre-existing destination entries survive byte-identical, and the
	// merged sibling lands beside them.
	require.Equal(t, "stray lock", readFile(t, filepath.Join(SlugDir("new-slug"), "opencode", "ensure-vm.lock")))
	require.Equal(t, "keep", readFile(t, filepath.Join(SlugDir("new-slug"), "opencode", "clients", "keep")))
	require.Equal(t, "old lease", readFile(t, filepath.Join(SlugDir("new-slug"), "opencode", "clients", "lease")))

	// The colliding entry stays legacy, so the caller can name it, and the
	// emptied sibling directories are gone.
	require.Equal(t, "legacy lock", readFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "ensure-vm.lock")))
	require.NoDirExists(t, filepath.Join(SlugDir("old-slug"), "opencode", "clients"))
	require.NoDirExists(t, filepath.Join(SlugDir("old-slug"), "claims"))
	require.DirExists(t, SlugDir("old-slug"))

	require.Equal(t, "y", readFile(t, filepath.Join(SlugDir("new-slug"), "claims", "notify.claim")))
}

func TestMoveSlugDirSkipsCollidingFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeFile(t, filepath.Join(SlugDir("old-slug"), "stray.lock"), "legacy lock")
	writeFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "state.yaml"), "home_volume: v1\n")
	writeFile(t, filepath.Join(SlugDir("new-slug"), "stray.lock"), "new lock")

	skipped, err := MoveSlugDir("old-slug", "new-slug")
	require.NoError(t, err)
	require.Equal(t, []string{"stray.lock"}, skipped)

	require.Equal(t, "legacy lock", readFile(t, filepath.Join(SlugDir("old-slug"), "stray.lock")))
	require.Equal(t, "new lock", readFile(t, filepath.Join(SlugDir("new-slug"), "stray.lock")))
	require.FileExists(t, filepath.Join(SlugDir("new-slug"), "opencode", "state.yaml"))
}

func TestMoveSlugDirSkipsTypeMismatch(t *testing.T) {
	t.Run("destination file where the source has a directory", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		writeFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "state.yaml"), "home_volume: v1\n")
		writeFile(t, filepath.Join(SlugDir("new-slug"), "opencode"), "not a directory")

		skipped, err := MoveSlugDir("old-slug", "new-slug")
		require.NoError(t, err)
		require.Equal(t, []string{"opencode"}, skipped)

		require.FileExists(t, filepath.Join(SlugDir("old-slug"), "opencode", "state.yaml"))
		require.Equal(t, "not a directory", readFile(t, filepath.Join(SlugDir("new-slug"), "opencode")))
	})

	t.Run("destination directory where the source has a file", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		writeFile(t, filepath.Join(SlugDir("old-slug"), "claims"), "not a directory")
		writeFile(t, filepath.Join(SlugDir("new-slug"), "claims", "keep"), "keep")

		skipped, err := MoveSlugDir("old-slug", "new-slug")
		require.NoError(t, err)
		require.Equal(t, []string{"claims"}, skipped)

		require.Equal(t, "not a directory", readFile(t, filepath.Join(SlugDir("old-slug"), "claims")))
		require.Equal(t, "keep", readFile(t, filepath.Join(SlugDir("new-slug"), "claims", "keep")))
	})
}

func TestMoveSlugDirMissingSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	skipped, err := MoveSlugDir("never-created", "new-slug")
	require.NoError(t, err)
	require.Empty(t, skipped)
	require.NoDirExists(t, SlugDir("new-slug"))
}

func TestMoveSlugDirEmptySource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	require.NoError(t, os.MkdirAll(SlugDir("old-slug"), 0o700))

	skipped, err := MoveSlugDir("old-slug", "new-slug")
	require.NoError(t, err)
	require.Empty(t, skipped)
	require.NoDirExists(t, SlugDir("new-slug"))
	require.NoDirExists(t, SlugDir("old-slug"))
}

// TestMoveSlugDirSameSlugIsNoOp guards the self-move: without the early return
// every entry would collide with itself and the caller would report a bogus
// skip list.
func TestMoveSlugDirSameSlugIsNoOp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeFile(t, filepath.Join(SlugDir("some-slug"), "opencode", "state.yaml"), "home_volume: v1\n")

	skipped, err := MoveSlugDir("some-slug", "some-slug")
	require.NoError(t, err)
	require.Empty(t, skipped)
	require.FileExists(t, filepath.Join(SlugDir("some-slug"), "opencode", "state.yaml"))
}

func TestMoveSlugDirCreatesDestinationParent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "state.yaml"), "home_volume: v1\n")
	require.NoDirExists(t, SlugDir("new-slug"))

	skipped, err := MoveSlugDir("old-slug", "new-slug")
	require.NoError(t, err)
	require.Empty(t, skipped)

	require.DirExists(t, SlugDir("new-slug"))
	require.FileExists(t, filepath.Join(SlugDir("new-slug"), "opencode", "state.yaml"))
}
