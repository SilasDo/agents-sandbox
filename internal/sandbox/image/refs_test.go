package image

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestEnsureLoadedReusesLegacyRefWhenContentMatches pins the no-reimport rule:
// when a pre-change install already cached the image under the legacy reference
// with the same content, that reference is reused instead of loading a second
// copy under the prefix-free reference.
func TestEnsureLoadedReusesLegacyRefWhenContentMatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.LegacyRunnerImageRef("test-project", "opencode")
	docker.WithNoopDockerMock(t)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == legacyRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:same"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when the legacy ref matches, got %v", msbClient.LoadedImages)
	}
	if info.Ref != legacyRef {
		t.Errorf("info.Ref = %q, want the reused legacy ref %q", info.Ref, legacyRef)
	}
}

// TestEnsureLoadedLoadsUnderNewRefWhenLegacyAbsent pins that a fresh install
// loads the image under the prefix-free reference.
func TestEnsureLoadedLoadsUnderNewRefWhenLegacyAbsent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.LegacyRunnerImageRef("test-project", "opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(
			_ context.Context, _ []string, _ ...client.ImageSaveOption,
		) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:new"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != newRef {
		t.Errorf("LoadedImages = %v, want [%s]", msbClient.LoadedImages, newRef)
	}
	if info.Ref != newRef {
		t.Errorf("info.Ref = %q, want %q", info.Ref, newRef)
	}
}

// TestEnsureLoadedLoadsUnderNewRefWhenLegacyContentDiffers pins that a legacy
// reference holding stale content is not reused: the current Docker image is
// loaded under the prefix-free reference instead.
func TestEnsureLoadedLoadsUnderNewRefWhenLegacyContentDiffers(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	newRef := naming.RunnerImageRef("test-project", "opencode")
	legacyRef := naming.LegacyRunnerImageRef("test-project", "opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(
			_ context.Context, _ []string, _ ...client.ImageSaveOption,
		) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == legacyRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:old"}, nil
		},
	}

	info, err := EnsureLoaded(
		context.Background(),
		msbClient,
		ImageInfo{DockerTag: legacyRef, Ref: newRef, Digest: "sha256:new"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 || msbClient.LoadedImages[0] != newRef {
		t.Errorf("LoadedImages = %v, want [%s]", msbClient.LoadedImages, newRef)
	}
	if info.Ref != newRef {
		t.Errorf("info.Ref = %q, want %q", info.Ref, newRef)
	}
}
