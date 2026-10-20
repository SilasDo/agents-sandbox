package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/humanize"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

type volumeOpFunc func(context.Context, state.Key, string, image.ImageInfo, bool, bool, termio.UI) error

const (
	colName    = "NAME"
	colStatus  = "STATUS"
	colImage   = "IMAGE"
	colCreated = "CREATED"
	colKind    = "KIND"
	colSize    = "SIZE"
	colRef     = "REFERENCE"
	colDigest  = "DIGEST"
)

// sandboxListHeaders returns the column order for sandbox lists. Matches msb:
// NAME IMAGE STATUS CREATED.
func sandboxListHeaders() []string {
	return []string{colName, colImage, colStatus, colCreated}
}

func imageListHeaders() []string {
	return []string{colRef, colDigest, colSize, colCreated}
}

func volumeListHeaders() []string {
	return []string{colName, colKind, colSize, colCreated}
}

// volumeSize renders the SIZE column to match msb: disk volumes show
// capacity, directory volumes show quota, and "-" when unavailable.
func volumeSize(kind string, q *uint32, c *uint64) string {
	if kind == "disk" {
		if c != nil {
			return humanize.FormatBytes(*c)
		}
		return "-"
	}
	if q != nil {
		return humanize.FormatBytes(uint64(*q) * 1024 * 1024)
	}
	return "-"
}

// truncateDigest shortens a full manifest digest to the short form msb uses in
// image list output: the "sha256:" prefix followed by the first 12 hex chars.
func truncateDigest(digest string) string {
	const shortLen = len("sha256:") + 12
	if len(digest) <= shortLen {
		return digest
	}
	return digest[:shortLen]
}

type jsonVolume struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Size      string `json:"size"`
	CreatedAt string `json:"created"`
}

func volumeJSON(infos []volume.VolumeInfo) any {
	out := make([]jsonVolume, 0, len(infos))
	for _, v := range infos {
		out = append(out, jsonVolume{
			Name:      v.Name,
			Kind:      v.Kind,
			Size:      volumeSize(v.Kind, v.QuotaMiB, v.CapacityBytes),
			CreatedAt: v.CreatedAt,
		})
	}
	return out
}

func buildVolumeOpsCmd(
	ui termio.UI,
	name string,
	fn volumeOpFunc,
	short, rmFlag, rmHelp string,
	rmVar *bool,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   name,
		Args:  cobra.MaximumNArgs(1),
		Short: short,
		RunE: func(c *cobra.Command, args []string) error {
			if !doctor.CheckAll(c.Context(), ui) {
				return errors.New("preflight failed")
			}
			dryRun, _ := c.Flags().GetBool(flagDryRun)
			a, err := resolveAgentFlag(c)
			if err != nil {
				return err
			}
			// Adopt like PrepareSandbox does, and use the adopted slug for both
			// the image and the state key. Never re-derive it from git: adoption
			// may have just moved the state, and a dry run deliberately returns
			// the legacy repo slug while the migration stays pending.
			adoption, err := sandbox.AdoptLegacyIdentity(
				c.Context(), msb.Get(), git.ResolveIdentity("."), a.Name(), !dryRun, ui,
			)
			if err != nil {
				return err
			}
			docker := false
			if r := resolverFromContext(c.Context()); r != nil {
				docker = r.Docker()
			}
			info, err := image.EnsureImage(
				c.Context(),
				a,
				adoption.Slug,
				image.BuildOptions{Force: false, AgentVersion: "", UserProvided: false, Docker: docker},
				ui,
			)
			if err != nil {
				return fmt.Errorf("ensure image: %w", err)
			}
			var volName string
			if len(args) > 0 {
				volName = args[0]
			}
			return fn(
				c.Context(),
				state.Key{Slug: adoption.Slug, Agent: a.Name()},
				volName,
				info,
				*rmVar,
				dryRun,
				ui,
			)
		},
	}
	cmd.Flags().BoolVar(rmVar, rmFlag, false, rmHelp)
	cmd.Flags().Bool(flagDryRun, false, "Show what would be done")
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile")
	// --rebuild was dropped from these volume ops without a deprecation cycle
	// (migrate/reset shipped it in 0.5.0). Restore it on all three as a hidden
	// no-op for one cycle: they ensure the runner image but never force a clean
	// rebuild.
	cmd.Flags().Bool(flagRebuild, false, "Deprecated no-op; run 'agents-sandbox build' to rebuild the runner image")
	_ = cmd.Flags().MarkDeprecated(flagRebuild, "run 'agents-sandbox build' to rebuild the runner image")
	return cmd
}

