// Package runoptions resolves the shared run/shell options from a command's
// flags and the resolved launcher config, with precedence flag > env > config >
// default.
package runoptions

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// Exported flag names that Build reads directly from the command.
const (
	FlagWorktree     = "worktree"
	FlagRebuild      = "rebuild"
	FlagDryRun       = "dry-run"
	FlagDryRunVM     = "dry-run-vm"
	FlagServeOnly    = "serve-only"
	FlagRoot         = "root"
	FlagAgent        = "agent"
	FlagAgentVersion = "agent-version"
	FlagNetwork      = "network"
	FlagDNSServers   = "dns"
	FlagNotify       = "notify"
	// FlagOpenLinks is the --open-links flag name, shared with the config key.
	FlagOpenLinks = viperconfig.KeyOpenLinks
)

// defaultAgentName is the fallback agent used when --agent is not provided.
const defaultAgentName = agent.DefaultAgentName

// notifyEnvVar is the primary environment variable override for --notify.
const notifyEnvVar = viperconfig.EnvPrefix + "_NOTIFY"

// legacyNotifyEnvVar is the deprecated --notify override.
const legacyNotifyEnvVar = viperconfig.LegacyEnvPrefix + "_NOTIFY"

// notifyEnvOverride returns the effective --notify environment override and the
// variable name it came from, preferring AGENTS_SANDBOX_NOTIFY over the
// deprecated OPENCODE_SANDBOX_NOTIFY. Both are empty when neither is set.
func notifyEnvOverride() (string, string) {
	for _, candidate := range []string{notifyEnvVar, legacyNotifyEnvVar} {
		if v := os.Getenv(candidate); v != "" {
			return v, candidate
		}
	}
	return "", ""
}

// Build resolves the shared run/shell options from the command's flags and the
// resolved launcher config, with precedence flag > env > config > default. A
// nil config resolves the options from the command's flags alone, still applying
// the agent default and the notify env override but leaving the config-derived
// fields (policy, sizes, mounts, provisioning) at their zero values.
func Build(cfg *viperconfig.Config, cmd *cobra.Command, ui termio.UI) (options.RunOptions, error) {
	opts, err := resolveFlags(cmd, ui)
	if err != nil {
		return options.RunOptions{}, err
	}

	if err = resolveAgentAndValidate(cfg, cmd, &opts); err != nil {
		return options.RunOptions{}, err
	}

	if err = resolveNotify(cfg, cmd, ui, &opts); err != nil {
		return options.RunOptions{}, err
	}

	if cfg != nil {
		opts.CPUs = cfg.CPUs
		opts.Memory = cfg.Memory
		opts.TmpSize = cfg.TmpSize
		opts.DiskSize = cfg.DiskSize
		opts.WorkspaceQuota = cfg.WorkspaceQuota
		opts.ReapPolicy = options.NewReapPolicy(cfg.AutoStopOnActiveSessions, cfg.AutoStopMaxSessionRetries)
		opts.IdleTimeout = cfg.IdleTimeout()
		opts.Docker = cfg.Docker
		opts.Mounts, err = mounts.ResolveBindMounts(cfg.Mounts)
		if err != nil {
			return options.RunOptions{}, err
		}
		provisionHostConfig := cfg.ProvisionHostConfig
		opts.ProvisionHostConfig = &provisionHostConfig
		opts.OpenLinks = cfg.OpenLinks
	} else {
		opts.OpenLinks, _ = cmd.Flags().GetBool(FlagOpenLinks)
	}

	applyDeprecatedDindFlag(cmd, &opts)

	if err := resolveNetwork(cfg, cmd, &opts); err != nil {
		return options.RunOptions{}, err
	}

	if err := resolveSizes(&opts); err != nil {
		return options.RunOptions{}, err
	}

	return opts, nil
}

// applyDeprecatedDindFlag folds the deprecated --dind flag into opts.Docker.
// The canonical --docker flag wins when both are set. The deprecated flag is
// not bound to viper, so it is resolved here. Cobra emits the deprecation
// warning for the flag itself.
func applyDeprecatedDindFlag(cmd *cobra.Command, opts *options.RunOptions) {
	dindFlag := cmd.Flags().Lookup(viperconfig.KeyDind)
	if dindFlag == nil || !dindFlag.Changed {
		return
	}
	if dockerFlag := cmd.Flags().Lookup(viperconfig.KeyDocker); dockerFlag != nil && dockerFlag.Changed {
		return
	}
	dind, _ := cmd.Flags().GetBool(viperconfig.KeyDind)
	opts.Docker = dind
}

// resolveFlags reads the direct-run flags off the command and returns the
// starting RunOptions, resolving the worktree spec and the dry-run auto-enable.
func resolveFlags(cmd *cobra.Command, ui termio.UI) (options.RunOptions, error) {
	opts := options.RunOptions{}
	rawWorktree, _ := cmd.Flags().GetString(FlagWorktree)
	worktree, err := options.ResolveWorktreeSpec(rawWorktree)
	if err != nil {
		return options.RunOptions{}, err
	}
	opts.Worktree = worktree
	opts.Rebuild, _ = cmd.Flags().GetBool(FlagRebuild)
	opts.AgentVersion, _ = cmd.Flags().GetString(FlagAgentVersion)
	opts.DryRun, _ = cmd.Flags().GetBool(FlagDryRun)
	opts.DryRunVM, _ = cmd.Flags().GetBool(FlagDryRunVM)
	if opts.DryRun {
		opts.DryRunVM = true
		ui.Verbosef("dry-run-vm: auto-enabled (--dry-run)")
	}
	opts.ServeOnly, _ = cmd.Flags().GetBool(FlagServeOnly)
	if cmd.Flags().Lookup(FlagRoot) != nil {
		opts.Root, _ = cmd.Flags().GetBool(FlagRoot)
	}
	return opts, nil
}

