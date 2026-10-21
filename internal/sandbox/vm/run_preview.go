package vm

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// Preview describes what a run/shell would do, without doing it: the agent
// version, the image build decision, and the home volume a real run would use.
type Preview struct {
	AgentVersion          string
	ResolveVersionAtBuild bool
	ImageTag              string
	BuildImage            bool
	BuildReasons          []string
	HomeVolume            string
	CreateHome            bool
}

// PreviewSandbox resolves the read-only plan for a run: the agent version, the
// image build decision, and the home volume, using only local inspection. It
// builds no image, creates no volume, writes no state, starts no VM, prompts,
// and makes no network request. The agent version is the explicit pin, else the
// recorded version, else the version recovered from the existing image label —
// recovered for reporting only, never recorded.
func PreviewSandbox(ctx context.Context, opts options.RunOptions, ui termio.UI) (Preview, error) {
	cwd, err := resolveWorkspaceDir()
	if err != nil {
		return Preview{}, err
	}

	a, ok := agent.Lookup(opts.Agent)
	if !ok {
		return Preview{}, fmt.Errorf("unknown agent %q", opts.Agent)
	}
	// Read-only adoption: apply=false so the preview never moves state, but it
	// still resolves against the location a real run would find first (the
	// legacy slug while a migration is pending), matching the volume dry runs.
	adoption, err := AdoptLegacyIdentity(ctx, msb.Get(), git.ResolveIdentity(cwd), a.Name(), false, ui)
	if err != nil {
		return Preview{}, err
	}
	k := state.Key{Slug: adoption.Slug, Agent: a.Name()}

	version := opts.AgentVersion
	if version == "" {
		version = currentAgentVersion(k)
	}
	if version == "" {
		if recovered, ok := image.ExistingAgentVersion(ctx, k.Slug, k.Agent); ok {
			version = recovered
		}
	}

	img, err := image.PreviewImage(
		ctx, a, k.Slug,
		buildOptions(opts, version, opts.Rebuild, currentAgentSource(k) == agentSourceUser),
		ui,
	)
	if err != nil {
		return Preview{}, err
	}

	client := msb.Get()
	homeVolume, createHome := volume.PreviewHomeVolume(ctx, client, k, liveHomeVolume(ctx, client, k, ui))

	return Preview{
		AgentVersion:          version,
		ResolveVersionAtBuild: img.ResolveVersionAtBuild,
		ImageTag:              img.Tag,
		BuildImage:            img.NeedsBuild,
		BuildReasons:          img.Reasons,
		HomeVolume:            homeVolume,
		CreateHome:            createHome,
	}, nil
}