func buildDoctorCmd(ui termio.UI) *cobra.Command {
	return &cobra.Command{
		Use:   cmdDoctor,
		Args:  cobra.NoArgs,
		Short: "Check prerequisites",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !doctor.CheckAll(cmd.Context(), ui) {
				return errors.New("preflight failed")
			}
			ui.Info("doctor: all checks passed")
			return nil
		},
	}
}

func buildListCmd(ui termio.UI) *cobra.Command {
	var (
		labelsStr []string
		limit     uint32
		running   bool
		stopped   bool
		namesOnly bool
		format    string
	)
	cmd := &cobra.Command{
		Use:     cmdList,
		Aliases: cmdListAliases,
		Args:    cobra.NoArgs,
		Short:   "List sandboxes for this host",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox list",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := resolveListOutput(format, ui.Quiet() || namesOnly)
			if err != nil {
				return err
			}
			labels, err := parseLabelFlags(labelsStr)
			if err != nil {
				return err
			}
			var lim *uint32
			if cmd.Flags().Changed(flagLimit) && limit != 0 {
				lim = &limit
			}
			opt := sandbox.ListOption{
				Labels:      labels,
				Limit:       lim,
				RunningOnly: running,
				StoppedOnly: stopped,
			}
			sandboxes, err := sandbox.ListSandboxes(cmd.Context(), opt)
			if err != nil {
				return err
			}
			return printList(ui, mode, sandboxes, "No sandboxes found.", sandboxListHeaders(), sandboxJSON,
				func(s sandbox.Info) string { return s.Name },
				func(s sandbox.Info) string { return s.Image },
				func(s sandbox.Info) string { return termio.StyleStatus(s.Status) },
				func(s sandbox.Info) string { return s.CreatedAt },
			)
		},
	}
	cmd.Flags().BoolVar(&namesOnly, pFlagNames, false, "Print only sandbox names")
	_ = cmd.Flags().MarkDeprecated(pFlagNames, "use --quiet")
	cmd.Flags().
		StringArrayVar(&labelsStr, flagLabel, nil, "Only show sandboxes carrying this label KEY=VALUE (repeatable, all must match)")
	cmd.Flags().Uint32Var(&limit, flagLimit, 0, "Limit the number of sandboxes shown")
	cmd.Flags().BoolVar(&running, flagRunning, false, "Show only running sandboxes")
	cmd.Flags().BoolVar(&stopped, flagStopped, false, "Show only stopped sandboxes")
	cmd.Flags().StringVar(&format, flagFormat, "", "Output format (json)")
	return cmd
}

func parseLabelFlags(values []string) (map[string]string, error) {
	labels := make(map[string]string, len(values))
	for _, v := range values {
		key, val, ok := strings.Cut(v, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid label %q: must be KEY=VALUE", v)
		}
		labels[key] = val
	}
	return labels, nil
}

type jsonSandbox struct {
	Name    string            `json:"name"`
	Status  string            `json:"status"`
	Image   string            `json:"image"`
	Created time.Time         `json:"created"`
	Updated time.Time         `json:"updated"`
	Labels  map[string]string `json:"labels"`
}

