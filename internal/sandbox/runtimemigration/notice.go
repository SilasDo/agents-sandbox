package runtimemigration

import (
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// LegacyRuntimeNoticeName is the root-state migrations registry key for the
// one-time "system microsandbox is now optional" notice.
const LegacyRuntimeNoticeName = "legacy_runtime_notice"

// showNotice presents the post-migration notice once. It is best-effort: any
// failure warns and returns nil so the session proceeds. It never fires for a
// fresh install or a user's own unrelated microsandbox (detection is
// prefix-based), and never deletes anything. It records the acknowledgment only
// in an interactive session, so a non-interactive run cannot silently consume
// the one-time moment. excluded names volumes a just-finalized campaign could
// not copy, which still hold data.
func showNotice(acknowledged bool, excluded []string, ui termio.UI) error {
	if acknowledged {
		return nil
	}
	artifacts, err := detectLegacyArtifactsFor()
	if err != nil {
		ui.Warnf("skipping legacy runtime notice: %v", err)
		return nil
	}
	if !artifacts.present() {
		return nil
	}
	message := renderNotice(artifacts, excluded)
	if !ui.IsInteractive() {
		ui.Infof("%s", message)
		return nil
	}
	if err := ui.Acknowledge(message); err != nil {
		ui.Warnf("could not show legacy runtime notice: %v", err)
		return nil
	}
	if err := acknowledgeNotice(); err != nil {
		ui.Warnf("could not record legacy runtime notice: %v", err)
	}
	return nil
}

// acknowledgeNotice records the terminal acknowledgment under the root lock,
// re-reading first so a concurrent session's write is never clobbered.
func acknowledgeNotice() error {
	_, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		if _, ok := tx.State().Migration(LegacyRuntimeNoticeName); ok {
			return nil
		}
		tx.State().SetMigration(LegacyRuntimeNoticeName, state.Migration{Status: state.MigrationAcknowledged})
		return tx.Commit()
	})
	return err
}

// renderNotice builds the notice text. Only the tiers actually present are
// shown. When excluded volumes exist the notice leads with a "do not remove"
// warning naming them, and suppresses every command that would delete them: the
// wholesale rm -rf ~/.microsandbox and the selective
// ~/.microsandbox/volumes/agents-sandbox-home-* glob both match an excluded
// (uncopied) volume. The safe sandbox globs are still offered, and the volumes
// glob is replaced by a note to remove the remaining volumes individually.
func renderNotice(a legacyArtifacts, excluded []string) string {
	var b strings.Builder
	b.WriteString("agents-sandbox now runs its own microsandbox runtime\n\n")
	b.WriteString("Nothing broke — agents-sandbox keeps its runtime in its own private home now,\n")
	b.WriteString("so the system-wide microsandbox is no longer needed to run it.\n\n")
	b.WriteString("Your ~/.microsandbox was left untouched. It holds two kinds of things:\n\n")
	if len(excluded) > 0 {
		writeExcludedWarning(&b, excluded)
	}
	writeRemovableLeftovers(&b, a, len(excluded) > 0)
	if len(excluded) == 0 {
		b.WriteString("  Everything else is your own microsandbox data; we never touch it. Remove the\n")
		b.WriteString("  whole folder only if microsandbox was installed just for agents-sandbox:\n")
		b.WriteString("    rm -rf ~/.microsandbox\n\n")
	}
	b.WriteString("Nothing was deleted for you.")
	return b.String()
}

// writeExcludedWarning writes the leading "do not remove" paragraph naming the
// volumes a campaign could not copy, before any removal command.
func writeExcludedWarning(b *strings.Builder, excluded []string) {
	b.WriteString("  These volumes could not be copied and still hold data:\n")
	for _, name := range excluded {
		b.WriteString("    " + name + "\n")
	}
	b.WriteString("  They were left in place; do not remove them.\n\n")
}

// writeRemovableLeftovers writes the "ours, safe to remove" tier and its removal
// commands, if any of our leftovers are present. When excluded volumes exist,
// hasExcluded suppresses the agents-sandbox-home-* glob (it matches an uncopied
// volume) and offers a qualification instead; the sandbox globs are unaffected.
func writeRemovableLeftovers(b *strings.Builder, a legacyArtifacts, hasExcluded bool) {
	if len(a.HomeVolumes) == 0 && len(a.Sandboxes) == 0 {
		return
	}
	b.WriteString("  Ours, safe to remove:\n")
	if len(a.HomeVolumes) > 0 {
		b.WriteString("    ~/.microsandbox/volumes/agents-sandbox-home-*\n")
	}
	if len(a.Sandboxes) > 0 {
		b.WriteString("    ~/.microsandbox/sandboxes/agents-sandbox-vm-*\n")
		b.WriteString("    ~/.microsandbox/sandboxes/agents-sandbox-task-*\n")
	}
	b.WriteString("  Remove just these with:\n")
	if len(a.HomeVolumes) > 0 {
		writeVolumeRemovalCommand(b, hasExcluded)
	}
	if len(a.Sandboxes) > 0 {
		b.WriteString("    rm -rf ~/.microsandbox/sandboxes/agents-sandbox-vm-*\n")
		b.WriteString("    rm -rf ~/.microsandbox/sandboxes/agents-sandbox-task-*\n")
	}
	b.WriteString("\n")
}

// writeVolumeRemovalCommand writes the removal command for our home volumes, or
// a qualification when excluded volumes exist: the glob matches an uncopied
// volume, so the user must remove the remaining volumes individually.
func writeVolumeRemovalCommand(b *strings.Builder, hasExcluded bool) {
	if hasExcluded {
		b.WriteString("    (the agents-sandbox-home-* glob also matches the uncopied volumes above;\n")
		b.WriteString("    remove the rest individually and leave those in place)\n")
		return
	}
	b.WriteString("    rm -rf ~/.microsandbox/volumes/agents-sandbox-home-*\n")
}
