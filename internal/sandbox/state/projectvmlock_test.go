package state

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// TestProjectVMLockDirLivesUnderLocksDir pins the project-VM creation lock to
// the never-deleted locks tree. It lived under KeyDir before #330, so prune's
// RemoveHomeState unlinked the lock a concurrent VM creation held.
func TestProjectVMLockDirLivesUnderLocksDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	want := filepath.Join(configpaths.Get().UserStateDir(), "locks", k.Slug)
	if got := ProjectVMLockDir(k); got != want {
		t.Fatalf("ProjectVMLockDir = %q, want %q", got, want)
	}
	if strings.HasPrefix(ProjectVMLockDir(k), KeyDir(k)) {
		t.Fatal("project-VM lock dir must not live in the removable key dir")
	}
}
