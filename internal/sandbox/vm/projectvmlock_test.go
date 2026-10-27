package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// TestProjectVMFlockPathLivesUnderLocksDir pins ensureProjectVM's VM-creation
// lock file outside KeyDir, under the never-deleted locks tree (#330).
func TestProjectVMFlockPathLivesUnderLocksDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "myproj", Agent: "opencode"}

	got := projectVMFlockPath(k)
	want := filepath.Join(state.ProjectVMLockDir(k), "ensure-vm.lock")
	if got != want {
		t.Fatalf("projectVMFlockPath = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, state.KeyDir(k)) {
		t.Fatal("project-VM lock must not live in the removable key dir")
	}
}

// TestProjectVMFlockSurvivesStateRemoval is the #330 regression: a held
// project-VM lock must survive prune's RemoveHomeState, so a later invocation
// still contends instead of locking a fresh file at the same path.
func TestProjectVMFlockSurvivesStateRemoval(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "myproj", Agent: "opencode"}

	lockPath := projectVMFlockPath(k)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	release, err := acquireProjectFlock(lockPath)
	if err != nil {
		t.Fatalf("acquireProjectFlock: %v", err)
	}
	defer release()

	if err := state.RemoveHomeState(k); err != nil {
		t.Fatalf("RemoveHomeState: %v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("project-VM lock must survive state removal: %v", err)
	}

	probe, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock for probe: %v", err)
	}
	defer probe.Close()
	if err := state.FlockExclusiveNB(probe); err == nil {
		t.Fatal("held project-VM lock must still contend after state removal")
	}
}