func sandboxJSON(infos []sandbox.Info) any {
	out := make([]jsonSandbox, 0, len(infos))
	for _, s := range infos {
		out = append(out, jsonSandbox{
			Name:    s.Name,
			Status:  s.Status,
			Image:   s.Image,
			Created: s.CreatedAtRaw,
			Updated: s.UpdatedAtRaw,
			Labels:  s.Labels,
		})
	}
	return out
}

func buildConfigCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdConfig,
		Aliases: cmdConfigAliases,
		Short:   "Inspect agent and home configuration",
	}

	cmd.AddCommand(buildConfigAgentCmd(ui))

	cmd.AddCommand(&cobra.Command{
		Use:   cmdHome,
		Args:  cobra.NoArgs,
		Short: "List home-file mappings from the config home: key",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, _ := agent.Lookup("")
			r := resolverFromContext(cmd.Context())
			layers, has := r.Home()
			if !has {
				ui.Out("No home configuration found.")
				return nil
			}
			pairs, err := homeconfig.DescribeLayers(
				layers,
				"/home/dev",
				reservedHomeConfigTargets(a, "/home/dev"),
			)
			if err != nil {
				return err
			}
			if len(pairs) == 0 {
				ui.Out("No home mappings.")
				return nil
			}
			for _, p := range pairs {
				ui.Outf("%s  <-  %s", p[0], p[1])
			}
			return nil
		},
	})

	return cmd
}

// reservedHomeConfigTargets returns the home-relative VM paths the agent
// reserves for its merged config, or nil for an agent without a ConfigMerger.
func reservedHomeConfigTargets(a agent.Agent, home string) []string {
	cm, ok := agent.AsConfigMerger(a)
	if !ok {
		return nil
	}
	rel, err := filepath.Rel(home, cm.VMConfigPath(home))
	if err != nil {
		return nil
	}
	return []string{rel}
}

// buildConfigAgentCmd returns the config agent subcommand, which prints the
// agent's merged snippet config, the host drop-in files, and the verbatim
// mirror files that provisioning would copy into the VM.
func buildConfigAgentCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdAgent,
		Args:  cobra.MaximumNArgs(1),
		Short: "Show the merged agent config and the host files provisioned into the VM",
		RunE: func(c *cobra.Command, args []string) error {
			a, err := configAgentFor(c, args)
			if err != nil {
				return err
			}
			provision := provisionHostConfigEnabled(c)
			hostHome, _ := configpaths.Get().UserHomeDir()
			merged, sources, hostFiles, mirrorFiles, source, err := reprovision.Describe(
				a,
				hostHome,
				reprovision.VMHomeDir,
				ui,
				provision,
			)
			if err != nil {
				return err
			}
			printConfigAgent(ui, a, source, merged, sources, hostFiles, mirrorFiles, provision)
			return nil
		},
	}
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile")
	return cmd
}

// configAgentFor resolves the agent for the config agent command, or errors if
// the resolved name is unknown.
func configAgentFor(c *cobra.Command, args []string) (agent.Agent, error) {
	name, err := resolveConfigAgentName(c, args)
	if err != nil {
		return nil, err
	}
	a, ok := agent.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q: must be one of %s", name, strings.Join(agent.Names(), ", "))
	}
	return a, nil
}

// provisionHostConfigEnabled reports whether host config provisioning is on,
// defaulting to true when no resolver is present.
func provisionHostConfigEnabled(c *cobra.Command) bool {
	if r := resolverFromContext(c.Context()); r != nil {
		return r.ProvisionHostConfig()
	}
	return true
}

