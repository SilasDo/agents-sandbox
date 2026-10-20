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
	state, err := os.ReadFile(filepath.Join(SlugDir("new-slug"), "opencode", "state.yaml"))
	require.NoError(t, err)
	require.Equal(t, "home_volume: v1\n", string(state))
	lease, err := os.ReadFile(filepath.Join(SlugDir("new-slug"), "opencode", "clients", "lease"))
	require.NoError(t, err)
	require.Equal(t, "x", string(lease))
}

// TestMoveSlugDirKeepsExistingDestinationEntry covers the state an aborted run
// leaves behind: the destination already owns an entry of the same name. The
// move must neither overwrite nor merge it, and must still move every other
// entry.
func TestMoveSlugDirKeepsExistingDestinationEntry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "state.yaml"), "home_volume: v1\n")
	writeFile(t, filepath.Join(SlugDir("old-slug"), "claims", "notify.claim"), "y")
	writeFile(t, filepath.Join(SlugDir("new-slug"), "opencode", "ensure-vm.lock"), "")

	skipped, err := MoveSlugDir("old-slug", "new-slug")
	require.NoError(t, err)
	require.Equal(t, []string{"opencode"}, skipped)

	require.FileExists(t, filepath.Join(SlugDir("new-slug"), "opencode", "ensure-vm.lock"))
	require.NoFileExists(t, filepath.Join(SlugDir("new-slug"), "opencode", "state.yaml"))
	require.FileExists(t, filepath.Join(SlugDir("old-slug"), "opencode", "state.yaml"))
	require.DirExists(t, SlugDir("old-slug"))

	require.FileExists(t, filepath.Join(SlugDir("new-slug"), "claims", "notify.claim"))
	require.NoFileExists(t, filepath.Join(SlugDir("old-slug"), "claims", "notify.claim"))
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
