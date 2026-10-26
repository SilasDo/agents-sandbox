package pruning

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// VolumeReport summarizes a PruneVolumes run.
type VolumeReport struct {
	VolumesPruned int
	Details       []StaleEntry
}

// PruneVolumes prunes home volumes of stale slugs. When a slug's last home
// volume is removed, its state file is removed too, so the state file never
// outlives the volume it references.
func PruneVolumes(
	ctx context.Context,
	pruneState PruneState,
	dryRun bool,
	ui termio.UI,
) (VolumeReport, error) {
	report := VolumeReport{VolumesPruned: 0, Details: nil}
	handles, err := msb.Get().ListVolumes(ctx)
	if err != nil {
		// gomutants:disable-next-line RETURN_ZERO reason="report is still its zero value at this point, so returning a fresh zero VolumeReport is identical"
		return report, err
	}
	exists := countVolumesByKey(handles)
	references, err := state.HomeVolumeReferences()
	if err != nil {
		// gomutants:disable-next-line RETURN_ZERO reason="report is still its zero value at this point, so returning a fresh zero VolumeReport is identical"
		return report, err
	}
	removed := pruneHomeVolumes(ctx, handles, pruneState, references, dryRun, ui, &report)
	if !dryRun {
		removeStateForGoneKeys(ui, removed, exists)
	}
	return report, nil
}

// countVolumesByKey returns how many home volumes each project/agent key
// currently has.
func countVolumesByKey(handles []msb.VolumeHandle) map[state.Key]int {
	exists := map[state.Key]int{}
	for _, handle := range handles {
		artifact := naming.ParseHomeVolumeName(handle.Name())
		if artifact.Slug != "" {
			exists[state.Key{Slug: artifact.Slug, Agent: artifact.Agent}]++
		}
	}
	return exists
}

// pruneHomeVolumes removes the home volumes of stale slugs and records the
// number removed per project/agent key. A volume still referenced by a project
// state file is kept and warned about instead, so prune can never delete a
// volume (for example one a pending adoption preserves) that a state file still
// points at.
func pruneHomeVolumes(
	ctx context.Context,
	handles []msb.VolumeHandle,
	pruneState PruneState,
	references map[string][]state.Key,
	dryRun bool,
	ui termio.UI,
	report *VolumeReport,
) map[state.Key]int {
	removed := map[state.Key]int{}
	for _, handle := range handles {
		name := handle.Name()
		volumeArtifact := naming.ParseHomeVolumeName(name)
		if volumeArtifact.Slug == "" {
			continue
		}
		key := state.Key{Slug: volumeArtifact.Slug, Agent: volumeArtifact.Agent}
		if _, found := pruneState.ToPrune[key]; !found {
			continue
		}
		if len(references[name]) > 0 {
			ui.Warnf("keeping home volume %s: still referenced by %s", name, state.FormatKeys(references[name]))
			continue
		}
		if !dryRun {
			if err := msb.Get().RemoveVolume(ctx, name); err != nil {
				ui.Warnf("failed to remove home volume %s: %v", name, err)
				continue
			}
			removed[key]++
		}
		report.VolumesPruned++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeVolume,
			Name:     name,
			Slug:     volumeArtifact.Slug,
			StaleFor: time.Since(handle.CreatedAt()),
			Digest:   volumeArtifact.Digest,
		})
	}
	return removed
}

// removeStateForGoneKeys removes the state file for keys whose last home
// volume was removed in this run. Keys are visited in a stable order so a
// skipped key never determines whether a later one is processed.
func removeStateForGoneKeys(ui termio.UI, removed, exists map[state.Key]int) {
	for _, key := range slices.SortedFunc(maps.Keys(removed), compareStateKey) {
		if removed[key] < exists[key] {
			continue
		}
		if err := state.RemoveHomeState(key); err != nil {
			ui.Warnf("failed to remove state file for key (slug %s, agent %s): %v", key.Slug, key.Agent, err)
		}
	}
}

// compareStateKey orders project keys by slug then agent.
func compareStateKey(a, b state.Key) int {
	if c := strings.Compare(a.Slug, b.Slug); c != 0 {
		return c
	}
	return strings.Compare(a.Agent, b.Agent)
}

func RenderVolumeReport(ui termio.UI, r VolumeReport, dryRun bool) {
	ui.Outf("%s %d home volume(s)", pruneReportPrefix(dryRun), r.VolumesPruned)
	for _, d := range r.Details {
		ui.Verbosef("  %s (%s)", d.Name, d.Slug)
	}
}