// printConfigAgent renders the merged config, mirror files, and drop-in host
// files that provisioning would copy into the VM.
func printConfigAgent(
	ui termio.UI,
	a agent.Agent,
	source reprovision.ConfigSource,
	merged []byte,
	sources []string,
	hostFiles []reprovision.HostFile,
	mirrorFiles []reprovision.MirrorFile,
	provision bool,
) {
	userDir, projectDir := reprovision.SourceDirs(source)
	sourceKind := "primary"
	if source.Fallback != "" {
		sourceKind = fmt.Sprintf("legacy fallback %q", source.Fallback)
	}
	ui.Outf("agent: %s", a.Name())
	ui.Outf("config dir: user=%q  project=%q  (%s)", userDir, projectDir, sourceKind)
	if len(sources) == 0 {
		ui.Out("No snippet files found; no merged config is provisioned.")
	} else {
		ui.Out("merged files:")
		for _, src := range sources {
			ui.Outf("  %s", src)
		}
		ui.Out("merged agent config:")
		for line := range strings.SplitSeq(string(merged), "\n") {
			ui.Outf("  %s", line)
		}
	}
	if len(mirrorFiles) > 0 {
		ui.Out("mirror files:")
		for _, mf := range mirrorFiles {
			ui.Outf("  %s  ->  %s", mf.HostPath, mf.VMPath)
		}
	}
	ui.Outf("host files (drop-in, provision-host-config=%v):", provision)
	for _, hf := range hostFiles {
		status := "not merged"
		if hf.Merged {
			status = "merged"
		}
		ui.Outf("  %-9s %s  ->  %s", status, hf.HostPath, hf.VMPath)
	}
}

// resolveConfigAgentName resolves the agent name for the config agent command:
// --agent flag, then the positional arg, then the resolver, then the default.
// An explicit --agent that conflicts with a positional arg is ambiguous.
func resolveConfigAgentName(c *cobra.Command, args []string) (string, error) {
	name := defaultAgentName
	flagName, _ := c.Flags().GetString(flagAgent)
	if c.Flags().Changed(flagAgent) {
		if len(args) > 0 && args[0] != flagName {
			return "", fmt.Errorf("ambiguous agent: --agent %q conflicts with positional %q", flagName, args[0])
		}
		return flagName, nil
	}
	if len(args) > 0 {
		return args[0], nil
	}
	if r := resolverFromContext(c.Context()); r != nil {
		if rn := r.Agent(); rn != "" {
			return rn, nil
		}
	}
	return name, nil
}

// resolveBuildDocker returns the effective docker switch for a build: the
// --docker flag when set, else the deprecated --dind alias when set, else the
// configured resolver value (false when absent).
func resolveBuildDocker(cmd *cobra.Command) bool {
	if cmd.Flags().Changed(flagDocker) {
		docker, _ := cmd.Flags().GetBool(flagDocker)
		return docker
	}
	if cmd.Flags().Changed(flagDind) {
		dind, _ := cmd.Flags().GetBool(flagDind)
		return dind
	}
	if r := resolverFromContext(cmd.Context()); r != nil {
		return r.Docker()
	}
	return false
}

func buildBuildCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdBuild,
		Args:  cobra.NoArgs,
		Short: "Build or rebuild the runner image",
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool(flagRebuild)
			dryRun, _ := cmd.Flags().GetBool(flagDryRun)
			openCodeVersion, _ := cmd.Flags().GetString(flagAgentVersion)
			if !cmd.Flags().Changed(flagAgentVersion) && cmd.Flags().Changed(flagOpenCodeVersion) {
				openCodeVersion, _ = cmd.Flags().GetString(flagOpenCodeVersion)
			}
			a, err := resolveAgentFlag(cmd)
			if err != nil {
				return err
			}
			// Check the Docker prerequisite before adopting, like run/shell and
			// the volume ops do: the check does not need the slug, and a failing
			// build must not destroy the legacy VM and move the state first.
			if !dryRun {
				if dockerErr := doctor.CheckDocker(cmd.Context()); dockerErr != nil {
					return fmt.Errorf("docker not available: %w", dockerErr)
				}
			}
			// Adopt like PrepareSandbox does, and use the adopted slug for the
			// image tag. It runs before the dry-run gate so the migration notice
			// prints; a dry run passes apply=false, so the migration stays
			// pending and adoption returns the legacy repo slug.
			adoption, err := sandbox.AdoptLegacyIdentity(
				cmd.Context(), msb.Get(), git.ResolveIdentity("."), a.Name(), !dryRun, ui,
			)
			if err != nil {
				return err
			}
			if dryRun {
				ui.Infof("dry-run: Would build runner image")
				return nil
			}
			return image.Build(cmd.Context(), a, adoption.Slug, image.BuildOptions{
				Force: force, AgentVersion: openCodeVersion, UserProvided: false, Docker: resolveBuildDocker(cmd),
			}, ui)
		},
	}
	cmd.Flags().BoolP(flagRebuild, flagRebuild[:1], false, "Force a clean rebuild")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Dry run without building")
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile to build")
	registerDockerFlags(cmd)
	cmd.Flags().
		String(flagAgentVersion, "", "Pin the agent version baked into the runner image (default: latest release)")
	cmd.Flags().
		String(flagOpenCodeVersion, "", "Deprecated alias for --agent-version")
	_ = cmd.Flags().MarkDeprecated(flagOpenCodeVersion, "use --agent-version instead")

	cmd.AddCommand(buildDockerfileCmd(ui))
	return cmd
}

// buildDockerfileCmd returns the "build dockerfile" subcommand, which prints
// the runner Dockerfile exactly as it would be built without invoking docker.
func buildDockerfileCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdDockerfile,
		Args:  cobra.NoArgs,
		Short: "Print the runner Dockerfile as it would be built",
		RunE: func(cmd *cobra.Command, _ []string) error {
			image.WarnIgnoredProjectConfig(ui)
			a, err := resolveAgentFlag(cmd)
			if err != nil {
				return err
			}
			agentVersion, _ := cmd.Flags().GetString(flagAgentVersion)
			out, err := image.RenderProjectDockerfile(a, agentVersion, resolveBuildDocker(cmd))
			if err != nil {
				return err
			}
			if _, err := ui.StdOut().Write(out); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile to build")
	cmd.Flags().
		String(flagAgentVersion, "", "Pin the agent version rendered into the preview (default: no pinned default)")
	registerDockerFlags(cmd)
	return cmd
}

func buildImageCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdImage,
		Aliases: cmdImageAliases,
		Args:    cobra.NoArgs,
		Short:   "Manage runner images",
	}
	var format string
	listCmd := &cobra.Command{
		Use:     cmdList,
		Args:    cobra.NoArgs,
		Aliases: cmdListAliases,
		Short:   "List cached runner images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := resolveListOutput(format, ui.Quiet())
			if err != nil {
				return err
			}
			images, err := image.ListImages(cmd.Context())
			if err != nil {
				return err
			}
			return printList(ui, mode, images, "No images found.", imageListHeaders(), imageJSON,
				func(i image.Info) string { return i.Reference },
				func(i image.Info) string { return truncateDigest(i.Digest) },
				func(i image.Info) string { return i.Size },
				func(i image.Info) string { return i.CreatedAt },
			)
		},
	}
	listCmd.Flags().StringVar(&format, flagFormat, "", "Output format (json)")
	cmd.AddCommand(listCmd)
	cmd.AddCommand(buildImageInspectCmd(ui))
	cmd.AddCommand(buildImageRemoveCmd(ui))
	cmd.AddCommand(buildBuildCmd(ui))
	cmd.AddCommand(buildImagePruneCmd(ui))
	return cmd
}

