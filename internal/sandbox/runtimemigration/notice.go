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
// shown. The wholesale-removal command appears with its caveat unless excluded
// volumes exist: those live under ~/.microsandbox, so a wholesale rm would
// destroy uncopied data, and the notice must never suggest it.
func renderNotice(a legacyArtifacts, excluded []string) string {
	var b strings.Builder
	b.WriteString("agents-sandbox now runs its own microsandbox runtime\n\n")
	b.WriteString("Nothing broke — agents-sandbox keeps its runtime in its own private home now,\n")
	b.WriteString("so the system-wide microsandbox is no longer needed to run it.\n\n")
	b.WriteString("Your ~/.microsandbox was left untouched. It holds two kinds of things:\n\n")
	if len(a.HomeVolumes) > 0 || len(a.Sandboxes) > 0 {
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
			b.WriteString("    rm -rf ~/.microsandbox/volumes/agents-sandbox-home-*\n")
		}
		if len(a.Sandboxes) > 0 {
			b.WriteString("    rm -rf ~/.microsandbox/sandboxes/agents-sandbox-vm-*\n")
			b.WriteString("    rm -rf ~/.microsandbox/sandboxes/agents-sandbox-task-*\n")
		}
		b.WriteString("\n")
	}
	if len(excluded) == 0 {
		b.WriteString("  Everything else is your own microsandbox data; we never touch it. Remove the\n")
		b.WriteString("  whole folder only if microsandbox was installed just for agents-sandbox:\n")
		b.WriteString("    rm -rf ~/.microsandbox\n\n")
	}
	if len(excluded) > 0 {
		b.WriteString("  These volumes could not be copied and still hold data:\n")
		for _, name := range excluded {
			b.WriteString("    " + name + "\n")
		}
		b.WriteString("  They were left in place; check them before removing anything. Do not remove\n")
		b.WriteString("  the whole ~/.microsandbox while they remain.\n\n")
	}
	b.WriteString("Nothing was deleted for you.")
	return b.String()
}
