package vm

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// DefaultSandboxUser is the user hooks run as unless the hook opts into root.
const DefaultSandboxUser = "dev"

// RootUser is the VM root user, used to run privileged startup hooks.
const RootUser = "root"

// defaultHookInterpreter is the interpreter used for a startup hook that does
// not declare a shebang.
const defaultHookInterpreter = "/bin/sh"

// runStartupHooks runs each configured startup hook inside the VM via an
// interactive attach. A hook with a shebang is executed directly so the VM
// kernel resolves the interpreter; one without falls back to /bin/sh. Hook
// scripts are marked executable during provisioning, so no +x bit is required
// on the host source. The hook's HOME is set so scripts can rely on it. The
// agent waits for the attach to finish; a hook that must outlive the attach is
// responsible for daemonizing itself. Failures are logged, not fatal.
func runStartupHooks(ctx context.Context, sb msb.Sandbox, hooks []homeconfig.HookSpec, ui termio.UI) {
	for _, h := range hooks {
		user := DefaultSandboxUser
		home := "/home/dev"
		if h.Root {
			user = RootUser
			home = "/root"
		}
		command := h.Target
		var args []string
		if !h.HasShebang {
			command = defaultHookInterpreter
			args = []string{h.Target}
		}
		ui.Infof("running startup hook %s (as %s, via %s)", h.Target, user, command)
		if _, err := sb.AttachWith(
			ctx,
			command,
			args,
			msbSdk.WithAttachUser(user),
			msbSdk.WithAttachEnv(map[string]string{"HOME": home}),
		); err != nil {
			ui.Warnf("startup hook %s failed: %v", h.Target, err)
		}
	}
}

func setUpSandbox(
	ctx context.Context,
	k state.Key,
	sb msb.Sandbox,
	opts options.RunOptions,
	cfs *reprovision.ConfigFiles,
	ui termio.UI,
	restart bool,
	boot vmBoot,
	guestCWD string,
) (string, error) {
	a, ok := agent.Lookup(opts.Agent)
	if !ok {
		return "", fmt.Errorf("unknown agent %q", opts.Agent)
	}

	ui.Verbosef("expected config files: %v", cfs.Keys)

	// Provisioning (writing files) is idempotent and non-disruptive, so it is
	// always performed when there is config to write. Whether the daemon is
	// restarted to pick the config up is decided separately (the restart flag):
	// on a "keep" decision the files are still updated on disk so the next
	// daemon start sees them, without disturbing the running instance.
	provisioned := true
	if cfs.HasSnippets || len(cfs.HomeFiles) > 0 || len(cfs.Provisioned) > 0 || len(cfs.Mirror) > 0 ||
		len(cfs.Remove) > 0 {
		if provErr := reprovision.Provision(ctx, sb, cfs); provErr != nil {
			ui.Warnf("provision failed: %v (continuing)", provErr)
			provisioned = false
		}
	}

	// Record image provenance and the agent version baseline on a fresh boot.
	if boot.booted() {
		recordImageProvenance(ctx, k, a, sb, ui)
	}

	if restart {
		if provisioned {
			restartDaemons(ctx, a, sb, opts.ServeOnly, ui)
		}
		return ResolveTarget(ctx, a, sb, opts.Worktree, guestCWD, ui)
	}

	if len(cfs.Hooks) > 0 && boot.booted() {
		runStartupHooks(ctx, sb, cfs.Hooks, ui)
	}

	if dockerErr := startDockerdIfPresent(ctx, sb, ui); dockerErr != nil {
		return "", fmt.Errorf("docker startup: %w", dockerErr)
	}

	if daemonErr := ensureDaemon(ctx, a, opts.ServeOnly, false, sb, ui); daemonErr != nil {
		return "", daemonErr
	}

	return ResolveTarget(ctx, a, sb, opts.Worktree, guestCWD, ui)
}

// reconfigDecision is the resolved reconfiguration plan for a project VM. Named
// fields replace the former positional (recreate, restart, homeVol,
// serveHostPort) return, so a caller reads which decision it is holding.
//
//exhaustruct:ignore
type reconfigDecision struct {
	Recreate bool
	Restart  bool
	// HomeAction is the resolved home-volume disposition when the VM is
	// recreated for a changed image digest; nil when no home action applies.
	// applyReconfigHomeAction applies it.
	HomeAction    *volume.VolumeAction
	ServeHostPort int
}

