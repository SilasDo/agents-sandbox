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

	want := filepath.Join(configpaths.Get().UserStateDir(), "locks", k.Slug, k.Agent)
	if got := ProjectVMLockDir(k); got != want {
		t.Fatalf("ProjectVMLockDir = %q, want %q", got, want)
	}
	if strings.HasPrefix(ProjectVMLockDir(k), KeyDir(k)) {
		t.Fatal("project-VM lock dir must not live in the removable key dir")
	}
}

// TestProjectVMLockDirIsAgentScoped pins the project-VM creation lock to the
// agent. Agents of one project use separate VMs (see projectVMName), so sharing
// one creation lock would make an unrelated agent's creation wait (#330).
func TestProjectVMLockDirIsAgentScoped(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	opencode := Key{Slug: "proj", Agent: "opencode"}
	pi := Key{Slug: "proj", Agent: "pi"}

	if ProjectVMLockDir(opencode) == ProjectVMLockDir(pi) {
		t.Fatalf("different agents must not share a creation lock: both %q", ProjectVMLockDir(opencode))
	}
}