// resolveAgentAndValidate resolves the agent (default > config > flag) and
// rejects unknown names and unsupported --worktree/--serve-only combinations.
func resolveAgentAndValidate(cfg *viperconfig.Config, cmd *cobra.Command, opts *options.RunOptions) error {
	opts.Agent = defaultAgentName
	if cfg != nil && cfg.Agent != "" {
		opts.Agent = cfg.Agent
	}
	if name, _ := cmd.Flags().GetString(FlagAgent); name != "" && cmd.Flags().Changed(FlagAgent) {
		opts.Agent = name
	}
	if !slices.Contains(agent.Names(), opts.Agent) {
		return fmt.Errorf(
			"unknown agent %q: must be one of %s",
			opts.Agent,
			strings.Join(agent.Names(), ", "),
		)
	}
	a, _ := agent.Lookup(opts.Agent)
	if opts.Worktree.Name != "" {
		if _, ok := agent.AsWorktreeProvider(a); !ok {
			return fmt.Errorf("--worktree is not supported by agent %q", a.Name())
		}
	}
	if opts.ServeOnly {
		if _, ok := agent.AsDaemonProvider(a); !ok {
			return fmt.Errorf("--serve-only is not supported by agent %q", a.Name())
		}
	}
	return nil
}

// resolveNotify resolves the effective notify config (flag > env > config),
// then validates agent support, warning or failing for agents without a daemon.
func resolveNotify(cfg *viperconfig.Config, cmd *cobra.Command, ui termio.UI, opts *options.RunOptions) error {
	nc, err := resolveNotifyConfig(cfg, cmd)
	if err != nil {
		return err
	}
	opts.Notify = nc
	if nc.Active() {
		a, _ := agent.Lookup(opts.Agent)
		if _, ok := agent.AsDaemonProvider(a); !ok {
			if override, _ := notifyEnvOverride(); cmd.Flags().Changed(FlagNotify) || override != "" {
				return fmt.Errorf(
					"--notify is not supported by agent %q (no daemon/event stream)",
					a.Name(),
				)
			}
			ui.Warnf("notifications not supported by agent %q (no daemon/event stream); ignoring", a.Name())
			opts.Notify = notify.Config{Audio: notify.AudioOff}
		}
	}
	return nil
}

// resolveNotifyConfig resolves the effective notify config with precedence
// flag > env > config, then validates the value.
func resolveNotifyConfig(cfg *viperconfig.Config, cmd *cobra.Command) (notify.Config, error) {
	resolved := notify.Config{Audio: notify.AudioOff}
	if cfg != nil {
		resolved = cfg.Notify
		if resolved.Audio == "" {
			resolved.Audio = notify.AudioOff
		}
	}
	if raw, name := notifyEnvOverride(); raw != "" {
		override, err := notify.ParseOverride(raw)
		if err != nil {
			return notify.Config{}, fmt.Errorf("%s: %w", name, err)
		}
		resolved = notify.ApplyOverride(resolved, override)
	}
	if raw, _ := cmd.Flags().GetString(FlagNotify); cmd.Flags().Changed(FlagNotify) && raw != "" {
		override, err := notify.ParseOverride(raw)
		if err != nil {
			return notify.Config{}, err
		}
		resolved = notify.ApplyOverride(resolved, override)
	}
	return resolved, nil
}

// resolveNetwork resolves the egress policy: the --network flag wins over the
// resolved config; --dns then replaces the DNS servers while keeping any
// profile and egress lists.
func resolveNetwork(cfg *viperconfig.Config, cmd *cobra.Command, opts *options.RunOptions) error {
	if raw, _ := cmd.Flags().GetStringSlice(FlagNetwork); len(raw) > 0 {
		profiles, err := network.ParseProfiles(raw)
		if err != nil {
			return err
		}
		opts.Network = network.Policy{Profiles: profiles, EgressAllow: nil, EgressDeny: nil, DNSServers: nil}
	} else if cfg != nil {
		opts.Network = cfg.Network.Effective()
	}
	if dns, _ := cmd.Flags().GetStringSlice(FlagDNSServers); len(dns) > 0 {
		opts.Network.DNSServers = dns
	}
	return nil
}

// resolveSizes validates the resolved tmp-size, disk-size, and workspace-quota
// values against the shared size grammar.
func resolveSizes(opts *options.RunOptions) error {
	if opts.TmpSize != "" {
		if _, ok := options.ParseMemoryOK(opts.TmpSize); !ok {
			return fmt.Errorf(
				"invalid --tmp-size %q: expected a size like 4G, 512M, or 2048",
				opts.TmpSize,
			)
		}
	}
	if opts.DiskSize != "" {
		if _, ok := options.ParseMemoryOK(opts.DiskSize); !ok {
			return fmt.Errorf(
				"invalid --disk-size %q: expected a size like 16G, 512M, or 4096",
				opts.DiskSize,
			)
		}
	}
	if opts.WorkspaceQuota != "" {
		if _, ok := options.ParseMemoryOK(opts.WorkspaceQuota); !ok {
			return fmt.Errorf(
				"invalid --workspace-quota %q: expected a size like 16G, 512M, or 4096",
				opts.WorkspaceQuota,
			)
		}
	}
	return nil
}