// resolveReconfigDecision computes the reconfiguration decision: it reads the
// live VM, compares the persisted fingerprints, resolves the recreate/restart
// choice against any attached clients, and — when the VM will be recreated for
// a changed image — resolves the home-volume action. It performs no
// home-volume side effects; applyReconfigHomeAction applies the resolved
// action.
//
// It re-fetches the existing sandbox handle to read live VM files: no
// passed-in msb.Sandbox is available at the call site, which runs before
// ensureProjectVM creates one.
func resolveReconfigDecision(
	ctx context.Context,
	client msb.Client,
	vm *volume.Manager,
	k state.Key,
	opts options.RunOptions,
	imageInfo image.ImageInfo, homeVol string, hs state.HomeState,
	cfs *reprovision.ConfigFiles,
	ui termio.UI,
) (reconfigDecision, error) {
	handle, _, _ := findProjectVM(ctx, client, k)

	var curCfg *msbSdk.SandboxConfig
	var liveSb msb.Sandbox
	if handle != nil {
		var cfgErr error
		curCfg, cfgErr = handle.Config()
		if cfgErr != nil {
			ui.Verbosef("reading existing VM config failed: %v (continuing)", cfgErr)
		}
		applyLiveMounts(curCfg, handle, ui)
		var connectErr error
		liveSb, connectErr = handle.Connect(ctx)
		if connectErr != nil {
			ui.Verbosef("connecting to existing VM failed: %v (continuing)", connectErr)
		}
	}

	// Detect whether the project's image digest changed since the last run. The
	// home-volume prompt is deferred until the rebuild decision below confirms
	// we are actually switching to the new image.
	imageChanged := hs.ImageDigest != imageInfo.Digest
	if imageChanged {
		ui.Verbosef("image digest changed: %s -> %s", hs.ImageDigest, imageInfo.Digest)
	}

	var agentCfgChanged bool
	if liveSb != nil {
		vmData := reprovision.ReadVMConfig(ctx, liveSb, cfs.Keys, ui)
		agentCfgChanged = len(vmData) > 0 && !reprovision.AgentConfigEqual(cfs, vmData)
		if detachErr := liveSb.Detach(context.Background()); detachErr != nil {
			ui.Verbosef("failed to detach live sandbox handle: %v", detachErr)
		}
	}

	desiredEnv, desiredSecrets := envsecret.LoadEnvAndSecrets(ui)
	envHasChanged := state.EnvChanged(hs.EnvState, desiredEnv)
	secretsHasChanged := state.SecretsChanged(hs.SecretState, desiredSecrets)
	networkHasChanged := state.NetworkChanged(hs.NetworkState, opts.Network)
	mountsHaveChanged := state.MountsChanged(hs.MountState, opts.Mounts)

	plan := reconfig.PlanReconfig(
		curCfg,
		imageInfo.Ref,
		opts,
		reconfig.ChangeFlags{
			Image:       imageChanged,
			Env:         envHasChanged,
			Secrets:     secretsHasChanged,
			Network:     networkHasChanged,
			Mounts:      mountsHaveChanged,
			AgentConfig: agentCfgChanged,
		},
		homeVol,
	)
	otherClients := state.CountActiveClients(k)
	applyRecreate, applyRestart, err := reconfig.ResolveReconfig(ctx, ui, plan, otherClients, plan.Changes)
	if err != nil {
		return reconfigDecision{}, err
	}

	// ResolveReconfig reports a daemon restart only when the plan does not
	// recreate, so applyRestart already implies !applyRecreate && !plan.Recreate.
	decision := reconfigDecision{
		Recreate:      applyRecreate,
		Restart:       applyRestart,
		ServeHostPort: plan.ServeHostPort,
	}

	// The home-volume question only matters when we are actually switching to
	// the new image, so it is asked after the rebuild decision instead of up
	// front. When the rebuild is deferred (keep current VM) or no state exists
	// yet (fresh home already created), the prompt is skipped.
	if decision.Recreate && imageChanged {
		action := vm.ResolveHomeAction(ui, hs.ImageDigest, imageInfo.Digest)
		decision.HomeAction = &action
	}
	return decision, nil
}

// applyReconfigHomeAction applies the home-volume action the decision resolved
// and returns the home volume to mount. A nil HomeAction means no home-volume
// change applies, so the current volume is returned unchanged. A "quit" action
// aborts the run with an ExitError.
func applyReconfigHomeAction(
	ctx context.Context,
	client msb.Client,
	vm *volume.Manager,
	k state.Key,
	homeVol string,
	imageInfo image.ImageInfo,
	opts options.RunOptions,
	decision reconfigDecision,
	ui termio.UI,
) (string, error) {
	if decision.HomeAction == nil {
		return homeVol, nil
	}
	if *decision.HomeAction == volume.ActionQuit {
		ui.Infof("exiting as requested by user")
		return "", &ExitError{Code: 1}
	}
	newVol, err := vm.ApplyHomeAction(ctx, client, k, homeVol, imageInfo, *decision.HomeAction, opts, ui)
	if err != nil {
		return "", fmt.Errorf("apply home action: %w", err)
	}
	return newVol, nil
}

// applyLiveMounts fills cfg.Volumes with the mounts the persisted config
// records, which the SDK's Config() drops (specs/live-mount-readback.md). Until
// it runs, cfg.Volumes is empty and the size/quota/home detectors see nothing. A
// read failure is reported at verbose level and leaves cfg.Volumes untouched, so
// the decision degrades to the pre-#223 behaviour instead of aborting the run.
func applyLiveMounts(cfg *msbSdk.SandboxConfig, handle msb.SandboxHandle, ui termio.UI) {
	if cfg == nil {
		return
	}
	liveMounts, err := handle.ConfigMounts()
	if err != nil {
		ui.Verbosef("reading VM mounts failed: %v (continuing)", err)
		return
	}
	cfg.Volumes = liveMounts
}

// restartDaemons restarts the agent's daemon so an already-provisioned
// agent-config change is picked up. Env/secret changes are never routed
// here: they require a VM rebuild and are handled by the recreate path instead.
func restartDaemons(ctx context.Context, a agent.Agent, sb msb.Sandbox, serveOnly bool, ui termio.UI) {
	ui.Infof("%s serve restarting…", a.Name())
	// ensureDaemon no-ops for an agent without a DaemonProvider.
	if err := ensureDaemon(ctx, a, serveOnly, true, sb, ui); err != nil {
		ui.Warnf("daemon restart failed: %v (using existing)", err)
	}
}
