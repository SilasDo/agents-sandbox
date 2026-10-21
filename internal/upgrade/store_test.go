package upgrade

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestMemoryStoreRoundTrips(t *testing.T) {
	var s memoryStore
	got, err := s.Load()
	if err != nil || !got.LastChecked.IsZero() || len(got.DismissedVersions) != 0 {
		t.Fatalf("empty load = %+v, %v", got, err)
	}
	want := LauncherState{DismissedVersions: []string{"2.0.0"}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err = s.Load()
	if err != nil || len(got.DismissedVersions) != 1 || got.DismissedVersions[0] != "2.0.0" {
		t.Fatalf("round-trip = %+v, %v", got, err)
	}
}

// writeLegacy writes the retired upgrade.json at the legacy path, creating the
// user state directory first.
func writeLegacy(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(configpaths.Get().UserStateDir(), 0o700); err != nil {
		t.Fatalf("create user state dir: %v", err)
	}
	if err := os.WriteFile(legacyStatePath(), []byte(body), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}
}

func TestFoldLegacyStateCopiesAndRemoves(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"last_check":"2026-09-01T00:00:00Z","dismissed_versions":["1.5.2"]}`)
	s := &fakeStore{}
	if err := FoldLegacyState(s); err != nil {
		t.Fatal(err)
	}
	if s.state.LastChecked.IsZero() || len(s.state.DismissedVersions) != 1 {
		t.Fatalf("fold = %+v", s.state)
	}
	if _, err := os.Stat(legacyStatePath()); !os.IsNotExist(err) {
		t.Fatalf("legacy file not removed: %v", err)
	}
}

func TestFoldLegacyStateUnionsDismissalsAndKeepsPresentLastChecked(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"last_check":"2026-09-01T00:00:00Z","dismissed_versions":["1.5.2"]}`)
	present := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s := &fakeStore{state: LauncherState{LastChecked: present, DismissedVersions: []string{"2.0.0"}}}
	if err := FoldLegacyState(s); err != nil {
		t.Fatal(err)
	}
	if !s.state.LastChecked.Equal(present) {
		t.Fatalf("present last_checked overridden: %v", s.state.LastChecked)
	}
	if len(s.state.DismissedVersions) != 2 {
		t.Fatalf("dismissals not unioned: %+v", s.state.DismissedVersions)
	}
}

func TestFoldLegacyStateCorruptRemovedWithoutFolding(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, "{not json")
	s := &fakeStore{}
	if err := FoldLegacyState(s); err != nil {
		t.Fatal(err)
	}
	if !s.state.LastChecked.IsZero() || len(s.state.DismissedVersions) != 0 {
		t.Fatalf("corrupt file folded something: %+v", s.state)
	}
	if _, err := os.Stat(legacyStatePath()); !os.IsNotExist(err) {
		t.Fatalf("corrupt legacy file not removed: %v", err)
	}
}

func TestFoldLegacyStateKeepsFileOnSaveFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"dismissed_versions":["1.5.2"]}`)
	s := &saveFailingStore{inner: &fakeStore{}}
	if err := FoldLegacyState(s); err == nil {
		t.Fatal("expected the save failure to surface")
	}
	if _, err := os.Stat(legacyStatePath()); err != nil {
		t.Fatalf("legacy file removed despite save failure: %v", err)
	}
}

// foldLegacyState must not delete the legacy file it could not read the store
// for: a load failure leaves the file in place so a later check retries.
func TestFoldLegacyStateKeepsFileOnLoadFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"dismissed_versions":["1.5.2"]}`)
	s := &fakeStore{err: errors.New("load boom")}
	if err := FoldLegacyState(s); err == nil {
		t.Fatal("expected the load failure to surface")
	}
	if _, err := os.Stat(legacyStatePath()); err != nil {
		t.Fatalf("legacy file removed despite load failure: %v", err)
	}
}

func TestFoldLegacyStateUnreadableFileIsRetried(t *testing.T) {
	// A read error other than "does not exist" leaves the file in place so a
	// later check retries the fold.
	configpaths.WithMockConfigPaths(t)
	if err := os.Mkdir(legacyStatePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := FoldLegacyState(&fakeStore{}); err == nil {
		t.Fatal("expected a read error to surface")
	}
}

// storeNotTouchedStore fails the test if Load or Save is called.
type storeNotTouchedStore struct{ t *testing.T }

func (s *storeNotTouchedStore) Load() (LauncherState, error) {
	s.t.Fatal("Load must not be called")
	return LauncherState{}, nil
}

func (s *storeNotTouchedStore) Save(LauncherState) error {
	s.t.Fatal("Save must not be called")
	return nil
}

func TestFoldLegacyStateMissingFileLeavesStoreUntouched(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	_ = os.Remove(legacyStatePath())
	if err := FoldLegacyState(&storeNotTouchedStore{t: t}); err != nil {
		t.Fatalf("missing file must be a no-op, got %v", err)
	}
}

func TestLegacyStatePathLivesInUserStateDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	want := filepath.Join(configpaths.Get().UserStateDir(), "upgrade.json")
	if got := legacyStatePath(); got != want {
		t.Fatalf("legacyStatePath() = %q, want %q", got, want)
	}
}
