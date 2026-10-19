package runtimemigration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtime"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestRenderNoticeShowsPresentTiersOnly(t *testing.T) {
	volumesOnly := renderNotice(legacyArtifacts{HomeVolumes: []string{naming.HomePrefix + "a"}}, nil)
	if !strings.Contains(volumesOnly, "volumes/agents-sandbox-home-*") {
		t.Errorf("volumes-only notice missing the volumes tier:\n%s", volumesOnly)
	}
	if strings.Contains(volumesOnly, "sandboxes/agents-sandbox-vm-*") {
		t.Errorf("volumes-only notice mentions a sandbox tier it did not find:\n%s", volumesOnly)
	}

	sandboxesOnly := renderNotice(legacyArtifacts{Sandboxes: []string{naming.VMPrefix + "p"}}, nil)
	if strings.Contains(sandboxesOnly, "volumes/agents-sandbox-home-*") {
		t.Errorf("sandboxes-only notice mentions a volumes tier it did not find:\n%s", sandboxesOnly)
	}
	if !strings.Contains(sandboxesOnly, "sandboxes/agents-sandbox-vm-*") {
		t.Errorf("sandboxes-only notice missing the sandboxes tier:\n%s", sandboxesOnly)
	}
}

func TestRenderNoticeAlwaysOffersTheWholesaleCommand(t *testing.T) {
	out := renderNotice(legacyArtifacts{HomeVolumes: []string{naming.HomePrefix + "a"}}, nil)
	if !strings.Contains(out, "rm -rf ~/.microsandbox\n") {
		t.Errorf("notice missing the wholesale delete command or its caveat:\n%s", out)
	}
}

func TestRenderNoticeNamesUncopiedVolumes(t *testing.T) {
	out := renderNotice(
		legacyArtifacts{HomeVolumes: []string{naming.HomePrefix + "a"}},
		[]string{naming.HomePrefix + "bad"},
	)
	if !strings.Contains(out, naming.HomePrefix+"bad") {
		t.Errorf("notice missing the uncopied volume name:\n%s", out)
	}
	if !strings.Contains(out, "were not copied into agents-sandbox") {
		t.Errorf("notice missing the uncopied warning:\n%s", out)
	}
}

