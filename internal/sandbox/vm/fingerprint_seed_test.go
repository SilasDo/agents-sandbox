package vm

import (
	"context"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	sandboximage "github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestResolveHomeVolumeSeedsFingerprints is the regression test for #183: a
// fresh home volume's state must carry the desired config fingerprints, so a
// second session reading the state beside the freshly created VM never mistakes
// the empty applied fingerprint for a "network policy changed" (or env/secret/
// mount) change. Seeding happens as part of the fresh-home write, closing both
// the concurrent window and the crash/deleted-state route.
func TestResolveHomeVolumeSeedsFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := termio.NewTestMock(t)
	k := state.Key{Slug: "seedproj", Agent: "opencode"}
	policy := network.Policy{Profile: network.ProfileNone}
	extraMounts := mounts.Mounts{
		"/host/data": {Source: "/host/data"},
	}

	fingerprints := state.Fingerprints{
		Network: state.BuildNetworkState(policy),
		Mount:   state.BuildMountState(extraMounts),
	}
	_, homeState, err := resolveHomeVolume(
		context.Background(),
		volume.NewManager(&ui),
		client,
		k,
		sandboximage.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:digest"},
		true,
		fingerprints,
		"",
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveHomeVolume: %v", err)
	}

	if state.NetworkChanged(homeState.NetworkState, policy) {
		t.Error("#183: fresh home state reports a spurious network change")
	}
	if state.MountsChanged(homeState.MountState, extraMounts) {
		t.Error("#183: fresh home state reports a spurious mount change")
	}

	onDisk, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if state.NetworkChanged(onDisk.NetworkState, policy) {
		t.Error("#183: persisted fresh state is fingerprint-less; a concurrent session sees a spurious network change")
	}
	if state.MountsChanged(onDisk.MountState, extraMounts) {
		t.Error("#183: persisted fresh state is fingerprint-less; a concurrent session sees a spurious mount change")
	}
}
