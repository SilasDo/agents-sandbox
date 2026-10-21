package volume

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// noCreateVolumeMock returns an msb mock whose GetVolume honours names, and
// whose CreateVolume fails the test: a preview must never create a volume.
func noCreateVolumeMock(t *testing.T, present ...string) *msb.MockMsbClient {
	t.Helper()
	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if slices.Contains(present, name) {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrVolumeNotFound, Message: name}
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must never be called by a preview (got %q)", name)
		panic("unreachable")
	}
	return mock
}

func TestPreviewHomeVolumeReusesRecordedPresentVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "previewproj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol-1"}); err != nil {
		t.Fatal(err)
	}
	mock := noCreateVolumeMock(t, "vol-1")

	gotName, gotCreate := PreviewHomeVolume(context.Background(), mock, k, "")
	if gotName != "vol-1" || gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (%q, false)", gotName, gotCreate, "vol-1")
	}
}

func TestPreviewHomeVolumeReusesRecoveredVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "previewproj", Agent: "opencode"}
	mock := noCreateVolumeMock(t, "live-1")

	gotName, gotCreate := PreviewHomeVolume(context.Background(), mock, k, "live-1")
	if gotName != "live-1" || gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (%q, false)", gotName, gotCreate, "live-1")
	}
}

// TestPreviewHomeVolumeRecordedVolumeGoneFallsBackToRecovered pins the
// precedence ResolveHomeVolume uses: a recorded volume that no longer exists
// does not win over a live recovered one.
func TestPreviewHomeVolumeRecordedVolumeGoneFallsBackToRecovered(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "previewproj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol-gone"}); err != nil {
		t.Fatal(err)
	}
	mock := noCreateVolumeMock(t, "live-1")

	gotName, gotCreate := PreviewHomeVolume(context.Background(), mock, k, "live-1")
	if gotName != "live-1" || gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (%q, false)", gotName, gotCreate, "live-1")
	}
}

// TestPreviewHomeVolumeReportsCreateWhenNonePresent pins that the create branch
// names no volume: a fresh volume's name embeds a timestamp, so there is no
// single name the preview could report.
func TestPreviewHomeVolumeReportsCreateWhenNonePresent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "previewproj", Agent: "opencode"}
	mock := noCreateVolumeMock(t)

	gotName, gotCreate := PreviewHomeVolume(context.Background(), mock, k, "")
	if gotName != "" || !gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (\"\", true)", gotName, gotCreate)
	}
}

// TestPreviewHomeVolumeReportsCreateWhenRecoveredVolumeMissing pins that an
// unusable recovered candidate is not adopted.
func TestPreviewHomeVolumeReportsCreateWhenRecoveredVolumeMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "previewproj", Agent: "opencode"}
	mock := noCreateVolumeMock(t)

	gotName, gotCreate := PreviewHomeVolume(context.Background(), mock, k, "live-gone")
	if gotName != "" || !gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (\"\", true)", gotName, gotCreate)
	}
}

// TestPreviewHomeVolumeCorruptStateReportsCreate covers a state file that
// cannot be read: the preview must report a create rather than fail, and leave
// the corrupt document untouched for the real run to repair.
func TestPreviewHomeVolumeCorruptStateReportsCreate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "corruptpreview", Agent: "opencode"}
	if err := os.MkdirAll(state.KeyDir(k), 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(state.KeyDir(k), "state.yaml")
	if err := os.WriteFile(statePath, []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mock := noCreateVolumeMock(t)

	gotName, gotCreate := PreviewHomeVolume(context.Background(), mock, k, "")
	if gotName != "" || !gotCreate {
		t.Errorf("PreviewHomeVolume = (%q, %v), want (\"\", true)", gotName, gotCreate)
	}

	onDisk, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("state file after preview: %v", err)
	}
	if string(onDisk) != "{ corrupt: [\n" {
		t.Errorf("preview rewrote the state file: %q", onDisk)
	}
}
