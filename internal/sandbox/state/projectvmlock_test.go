package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// TestProjectVMLockDirLivesUnderLocksDir pins the project-VM creation lock to
// the never-deleted locks tree. It lived under KeyDir before #330, so prune's
// state removal unlinked the lock a concurrent VM creation held.
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

// TestLockProjectVMFailsFastWhileHeld pins the same-process guard: a second
// blocking acquire of a held project-VM lock errors instead of deadlocking.
func TestLockProjectVMFailsFastWhileHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, _, err := lockProjectVM(k, true)
	if err != nil {
		t.Fatalf("lockProjectVM: %v", err)
	}
	defer release()

	if _, _, err := lockProjectVM(k, true); err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("second blocking acquire = %v, want an already-held error", err)
	}
}

// TestLockProjectVMFlockContention pins the non-blocking contract callers rely
// on: contention reports ok=false, not an error.
func TestLockProjectVMFlockContention(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, _, err := lockProjectVM(k, true)
	if err != nil {
		t.Fatalf("lockProjectVM: %v", err)
	}
	defer release()

	_, ok, err := lockProjectVM(k, false)
	if err != nil {
		t.Fatalf("non-blocking acquire: %v", err)
	}
	if ok {
		t.Fatal("non-blocking acquire must report contention while the lock is held")
	}
}

// TestLockProjectVMNonBlockingSucceedsWhenFree pins the non-blocking success
// path (and its release).
func TestLockProjectVMNonBlockingSucceedsWhenFree(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, ok, err := lockProjectVM(k, false)
	if err != nil {
		t.Fatalf("lockProjectVM: %v", err)
	}
	if !ok {
		t.Fatal("non-blocking acquire must succeed when the lock is free")
	}
	release()
}

func TestLockProjectVMMkdirError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	orig := mkdirProjectVMLock
	mkdirProjectVMLock = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirProjectVMLock = orig })

	_, _, err := lockProjectVM(testKey(), true)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "create lock dir") {
		t.Fatalf("lockProjectVM() = %v, want a create-lock-dir error wrapping %v", err, wantErr)
	}
}

func TestLockProjectVMOpenError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("open boom")
	orig := openProjectVMLock
	openProjectVMLock = func(string, int, os.FileMode) (*os.File, error) { return nil, wantErr }
	t.Cleanup(func() { openProjectVMLock = orig })

	_, _, err := lockProjectVM(testKey(), true)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "open project VM lock") {
		t.Fatalf("lockProjectVM() = %v, want an open error wrapping %v", err, wantErr)
	}
}

func TestLockProjectVMFlockErrorReleasesRegistry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	wantErr := errors.New("flock boom")
	orig := flockProjectVMLock
	flockProjectVMLock = func(*os.File, bool) error { return wantErr }
	t.Cleanup(func() { flockProjectVMLock = orig })

	if _, _, err := lockProjectVM(k, true); !errors.Is(err, wantErr) {
		t.Fatalf("lockProjectVM() = %v, want %v", err, wantErr)
	}

	path := filepath.Join(ProjectVMLockDir(k), ProjectVMLockName)
	if err := holdLockPath(path); err != nil {
		t.Fatalf("a failed acquire leaked its registry entry: %v", err)
	}
	releaseLockPath(path)
}