// buildImageRemoveCmd builds `image remove <ref>...`, which deletes cached
// runner images by reference. It refuses when an image is still in use unless
// --force is set, and prompts unless --yes is set.
func buildImageRemoveCmd(ui termio.UI) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     cmdRemove + " <ref>...",
		Aliases: cmdRemoveAliases,
		Args:    cobra.MinimumNArgs(1),
		Short:   "Remove cached runner images by reference",
		Long: "Remove the named cached runner images. Refuses when an image is still in use by a " +
			"project's state or a live sandbox; pass --force to remove it anyway. Prompts unless --yes is set.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return image.RemoveImages(cmd.Context(), args, force, ui)
		},
	}
	cmd.Flags().
		BoolVarP(&force, flagForce, flagForce[:1], false, "Remove even when a project or sandbox still references the image")
	return cmd
}

type jsonImage struct {
	Reference string `json:"reference"`
	Digest    string `json:"digest"`
	Size      string `json:"size"`
	Created   string `json:"created"`
}

func imageJSON(infos []image.Info) any {
	out := make([]jsonImage, 0, len(infos))
	for _, i := range infos {
		out = append(out, jsonImage{
			Reference: i.Reference,
			Digest:    i.Digest,
			Size:      i.Size,
			Created:   i.CreatedAt,
		})
	}
	return out
}

func buildVolumeCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdVolume,
		Aliases: cmdVolumeAliases,
		Short:   "Manage home volumes",
	}
	var format string
	listCmd := &cobra.Command{
		Use:     cmdList,
		Aliases: cmdListAliases,
		Args:    cobra.NoArgs,
		Short:   "List managed volumes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := resolveListOutput(format, ui.Quiet())
			if err != nil {
				return err
			}
			volumes, err := volume.ListVolumes(cmd.Context())
			if err != nil {
				return err
			}
			return printList(ui, mode, volumes, "No volumes found.", volumeListHeaders(), volumeJSON,
				func(v volume.VolumeInfo) string { return v.Name },
				func(v volume.VolumeInfo) string { return v.Kind },
				func(v volume.VolumeInfo) string { return volumeSize(v.Kind, v.QuotaMiB, v.CapacityBytes) },
				func(v volume.VolumeInfo) string { return v.CreatedAt },
			)
		},
	}
	listCmd.Flags().StringVar(&format, flagFormat, "", "Output format (json)")
	cmd.AddCommand(listCmd)
	cmd.AddCommand(buildVolumeInspectCmd(ui))

	var migrateRmOld bool
	cmd.AddCommand(
		buildVolumeOpsCmd(
			ui,
			cmdMigrate,
			volume.CmdMigrate,
			"Migrate: create new volume, copy files from existing volume on top",
			flagRemove,
			"Remove the old home volume after migration",
			&migrateRmOld,
		),
	)

	var resetRmOld bool
	cmd.AddCommand(
		buildVolumeOpsCmd(
			ui,
			cmdReset,
			volume.CmdReset,
			"Reset: create new volume from image, ",
			flagRemove,
			"Remove the old home volume after reset",
			&resetRmOld,
		),
	)

	var editRmOld bool
	cmd.AddCommand(
		buildVolumeOpsCmd(
			ui,
			cmdEdit,
			volume.CmdEdit,
			"Edit: create new volume alongside old one for manual transfer",
			flagRemove,
			"Remove the old home volume after editing",
			&editRmOld,
		),
	)

	cmd.AddCommand(buildVolumeRemoveCmd(ui))
	cmd.AddCommand(buildVolumePruneCmd(ui))
	return cmd
}

// buildVolumeRemoveCmd builds `volume remove <name>...`, which deletes home
// volumes by name. It refuses when a project still references a volume unless
// --force is set, and prompts unless --yes is set.
func buildVolumeRemoveCmd(ui termio.UI) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     cmdRemove + " <name>...",
		Aliases: cmdRemoveAliases,
		Args:    cobra.MinimumNArgs(1),
		Short:   "Remove home volumes by name",
		Long: "Remove the named home volumes. Refuses when a volume is still referenced by a " +
			"project's state; pass --force to remove it anyway. Prompts unless --yes is set.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return volume.RemoveVolumes(cmd.Context(), args, force, ui)
		},
	}
	cmd.Flags().
		BoolVarP(&force, flagForce, flagForce[:1], false, "Remove even when a project still references the volume")
	return cmd
}

func buildSandboxCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdSandbox,
		Aliases: cmdSandboxAliases,
		Args:    cobra.NoArgs,
		Short:   "Manage sandboxes",
	}
	cmd.AddCommand(buildListCmd(ui))
	cmd.AddCommand(buildShellCmd(ui))
	cmd.AddCommand(buildRunCmd(ui))
	cmd.AddCommand(buildStopCmd(ui))
	cmd.AddCommand(buildKillCmd(ui))
	cmd.AddCommand(buildSandboxInspectCmd(ui))
	cmd.AddCommand(buildSandboxRemoveCmd(ui))
	cmd.AddCommand(buildSandboxPruneCmd(ui))
	return cmd
}

// buildSandboxRemoveCmd builds `sandbox remove <name>`, which removes a project
// VM by name. It refuses a running VM unless --force is set (which stops it
// first); the persisted project state is left intact so the next run reuses the
// recorded home volume.
func buildSandboxRemoveCmd(ui termio.UI) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     cmdRemove + " <name>",
		Aliases: cmdRemoveAliases,
		Args:    cobra.ExactArgs(1),
		Short:   "Remove a project sandbox VM by name",
		Long: "Remove an agents-sandbox project VM. Refuses a running VM unless --force is set, " +
			"which stops it first. The project's persisted state is left intact. Prompts unless --yes is set.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sandbox.RemoveSandbox(cmd.Context(), args[0], force, ui)
		},
	}
	cmd.Flags().BoolVarP(&force, flagForce, flagForce[:1], false, "Remove a running VM (stopping it first)")
	return cmd
}

// confirmPrune gates a destructive prune behind confirmation. A dry-run only
// previews, so it is authorized without prompting. The global --yes flag
// authorizes non-interactively through ui.Confirm's assume-yes path; a
// non-interactive UI without --yes fails fast via ui.Confirm rather than
// blocking.
func confirmPrune(ui termio.UI, dryRun bool) error {
	if dryRun {
		return nil
	}
	ok, err := ui.Confirm("Prune stale runtime objects?", false)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("prune cancelled")
	}
	return nil
}

func buildPruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune stale VMs, volumes, and images",
		Annotations: map[string]string{
			annotationAlsoAs: cmdStorage + " " + cmdPrune,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			age, dryRun, err := resolvePruneFlags(cmd)
			if err != nil {
				return err
			}
			mode, err := resolvePruneOutputMode(cmd, ui)
			if err != nil {
				return err
			}
			if err = confirmPrune(ui, dryRun); err != nil {
				return err
			}
			report, pruneErr := pruning.Prune(cmd.Context(), age, dryRun, ui)
			if report != nil {
				renderPruneReport(ui, mode,
					func() { pruning.RenderPruneSummary(ui, report, dryRun) },
					newPruneReportJSON(report, dryRun))
			}
			return pruneErr
		},
	}
	setPruneFlags(cmd)
	return cmd
}

// resolvePruneAge returns the effective prune threshold for a manual prune:
// --older-than (or the deprecated --age) if set, else manual-prune-age from
// config, else the 7d default.
func resolvePruneAge(cmd *cobra.Command) (time.Duration, error) {
	olderThanFlag := cmd.Flags().Lookup(flagOlderThan)
	ageFlag := cmd.Flags().Lookup(flagAge)
	if olderThanFlag.Changed && ageFlag.Changed {
		return 0, fmt.Errorf("use either --%s or --%s, not both", flagOlderThan, flagAge)
	}
	if !olderThanFlag.Changed && !ageFlag.Changed {
		if r := resolverFromContext(cmd.Context()); r != nil && r.ManualPruneAge() > 0 {
			return r.ManualPruneAge(), nil
		}
		return 7 * 24 * time.Hour, nil
	}
	ageStr := olderThanFlag.Value.String()
	if ageFlag.Changed {
		ageStr = ageFlag.Value.String()
	}
	d, ok := viperconfig.ParseHumanDuration(ageStr)
	if !ok {
		return 0, fmt.Errorf("invalid age %q: use a Go duration or suffix d/w (e.g. 7d, 2w)", ageStr)
	}
	return d, nil
}

