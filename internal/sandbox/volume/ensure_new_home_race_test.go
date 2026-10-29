package volume

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

var raceTestImage = image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"}

// commitConcurrentHome records volume as the project's home, the way a racing
// session's EnsureNewHome commits its fresh volume.
func commitConcurrentHome(t *testing.T, k state.Key, volume string) {
	t.Helper()
	require.NoError(t, seedHomeState(k, state.NewHomeState(volume, "sha256:winner")))
}

// withNoHomeVolumeNameWait replaces the wait between colliding volume names
// with a counter, so collision tests do not sleep.
func withNoHomeVolumeNameWait(t *testing.T) *int {
	t.Helper()
	waits := 0
	restore := waitForNextHomeVolumeName
	waitForNextHomeVolumeName = func() { waits++ }
	t.Cleanup(func() { waitForNextHomeVolumeName = restore })
	return &waits
}

// TestEnsureNewHome_ConcurrentCommitAdoptsWinner covers #431: when another
// session commits a fresh home volume while this one is creating its own, the
// later commit must not clobber the earlier one. It adopts the committed volume
// and removes its own, so no volume is left unrecorded.
func TestEnsureNewHome_ConcurrentCommitAdoptsWinner(t *testing.T) {
	tests := []struct {
		name          string
		priorRecorded string
	}{
		{name: "state absent", priorRecorded: ""},
		{name: "recorded volume gone", priorRecorded: "gone-vol"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			k := state.Key{Slug: "raceproj", Agent: "opencode"}
			if tt.priorRecorded != "" {
				require.NoError(t, seedHomeState(k, state.NewHomeState(tt.priorRecorded, "sha256:old")))
			}
			var createdVolume string
			client := &msb.MockMsbClient{
				CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
					createdVolume = name
					commitConcurrentHome(t, k, "winner-vol")
					return msb.MockVolumeHandle{Name_: name}, nil
				},
			}
			ui := &termio.Mock{}

			volName, hs, err := NewManager(ui).EnsureNewHome(
				context.Background(), client, k, raceTestImage, true, state.Fingerprints{}, ui,
			)

			require.NoError(t, err)
			require.Equal(t, "winner-vol", volName)
			require.Equal(t, "winner-vol", hs.HomeVolume)
			require.Equal(t, []string{createdVolume}, client.RemovedVolumes)
			persisted, err := state.ReadState(k)
			require.NoError(t, err)
			require.Equal(t, "winner-vol", persisted.HomeVolume)
			require.Equal(t, "sha256:winner", persisted.ImageDigest)
		})
	}
}

// TestEnsureNewHome_ReplacesStaleRecordedVolume pins that a state whose
// recorded volume is gone, unchanged since the snapshot, is still replaced.
func TestEnsureNewHome_ReplacesStaleRecordedVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "staleproj", Agent: "opencode"}
	require.NoError(t, seedHomeState(k, state.NewHomeState("gone-vol", "sha256:old")))
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}

	volName, _, err := NewManager(ui).EnsureNewHome(
		context.Background(), client, k, raceTestImage, true, state.Fingerprints{}, ui,
	)

	require.NoError(t, err)
	require.NotEqual(t, "gone-vol", volName)
	require.Empty(t, client.RemovedVolumes)
	persisted, err := state.ReadState(k)
	require.NoError(t, err)
	require.Equal(t, volName, persisted.HomeVolume)
}

// TestEnsureNewHome_NameCollisionRetries covers the same-second case of #431:
// HomeVolumeName has second granularity, so two racing sessions can generate
// the same name. The loser waits for the next name instead of failing.
func TestEnsureNewHome_NameCollisionRetries(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	waits := withNoHomeVolumeNameWait(t)
	k := state.Key{Slug: "collideproj", Agent: "opencode"}
	attempts := 0
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			attempts++
			if attempts == 1 {
				return nil, &msbSdk.Error{Kind: msbSdk.ErrVolumeAlreadyExists, Message: "exists"}
			}
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}

	volName, _, err := NewManager(ui).EnsureNewHome(
		context.Background(), client, k, raceTestImage, true, state.Fingerprints{}, ui,
	)

	require.NoError(t, err)
	require.NotEmpty(t, volName)
	require.Equal(t, 2, attempts)
	require.Equal(t, 1, *waits)
}

func TestEnsureNewHome_NameCollisionGivesUp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	waits := withNoHomeVolumeNameWait(t)
	k := state.Key{Slug: "collideproj", Agent: "opencode"}
	attempts := 0
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			attempts++
			return nil, &msbSdk.Error{Kind: msbSdk.ErrVolumeAlreadyExists, Message: "exists"}
		},
	}
	ui := &termio.Mock{}

	_, _, err := NewManager(ui).EnsureNewHome(
		context.Background(), client, k, raceTestImage, true, state.Fingerprints{}, ui,
	)

	require.Error(t, err)
	require.True(t, msb.IsVolumeAlreadyExists(err))
	require.Equal(t, homeVolumeNameAttempts, attempts)
	require.Equal(t, homeVolumeNameAttempts-1, *waits)
}

func TestEnsureNewHome_CreateErrorDoesNotRetry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	waits := withNoHomeVolumeNameWait(t)
	createErr := errors.New("disk full")
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return nil, createErr
		},
	}
	ui := &termio.Mock{}

	_, _, err := NewManager(ui).EnsureNewHome(
		context.Background(), client, state.Key{Slug: "failproj", Agent: "opencode"},
		raceTestImage, true, state.Fingerprints{}, ui,
	)

	require.ErrorIs(t, err, createErr)
	require.Zero(t, *waits)
}
