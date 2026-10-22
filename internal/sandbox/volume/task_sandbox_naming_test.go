package volume

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestPrefillSandboxNameIsPrefixFreeAndLabelled pins the task sandbox shape: no
// vendor prefix on the name, and the task label that makes it classifiable
// without one.
func TestPrefillSandboxNameIsPrefixFreeAndLabelled(t *testing.T) {
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{CreatedSandbox: &msb.MockSandbox{Name_: "prefill"}}
	vm := NewManager(&ui)

	err := vm.PrefillVolume(
		context.Background(),
		client,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"test-home-vol",
		image.ImageInfo{Tag: "agents-sandbox/runner-test:latest"},
		&ui,
	)
	require.NoError(t, err)
	require.Len(t, client.CreatedSandboxCalls, 1)
	name := client.CreatedSandboxCalls[0].Name
	require.True(t, strings.HasPrefix(name, "myproject-opencode-"), "name = %q", name)
	require.NotContains(t, name, naming.LegacyTaskPrefix)

	var cfg msbSdk.SandboxConfig
	for _, opt := range client.CreatedSandboxCalls[0].Opts {
		opt(&cfg)
	}
	require.Equal(t, "1", cfg.Labels[naming.LabelTask])
}

// TestCopySandboxNameIsPrefixFreeAndLabelled covers the second creation site.
func TestCopySandboxNameIsPrefixFreeAndLabelled(t *testing.T) {
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{CreatedSandbox: &msb.MockSandbox{Name_: "copy"}}
	vm := NewManager(&ui)

	err := vm.CopyVolume(
		context.Background(),
		client,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"old-home-vol",
		"new-home-vol",
		image.ImageInfo{Tag: "agents-sandbox/runner-test:latest"},
		&ui,
	)
	require.NoError(t, err)
	require.Len(t, client.CreatedSandboxCalls, 1)
	name := client.CreatedSandboxCalls[0].Name
	require.True(t, strings.HasPrefix(name, "myproject-opencode-"), "name = %q", name)
	require.NotContains(t, name, naming.LegacyTaskPrefix)

	var cfg msbSdk.SandboxConfig
	for _, opt := range client.CreatedSandboxCalls[0].Opts {
		opt(&cfg)
	}
	require.Equal(t, "1", cfg.Labels[naming.LabelTask])
}