// runGroupPrune runs one group pruner behind the shared prune flag handling:
// output mode and confirmation are resolved (so bad flags fail fast), then the
// report is rendered even when the pruner returns an error, so a partial run
// still reports what it removed before the error surfaces.
func runGroupPrune[R any](
	cmd *cobra.Command,
	ui termio.UI,
	pruneLambda func(context.Context, pruning.PruneState, bool, termio.UI) (R, error),
	render func(report R, dryRun bool),
	jsonValue func(report R, dryRun bool) any,
) error {
	age, dryRun, err := resolvePruneFlags(cmd)
	if err != nil {
		return err
	}
	mode, err := resolvePruneOutputMode(cmd, ui)
	if err != nil {
		return err
	}
	if err = confirmPrune(ui, dryRun); err != nil {
		return err
	}
	report, pruneErr := pruning.InvokePruneFunc(cmd.Context(), pruneLambda, age, dryRun, ui)
	renderPruneReport(ui, mode,
		func() { render(report, dryRun) },
		jsonValue(report, dryRun))
	return pruneErr
}

func buildImagePruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune cached runner images not in use",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGroupPrune(cmd, ui, pruning.PruneImages,
				func(report pruning.ImageReport, dryRun bool) { pruning.RenderImageReport(ui, report, dryRun) },
				func(report pruning.ImageReport, dryRun bool) any { return newImageReportJSON(report, dryRun) })
		},
	}
	setPruneFlags(cmd)
	return cmd
}

func buildVolumePruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune home volumes no longer referenced by a project VM",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGroupPrune(cmd, ui, pruning.PruneVolumes,
				func(report pruning.VolumeReport, dryRun bool) { pruning.RenderVolumeReport(ui, report, dryRun) },
				func(report pruning.VolumeReport, dryRun bool) any { return newVolumeReportJSON(report, dryRun) })
		},
	}
	setPruneFlags(cmd)
	return cmd
}

func resolvePruneFlags(cmd *cobra.Command) (time.Duration, bool, error) {
	age, err := resolvePruneAge(cmd)
	if err != nil {
		return 0 * time.Second, false, err
	}
	dryRun, _ := cmd.Flags().GetBool(flagDryRun)
	return age, dryRun, nil
}

func buildSandboxPruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune stale sandboxes and leftover task workers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGroupPrune(cmd, ui, pruning.PruneSandboxes,
				func(report pruning.SandboxReport, dryRun bool) { pruning.RenderSandboxReport(ui, report, dryRun) },
				func(report pruning.SandboxReport, dryRun bool) any { return newSandboxReportJSON(report, dryRun) })
		},
	}
	setPruneFlags(cmd)
	return cmd
}

func resolvePruneOutputMode(cmd *cobra.Command, ui termio.UI) (listOutput, error) {
	format, _ := cmd.Flags().GetString(flagFormat)
	return resolveListOutput(format, ui.Quiet())
}

func setPruneFlags(cmd *cobra.Command) {
	cmd.Flags().String(flagOlderThan, "7d", "Prune threshold (default: manualPruneAge from config)")
	cmd.Flags().StringP(flagAge, flagAge[:1], "", "Deprecated alias for --older-than")
	_ = cmd.Flags().MarkDeprecated(flagAge, "use --older-than")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would be pruned without deleting")
	cmd.Flags().String(flagFormat, "", "Output format (json)")
}
