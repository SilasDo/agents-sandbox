package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/upgrade"
)

func TestLauncherStoreRoundTripsThroughRootState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	s := launcherStore{}
	if got, err := s.Load(); err != nil || !got.LastChecked.IsZero() {
		t.Fatalf("empty load = %+v, %v", got, err)
	}
	want := upgrade.LauncherState{DismissedVersions: []string{"2.0.0"}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DismissedVersions) != 1 || got.DismissedVersions[0] != "2.0.0" {
		t.Fatalf("round-trip = %+v", got)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatal(err)
	}
	if root.Updates.Launcher.DismissedVersions[0] != "2.0.0" {
		t.Fatalf("not persisted under updates.launcher: %+v", root.Updates)
	}
}

// A corrupt root document must surface as an error rather than read as an empty
// state: upgrade.Check then defers the check and the legacy-file fold keeps its
// file, instead of overwriting the unreadable document with a zero one.
func TestLauncherStoreErrorsOnCorruptRootState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	statePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("migrations: [not-a-map\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := launcherStore{}
	if _, err := s.Load(); err == nil {
		t.Fatal("expected Load to fail on a corrupt root document")
	}
	if err := s.Save(upgrade.LauncherState{DismissedVersions: []string{"2.0.0"}}); err == nil {
		t.Fatal("expected Save to fail on a corrupt root document")
	}
	remaining, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(remaining), "not-a-map") {
		t.Fatalf("failed Save clobbered the corrupt document: %q", remaining)
	}
}