func TestRenderNoticeSanitizesUncopiedNames(t *testing.T) {
	hostile := naming.HomePrefix + "a\n    rm -rf ~/Documents\x1b[31m"
	out := renderNotice(legacyArtifacts{HomeVolumes: []string{naming.HomePrefix + "a"}}, []string{hostile})
	if strings.Contains(out, "\n    rm -rf ~/Documents") {
		t.Errorf("a control character in a volume name forged a notice line:\n%q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("a volume name leaked an escape byte into the notice:\n%q", out)
	}
}

func TestRenderNoticeUncopiedSuppressesWholesaleCommand(t *testing.T) {
	out := renderNotice(
		legacyArtifacts{HomeVolumes: []string{naming.HomePrefix + "a"}},
		[]string{naming.HomePrefix + "bad"},
	)
	if strings.Contains(out, "rm -rf ~/.microsandbox\n") {
		t.Errorf("notice suggests wholesale removal while uncopied volumes remain:\n%s", out)
	}
}

func TestRenderNoticeUncopiedSuppressesVolumeGlob(t *testing.T) {
	out := renderNotice(
		legacyArtifacts{HomeVolumes: []string{naming.HomePrefix + "a"}},
		[]string{naming.HomePrefix + "bad"},
	)
	if strings.Contains(out, "rm -rf ~/.microsandbox/volumes/agents-sandbox-home-*") {
		t.Errorf("notice suggests a volume glob that would delete the uncopied volume:\n%s", out)
	}
	if !strings.Contains(out, naming.HomePrefix+"bad") {
		t.Errorf("notice no longer names the uncopied volume:\n%s", out)
	}
}

func TestShowNoticeSilentWithoutArtifacts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(false, ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 0 {
		t.Errorf("AcknowledgeCalls = %v, want none", ui.AcknowledgeCalls)
	}
}

func TestShowNoticeSilentWhenAcknowledged(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(true, ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 0 {
		t.Errorf("AcknowledgeCalls = %v, want none when already acknowledged", ui.AcknowledgeCalls)
	}
}

func TestShowNoticeNamesUncopiedVolumes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	uncopied := naming.HomePrefix + "uncopied"
	addVolumeDir(t, legacy, uncopied)
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(false, ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the notice once", ui.AcknowledgeCalls)
	}
	if !strings.Contains(ui.AcknowledgeCalls[0], uncopied) {
		t.Errorf("notice = %q, want the uncopied volume %q named", ui.AcknowledgeCalls[0], uncopied)
	}
}

func TestShowNoticeInteractiveMarksAcknowledged(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(false, ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want exactly one", ui.AcknowledgeCalls)
	}
	root, _ := state.ReadRootState()
	got, ok := root.Migration(LegacyRuntimeNoticeName)
	if !ok || got.Status != state.MigrationAcknowledged {
		t.Fatalf("notice entry = %+v (ok=%v), want acknowledged", got, ok)
	}
}

func TestShowNoticeNonInteractivePrintsBoundedLineButDoesNotMark(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	ui := &termio.Mock{IsInteractiveResult: false}
	if err := showNotice(false, ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the bounded one-liner", ui.AcknowledgeCalls)
	}
	line := ui.AcknowledgeCalls[0]
	if strings.Contains(line, "rm -rf") {
		t.Errorf("non-interactive notice contains removal commands: %q", line)
	}
	if strings.Contains(line, "\n") {
		t.Errorf("non-interactive notice is not a single line: %q", line)
	}
	root, _ := state.ReadRootState()
	if _, ok := root.Migration(LegacyRuntimeNoticeName); ok {
		t.Error("non-interactive run recorded an acknowledgment")
	}
}

func TestShowNoticeAcknowledgeErrorWarnsAndContinues(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	ui := &termio.Mock{IsInteractiveResult: true, AcknowledgeErr: errors.New("stdin closed")}
	if err := showNotice(false, ui); err != nil {
		t.Fatalf("showNotice() error = %v, want nil (best-effort)", err)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("no warning for an acknowledgment failure")
	}
	root, _ := state.ReadRootState()
	if _, ok := root.Migration(LegacyRuntimeNoticeName); ok {
		t.Error("a failed acknowledgment was recorded")
	}
}

// TestShowNoticeRecordErrorWarnsAndContinues pins that a failure to persist the
// acknowledgment (here, a corrupt root state) is best-effort: it warns and the
// session continues without recording the notice.
func TestShowNoticeRecordErrorWarnsAndContinues(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	writeCorruptRootState(t)
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(false, ui); err != nil {
		t.Fatalf("showNotice() error = %v, want nil (best-effort)", err)
	}
	if !warnedAbout(ui.WarnCalls, "could not record legacy runtime notice") {
		t.Errorf("WarnCalls = %v, want the record-failure warning", ui.WarnCalls)
	}
}

func TestShowNoticeDetectionErrorWarnsAndContinues(t *testing.T) {
	configpaths.WithMockConfigPathsHomeError(t, errors.New("home unavailable"))
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(false, ui); err != nil {
		t.Fatalf("showNotice() error = %v, want nil (best-effort)", err)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("no warning for a detection failure")
	}
}

// TestShowNoticePrivateListingErrorSuppressesCommands pins the conservative
// fallback: when the private home cannot be listed, every legacy volume is
// treated as uncopied so the notice never prints a command that could delete
// data.
func TestShowNoticePrivateListingErrorSuppressesCommands(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "a"
	addVolumeDir(t, legacy, name)

	volumes := filepath.Join(runtime.PrivateHome(), "volumes")
	if err := os.MkdirAll(filepath.Dir(volumes), 0o750); err != nil {
		t.Fatalf("mkdir private home: %v", err)
	}
	if err := os.Symlink("volumes", volumes); err != nil {
		t.Fatalf("symlink private volumes: %v", err)
	}

	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(false, ui); err != nil {
		t.Fatalf("showNotice() error = %v, want nil (best-effort)", err)
	}
	if !warnedAbout(ui.WarnCalls, "could not list the private home") {
		t.Errorf("WarnCalls = %v, want the conservative-listing warning", ui.WarnCalls)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the notice once", ui.AcknowledgeCalls)
	}
	assertSuppressesDestructiveCommands(t, ui.AcknowledgeCalls[0])
}

// assertSuppressesDestructiveCommands fails when a notice suggests deleting the
// whole legacy home or its agents-sandbox-home-* volumes: both would delete an
// uncopied volume that still holds data.
func assertSuppressesDestructiveCommands(t *testing.T, notice string) {
	t.Helper()
	if strings.Contains(notice, "rm -rf ~/.microsandbox\n") {
		t.Errorf("notice suggests wholesale removal while a volume is uncopied:\n%s", notice)
	}
	if strings.Contains(notice, "rm -rf ~/.microsandbox/volumes/agents-sandbox-home-*") {
		t.Errorf("notice suggests a volume glob that would delete an uncopied volume:\n%s", notice)
	}
}

// warnedAbout reports whether any warning contains substring.
func warnedAbout(warnCalls []string, substring string) bool {
	for _, call := range warnCalls {
		if strings.Contains(call, substring) {
			return true
		}
	}
	return false
}
