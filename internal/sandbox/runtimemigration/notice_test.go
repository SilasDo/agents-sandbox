package runtimemigration

import (
	"errors"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
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

func TestRenderNoticeNamesExcludedVolumes(t *testing.T) {
	out := renderNotice(
		legacyArtifacts{HomeVolumes: []string{naming.HomePrefix + "a"}},
		[]string{naming.HomePrefix + "bad"},
	)
	if !strings.Contains(out, naming.HomePrefix+"bad") {
		t.Errorf("notice missing the excluded volume name:\n%s", out)
	}
	if !strings.Contains(out, "could not be copied") {
		t.Errorf("notice missing the excluded warning:\n%s", out)
	}
}

func TestShowNoticeSilentWithoutArtifacts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(false, nil, ui); err != nil {
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
	if err := showNotice(true, nil, ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 0 {
		t.Errorf("AcknowledgeCalls = %v, want none when already acknowledged", ui.AcknowledgeCalls)
	}
}

// showNoticeResult shows the notice and returns its result, the way a session
// entrypoint propagates it, so the notice's error contract is exercised rather
// than discarded.
func showNoticeResult(ui termio.UI, excluded []string) error {
	return showNotice(false, excluded, ui)
}

func TestShowNoticeNamesExcludedVolumes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	excluded := naming.HomePrefix + "bad"
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNoticeResult(ui, []string{excluded}); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the notice once", ui.AcknowledgeCalls)
	}
	if !strings.Contains(ui.AcknowledgeCalls[0], excluded) {
		t.Errorf("notice = %q, want the excluded volume %q named", ui.AcknowledgeCalls[0], excluded)
	}
}

func TestShowNoticeInteractiveMarksAcknowledged(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(false, nil, ui); err != nil {
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

func TestShowNoticeNonInteractivePrintsButDoesNotMark(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	ui := &termio.Mock{IsInteractiveResult: false}
	if err := showNotice(false, nil, ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 0 {
		t.Errorf("AcknowledgeCalls = %v, want none non-interactively", ui.AcknowledgeCalls)
	}
	if len(ui.InfoCalls) == 0 {
		t.Error("non-interactive run printed nothing")
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
	if err := showNotice(false, nil, ui); err != nil {
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

func TestShowNoticeDetectionErrorWarnsAndContinues(t *testing.T) {
	configpaths.WithMockConfigPathsHomeError(t, errors.New("home unavailable"))
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(false, nil, ui); err != nil {
		t.Fatalf("showNotice() error = %v, want nil (best-effort)", err)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("no warning for a detection failure")
	}
}
