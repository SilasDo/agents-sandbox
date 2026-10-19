# Post-migration Legacy-runtime Notice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After a ≤0.5.0 upgrader's legacy microsandbox home is dealt with, tell them once — interactively, acknowledgably — that agents-sandbox now runs its own isolated runtime and their system-wide microsandbox is optional; never delete anything.

**Architecture:** Detection is a file listing over the legacy home (`volumes/agents-sandbox-home-*`, `sandboxes/agents-sandbox-{vm,task}-*`). A new `legacy_runtime_notice` entry in the existing root-state `migrations` registry records acknowledgment as a new terminal `acknowledged` status. A new `termio.UI.Acknowledge` primitive prints the notice and waits for a keypress. All four trigger points live in `runtimemigration.Offer`, which no longer returns early on a terminal migration.

**Tech Stack:** Go 1.26, `internal/termio`, `internal/sandbox/state`, `internal/sandbox/runtimemigration`, `internal/sandbox/naming`, Jekyll docs.

**Spec:** `.operator-shared/specs/runtime-home-decoupling.md` → "Post-migration legacy-runtime notice".

## Global Constraints

- Instruct-only: agents-sandbox must never delete `~/.microsandbox` or anything in it. The notice only prints commands for the user to run.
- Once-ever: the notice fires only while `legacy_runtime_notice` is absent from `migrations`; presence is permanent with no re-arm.
- Non-interactive sessions may print the informational text but must never write the acknowledgment.
- Detection is prefix-based and never fires for a fresh install or a user's own unrelated microsandbox.
- The notice is best-effort: a detection or acknowledgment failure warns and lets the session continue; it never blocks.
- All root-state mutation goes through `state.WithRootState`; never hold the root lock across the keypress.
- Use `make fmt`/`make lint` (`golangci-lint`), not `go fmt`/`go vet`. Run `make check` before finalizing.
- TDD: write the failing test, watch it fail, implement, watch it pass, commit.

## Review Focus

- A legacy home whose `~/.microsandbox` exists but holds only the user's own unrelated data → notice must be silent.
- A missing, not-a-directory, or permission-denied `volumes`/`sandboxes` directory → detection must not error and must not fire.
- A non-interactive session with artifacts present → prints the line, writes nothing; a later interactive session must still show the notice.
- Concurrent interactive sessions → the acknowledgment write must be idempotent and must not clobber another session's terminal write.
- A partial copy that left `excluded` volumes → the notice names them and never suggests removing the whole `~/.microsandbox`.

---

### Task 1: `termio.Acknowledge` primitive

**Files:**
- Modify: `internal/termio/termio.go` (the `UI` interface, after `Confirm`)
- Modify: `internal/termio/printer.go`
- Modify: `internal/termio/mock.go`
- Test: `internal/termio/printer_test.go`, `internal/termio/mock_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `UI.Acknowledge(message string) error`
  - `func (p *printer) Acknowledge(message string) error`
  - `func (m *Mock) Acknowledge(message string) error`, with exported fields `AcknowledgeCalls []string` and `AcknowledgeErr error`.

- [ ] **Step 1: Write the failing tests**

In `internal/termio/printer_test.go`:

```go
func TestPrinterAcknowledgeInteractiveWaitsForEnter(t *testing.T) {
	var stderr bytes.Buffer
	p := &printer{stdin: strings.NewReader("next\n"), stderr: &stderr, isTerminal: func(int) bool { return true }}
	if err := p.Acknowledge("body text"); err != nil {
		t.Fatalf("Acknowledge() error = %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "body text") || !strings.Contains(out, "Press Enter") {
		t.Errorf("interactive Acknowledge output = %q, want body and a Press Enter affordance", out)
	}
}

func TestPrinterAcknowledgeNonInteractivePrintsWithoutWaiting(t *testing.T) {
	var stderr bytes.Buffer
	p := &printer{stdin: strings.NewReader(""), stderr: &stderr, isTerminal: func(int) bool { return false }}
	if err := p.Acknowledge("body text"); err != nil {
		t.Fatalf("Acknowledge() error = %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "body text") {
		t.Errorf("non-interactive Acknowledge output = %q, want the body", out)
	}
	if strings.Contains(out, "Press Enter") {
		t.Errorf("non-interactive Acknowledge output = %q, want no Press Enter affordance", out)
	}
}

func TestPrinterAcknowledgeReadError(t *testing.T) {
	p := &printer{stdin: strings.NewReader(""), stderr: &bytes.Buffer{}, isTerminal: func(int) bool { return true }}
	if err := p.Acknowledge("body text"); err == nil {
		t.Fatal("Acknowledge() error = nil, want a read error on empty stdin")
	}
}
```

In `internal/termio/mock_test.go`:

```go
func TestMockAcknowledgeRecordsAndReturnsError(t *testing.T) {
	m := &Mock{AcknowledgeErr: errors.New("boom")}
	if err := m.Acknowledge("note"); err == nil {
		t.Fatal("Acknowledge() error = nil, want the configured error")
	}
	if len(m.AcknowledgeCalls) != 1 || m.AcknowledgeCalls[0] != "note" {
		t.Errorf("AcknowledgeCalls = %v, want [\"note\"]", m.AcknowledgeCalls)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/termio/ -run 'Acknowledge' -v`
Expected: FAIL — `Acknowledge` undefined on `*printer`/`*Mock`, and `UI` has no such method.

- [ ] **Step 3: Add the method to the `UI` interface**

In `internal/termio/termio.go`, inside `type UI interface`, after `Confirm(...)`:

```go
	Confirm(prompt string, defaultYes bool) (bool, error)
	Acknowledge(message string) error
```

- [ ] **Step 4: Implement `(*printer).Acknowledge` in `internal/termio/printer.go`**

```go
// Acknowledge prints a one-time notice and, when interactive, waits for the
// user to press Enter. A non-interactive session prints the notice but returns
// immediately, so its caller does not record an acknowledgment the user never
// gave.
func (p *printer) Acknowledge(message string) error {
	p.write(p.stderr, "", message)
	if !p.IsInteractive() {
		return nil
	}
	fmt.Fprint(p.stderr, "Press Enter to continue: ")
	_, err := p.getStdinReader().ReadString('\n')
	return err
}
```

- [ ] **Step 5: Implement `(*Mock).Acknowledge` in `internal/termio/mock.go`**

Add to the `Mock` struct (next to `ConfirmCalls`):

```go
	AcknowledgeCalls []string
	AcknowledgeErr   error
```

And the method:

```go
// Acknowledge records the notice. It does not gate on IsInteractiveResult: the
// caller decides whether a non-interactive session may record an acknowledgment.
func (m *Mock) Acknowledge(message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.AcknowledgeCalls = append(m.AcknowledgeCalls, message)
	return m.AcknowledgeErr
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/termio/ -run 'Acknowledge' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/termio/termio.go internal/termio/printer.go internal/termio/mock.go internal/termio/printer_test.go internal/termio/mock_test.go
git commit -m "feat(termio): add Acknowledge primitive for one-time notices"
```

---

### Task 2: `MigrationAcknowledged` status

**Files:**
- Modify: `internal/sandbox/state/root.go` (the `MigrationStatus` const block)
- Test: `internal/sandbox/state/root_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `state.MigrationAcknowledged MigrationStatus = "acknowledged"`.

- [ ] **Step 1: Write the failing test**

In `internal/sandbox/state/root_test.go`:

```go
func TestMigrationAcknowledgedRoundTrips(t *testing.T) {
	if state.MigrationAcknowledged != "acknowledged" {
		t.Fatalf("MigrationAcknowledged = %q, want \"acknowledged\"", state.MigrationAcknowledged)
	}
	rs := &state.RootState{}
	rs.SetMigration("legacy_runtime_notice", state.Migration{Status: state.MigrationAcknowledged})
	got, ok := rs.Migration("legacy_runtime_notice")
	if !ok || got.Status != state.MigrationAcknowledged {
		t.Fatalf("round-tripped migration = %+v (ok=%v), want acknowledged", got, ok)
	}
}
```

(Adjust the package/import style to match the file: if `root_test.go` is `package state`, use bare `RootState`/`Migration`/`MigrationAcknowledged`.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/state/ -run 'MigrationAcknowledged' -v`
Expected: FAIL — `state.MigrationAcknowledged` undefined.

- [ ] **Step 3: Add the constant**

In `internal/sandbox/state/root.go`, extend the status const block:

```go
const (
	MigrationCopying      MigrationStatus = "copying"
	MigrationCompleted    MigrationStatus = "completed"
	MigrationDismissed    MigrationStatus = "dismissed"
	MigrationAcknowledged MigrationStatus = "acknowledged"
)
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sandbox/state/ -run 'MigrationAcknowledged' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state/root.go internal/sandbox/state/root_test.go
git commit -m "feat(state): add acknowledged migration status"
```

---

### Task 3: Legacy-artifact detection

**Files:**
- Modify: `internal/sandbox/naming/naming.go` (add `IsHomeVolume`)
- Modify: `internal/sandbox/runtimemigration/legacy.go` (refactor the directory listing; add sandbox detection + `legacyArtifacts`)
- Test: `internal/sandbox/runtimemigration/legacy_test.go`

**Interfaces:**
- Consumes: `runtime.LegacyHome()`, `naming.HomePrefix`, `naming.IsLauncherSandbox` (both existing).
- Produces:
  - `naming.IsHomeVolume(name string) bool`
  - `type legacyArtifacts struct { HomeVolumes []string; Sandboxes []string }` with method `present() bool`
  - `func detectLegacyArtifacts(legacyHome string) (legacyArtifacts, error)`
  - `func detectLegacyArtifactsFor() (legacyArtifacts, error)`
  - `func legacySandboxNames(legacyHome string) ([]string, error)`

- [ ] **Step 1: Write the failing tests**

Add to `internal/sandbox/runtimemigration/legacy_test.go` (it already has `newHome`, `addVolumeDir`, and the `configpaths.WithMockConfigPaths`/`installLegacyHome` helpers):

```go
// addSandboxDir creates <home>/sandboxes/<name>.
func addSandboxDir(t *testing.T, home, name string) {
	t.Helper()
	path := filepath.Join(home, "sandboxes", name)
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatalf("create sandbox record %q: %v", name, err)
	}
}

func TestDetectLegacyArtifactsFindsHomeVolumes(t *testing.T) {
	legacy := newHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	got, err := detectLegacyArtifacts(legacy)
	if err != nil {
		t.Fatalf("detectLegacyArtifacts: %v", err)
	}
	if len(got.HomeVolumes) != 1 || len(got.Sandboxes) != 0 || !got.present() {
		t.Fatalf("artifacts = %+v, want one home volume and nothing else", got)
	}
}

func TestDetectLegacyArtifactsFindsLauncherSandboxes(t *testing.T) {
	legacy := newHome(t)
	addSandboxDir(t, legacy, naming.VMPrefix+"proj")
	addSandboxDir(t, legacy, naming.TaskPrefix+"proj")
	addSandboxDir(t, legacy, "user-own")
	got, err := detectLegacyArtifacts(legacy)
	if err != nil {
		t.Fatalf("detectLegacyArtifacts: %v", err)
	}
	if len(got.Sandboxes) != 2 || len(got.HomeVolumes) != 0 || !got.present() {
		t.Fatalf("artifacts = %+v, want the two launcher sandboxes only", got)
	}
}

func TestDetectLegacyArtifactsSilentForUnrelatedHome(t *testing.T) {
	legacy := newHome(t)
	addVolumeDir(t, legacy, "user-own-volume")
	addSandboxDir(t, legacy, "user-own-sandbox")
	got, err := detectLegacyArtifacts(legacy)
	if err != nil {
		t.Fatalf("detectLegacyArtifacts: %v", err)
	}
	if got.present() {
		t.Fatalf("artifacts = %+v, want none", got)
	}
}

func TestDetectLegacyArtifactsToleratesMissingDirs(t *testing.T) {
	got, err := detectLegacyArtifacts(t.TempDir())
	if err != nil {
		t.Fatalf("detectLegacyArtifacts: %v", err)
	}
	if got.present() {
		t.Fatalf("artifacts = %+v, want none for an empty home", got)
	}
}

func TestDetectLegacyArtifactsForResolvesLegacyHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	got, err := detectLegacyArtifactsFor()
	if err != nil {
		t.Fatalf("detectLegacyArtifactsFor: %v", err)
	}
	if !got.present() {
		t.Fatal("detectLegacyArtifactsFor() found no artifacts in the installed legacy home")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/runtimemigration/ -run 'DetectLegacyArtifacts' -v`
Expected: FAIL — `detectLegacyArtifacts`/`detectLegacyArtifactsFor`/`addSandboxDir`-referenced symbols undefined.

- [ ] **Step 3: Add `naming.IsHomeVolume`**

In `internal/sandbox/naming/naming.go`, after `IsLauncherSandbox`:

```go
// IsHomeVolume reports whether name is an agents-sandbox home volume name.
func IsHomeVolume(name string) bool {
	return strings.HasPrefix(name, HomePrefix)
}
```

- [ ] **Step 4: Refactor the directory listing and add detection in `legacy.go`**

Replace `homeVolumeNames`'s body with a shared tolerant/strict helper, and add the sandbox + artifact functions. The exact new functions:

```go
// matchingDirNames lists the immediate subdirectories of dir whose name matches
// match. A missing directory is always empty. When tolerateUnreadable is true an
// existing-but-unlistable directory (not-a-directory or permission denied) is
// also empty; when false only the missing case is empty and any other read
// failure is returned, so an open campaign fails closed.
func matchingDirNames(dir string, match func(string) bool, tolerateUnreadable bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		if tolerateUnreadable && (errors.Is(err, syscall.ENOTDIR) || errors.Is(err, fs.ErrPermission)) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if name := entry.Name(); match(name) {
			names = append(names, name)
		}
	}
	return names, nil
}

func homeVolumeNames(home string, tolerateUnreadable bool) ([]string, error) {
	return matchingDirNames(filepath.Join(home, "volumes"), naming.IsHomeVolume, tolerateUnreadable)
}

// legacySandboxNames lists launcher-owned sandbox records under
// <legacyHome>/sandboxes. Tolerance matches the pending-state volume listing.
func legacySandboxNames(legacyHome string) ([]string, error) {
	return matchingDirNames(filepath.Join(legacyHome, "sandboxes"), naming.IsLauncherSandbox, true)
}

// legacyArtifacts are the launcher-owned leftovers in the legacy home.
type legacyArtifacts struct {
	HomeVolumes []string
	Sandboxes   []string
}

func (a legacyArtifacts) present() bool {
	return len(a.HomeVolumes) > 0 || len(a.Sandboxes) > 0
}

// detectLegacyArtifacts reports the launcher-owned leftovers under legacyHome.
func detectLegacyArtifacts(legacyHome string) (legacyArtifacts, error) {
	volumes, err := legacyVolumeNames(legacyHome)
	if err != nil {
		return legacyArtifacts{}, err
	}
	sandboxes, err := legacySandboxNames(legacyHome)
	if err != nil {
		return legacyArtifacts{}, err
	}
	return legacyArtifacts{HomeVolumes: volumes, Sandboxes: sandboxes}, nil
}

// detectLegacyArtifactsFor resolves the legacy home and delegates.
func detectLegacyArtifactsFor() (legacyArtifacts, error) {
	legacyHome, err := runtime.LegacyHome()
	if err != nil {
		return legacyArtifacts{}, err
	}
	return detectLegacyArtifacts(legacyHome)
}
```

Delete the now-unused `strings` import only if nothing else in `legacy.go` uses it (it does not after this change; keep the import list correct and run `make fmt`).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/runtimemigration/ -run 'DetectLegacyArtifacts' -v`
Expected: PASS. Then run the whole package to confirm the `homeVolumeNames` refactor did not regress: `go test ./internal/sandbox/runtimemigration/`.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/naming/naming.go internal/sandbox/runtimemigration/legacy.go internal/sandbox/runtimemigration/legacy_test.go
git commit -m "feat(migration): detect launcher artifacts in the legacy home"
```

---

### Task 4: Notice rendering, display, and once-ever acknowledgment

**Files:**
- Create: `internal/sandbox/runtimemigration/notice.go`
- Test: `internal/sandbox/runtimemigration/notice_test.go` (new)

**Interfaces:**
- Consumes: `legacyArtifacts`/`detectLegacyArtifactsFor` (Task 3), `state.MigrationAcknowledged` (Task 2), `termio.UI.Acknowledge` (Task 1).
- Produces:
  - `const LegacyRuntimeNoticeName = "legacy_runtime_notice"`
  - `func renderNotice(a legacyArtifacts, excluded []string) string`
  - `func showNotice(acknowledged bool, excluded []string, ui termio.UI) error`
  - `func acknowledgeNotice() error`

- [ ] **Step 1: Write the failing tests**

Create `internal/sandbox/runtimemigration/notice_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/runtimemigration/ -run 'Notice|RenderNotice' -v`
Expected: FAIL — `showNotice`, `renderNotice`, `LegacyRuntimeNoticeName` undefined.

- [ ] **Step 3: Implement `internal/sandbox/runtimemigration/notice.go`**

```go
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
// shown; the wholesale-removal command always appears with its caveat.
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
	b.WriteString("  Everything else is your own microsandbox data; we never touch it. Remove the\n")
	b.WriteString("  whole folder only if microsandbox was installed just for agents-sandbox:\n")
	b.WriteString("    rm -rf ~/.microsandbox\n\n")
	if len(excluded) > 0 {
		b.WriteString("  These volumes could not be copied and still hold data:\n")
		for _, name := range excluded {
			b.WriteString("    " + name + "\n")
		}
		b.WriteString("  They were left in place; check them before removing anything.\n\n")
	}
	b.WriteString("Nothing was deleted for you.")
	return b.String()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/runtimemigration/ -run 'Notice|RenderNotice' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/runtimemigration/notice.go internal/sandbox/runtimemigration/notice_test.go
git commit -m "feat(migration): render and acknowledge the post-migration notice"
```

---

### Task 5: Wire the notice into `Offer`

**Files:**
- Modify: `internal/sandbox/runtimemigration/copy.go` (`CopyVolumes`, `copyVolumesLocked`, `finalizeMigration`)
- Modify: `internal/sandbox/runtimemigration/offer.go` (`Offer`, `finalizeCompletedCampaign`)
- Test: `internal/sandbox/runtimemigration/offer_test.go` (and adjust `copy_test.go` if the excluded-warning assertion moves)

**Interfaces:**
- Consumes: `showNotice`, `LegacyRuntimeNoticeName` (Task 4); `detectLegacyArtifacts` (Task 3).
- Produces:
  - `func copyVolumesCampaign(ctx context.Context, client msb.Client, ui termio.UI) ([]string, error)` (the excluded names)
  - `CopyVolumes(ctx, client, ui) error` keeps its signature and discards the excluded names.
  - `finalizeCompletedCampaign(ui termio.UI) ([]string, error)` (the excluded names).

- [ ] **Step 1: Write the failing tests**

Add to `internal/sandbox/runtimemigration/offer_test.go`. Reuse the existing `installLegacyHome`, `addVolumeDir`, `interactiveUI`, and `seedRootState` helpers; add `addSandboxDir` from Task 3.

```go
// noticeUI returns an interactive mock whose Select returns key and which
// records the notice acknowledgment.
func noticeUI(key string) *termio.Mock {
	return &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) { return key, nil },
	}
}

func TestOfferShowsNoticeAfterCopy(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	target := t.TempDir()
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: name, Path_: target}, nil
		},
	}
	ui := noticeUI(choiceCopy)
	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the notice once", ui.AcknowledgeCalls)
	}
	root, _ := state.ReadRootState()
	if m, _ := root.Migration(MigrationName); m.Status != state.MigrationCompleted {
		t.Fatalf("migration status = %q, want completed", m.Status)
	}
}

func TestOfferShowsNoticeAfterDismiss(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	ui := noticeUI(choiceFresh)
	client := &msb.MockMsbClient{}
	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the notice once", ui.AcknowledgeCalls)
	}
}

func TestOfferShowsNoticeOnLaterTerminalSession(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	if err := seedRootState(state.RootState{}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if _, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		tx.State().SetMigration("runtime_home", state.Migration{Status: state.MigrationCompleted})
		return tx.Commit()
	}); err != nil {
		t.Fatalf("seed completed migration: %v", err)
	}

	ui := &termio.Mock{IsInteractiveResult: true}
	if err := Offer(context.Background(), &msb.MockMsbClient{}, ui); err != nil {
		t.Fatalf("Offer() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the notice on a terminal session", ui.AcknowledgeCalls)
	}
}

func TestOfferShowsNoticePendingNothingToCopy(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addSandboxDir(t, legacy, naming.VMPrefix+"proj") // artifacts present, no volumes to copy
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := Offer(context.Background(), &msb.MockMsbClient{}, ui); err != nil {
		t.Fatalf("Offer() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the notice", ui.AcknowledgeCalls)
	}
}

func TestOfferNoticeOnceEver(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	if _, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		tx.State().SetMigration(LegacyRuntimeNoticeName, state.Migration{Status: state.MigrationAcknowledged})
		tx.State().SetMigration("runtime_home", state.Migration{Status: state.MigrationCompleted})
		return tx.Commit()
	}); err != nil {
		t.Fatalf("seed acknowledged notice: %v", err)
	}
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := Offer(context.Background(), &msb.MockMsbClient{}, ui); err != nil {
		t.Fatalf("Offer() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 0 {
		t.Fatalf("AcknowledgeCalls = %v, want none once acknowledged", ui.AcknowledgeCalls)
	}
}

func TestOfferNonInteractiveNoticeDoesNotMark(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"a")
	if _, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		tx.State().SetMigration("runtime_home", state.Migration{Status: state.MigrationCompleted})
		return tx.Commit()
	}); err != nil {
		t.Fatalf("seed completed migration: %v", err)
	}
	ui := &termio.Mock{IsInteractiveResult: false}
	if err := Offer(context.Background(), &msb.MockMsbClient{}, ui); err != nil {
		t.Fatalf("Offer() error = %v", err)
	}
	root, _ := state.ReadRootState()
	if _, ok := root.Migration(LegacyRuntimeNoticeName); ok {
		t.Error("non-interactive Offer recorded an acknowledgment")
	}
}
```

Note: the existing test at `offer_test.go:780` `TestFinalizeMigrationWarnsExcluded` is replaced — the excluded summary now appears in the notice (Task 4, `TestRenderNoticeNamesExcludedVolumes`), and `copyVolumesLocked` still warns per failed volume. Delete `TestFinalizeMigrationWarnsExcluded`; add a focused notice-after-copy exclusion test:

```go
func TestOfferNoticeNamesExcludedVolumes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	bad := naming.HomePrefix + "bad"
	good := naming.HomePrefix + "good"
	addVolumeDir(t, legacy, bad)
	addVolumeDir(t, legacy, good)

	target := t.TempDir()
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			if name == bad {
				return nil, errors.New("no space")
			}
			return &msb.MockVolumeHandle{Name_: name, Path_: target}, nil
		},
	}
	ui := noticeUI(choiceCopy)
	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 || !strings.Contains(ui.AcknowledgeCalls[0], bad) {
		t.Fatalf("AcknowledgeCalls = %v, want the excluded volume %q named", ui.AcknowledgeCalls, bad)
	}
}
```

`offer_test.go` already imports `msbSdk`, `msb`, `errors`, `strings`, and `state`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/runtimemigration/ -run 'Offer.*Notice|OfferShowsNotice' -v`
Expected: FAIL — the notice is never shown (and `copyVolumesCampaign`/`addSandboxDir` may be undefined).

- [ ] **Step 3: Thread the excluded names through the copy path in `copy.go`**

- Rename the body of `CopyVolumes` into `copyVolumesCampaign` returning `([]string, error)`, and add a thin wrapper:

```go
// CopyVolumes runs the campaign and discards the excluded names. See
// copyVolumesCampaign.
func CopyVolumes(ctx context.Context, client msb.Client, ui termio.UI) error {
	_, err := copyVolumesCampaign(ctx, client, ui)
	return err
}

// copyVolumesCampaign runs the campaign and returns the names it excluded
// (failed to copy), which still hold data in the legacy home.
func copyVolumesCampaign(ctx context.Context, client msb.Client, ui termio.UI) ([]string, error) {
	var excluded []string
	_, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		var inner error
		excluded, inner = copyVolumesLocked(ctx, client, ui, tx)
		return inner
	})
	if err != nil {
		return nil, err
	}
	return excluded, nil
}
```

- Change `copyVolumesLocked` to return `([]string, error)`: every `return nil` becomes `return nil, nil`, `return err` becomes `return nil, err`, and the tail becomes:

```go
	if err := finalizeMigration(tx, m, ui); err != nil {
		return nil, err
	}
	return m.Excluded, nil
```

- Remove the excluded `ui.Warnf` block from `finalizeMigration` (the notice now carries it); keep `finalizeMigration`'s signature and its `saveMigration(... completed)` call.

- [ ] **Step 4: Restructure `Offer` and `finalizeCompletedCampaign` in `offer.go`**

- Add, right after reading the migration, the notice flag:

```go
	migration, _ := rootState.Migration(MigrationName)
	_, noticeAcknowledged := rootState.Migration(LegacyRuntimeNoticeName)
	if migration.Status == state.MigrationCompleted || migration.Status == state.MigrationDismissed {
		return showNotice(noticeAcknowledged, nil, ui)
	}
```

- Change the nothing-to-copy branch to capture the excluded names and fall through:

```go
	var excluded []string
	if len(names) == 0 && len(unclear) == 0 {
		if migration.Status == state.MigrationCopying {
			excluded, err = finalizeCompletedCampaign(ui)
			if err != nil {
				return err
			}
		}
		return showNotice(noticeAcknowledged, excluded, ui)
	}
```

- Change the copy arm to use `copyVolumesCampaign` and the dismiss arm to fall through to the notice:

```go
	switch choice {
	case choiceCopy:
		excluded, err = copyVolumesCampaign(ctx, client, ui)
		if err != nil {
			return fmt.Errorf("copying legacy home volumes: %w", err)
		}
	case choiceFresh:
		if err := dismissMigration(ctx, client); err != nil {
			return err
		}
	default:
		return ErrMigrationPending
	}
	return showNotice(noticeAcknowledged, excluded, ui)
```

- Change `finalizeCompletedCampaign` to return `([]string, error)`:

```go
func finalizeCompletedCampaign(ui termio.UI) ([]string, error) {
	var excluded []string
	_, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		m, _ := tx.State().Migration(MigrationName)
		if m.Status != state.MigrationCopying {
			return nil
		}
		excluded = m.Excluded
		return finalizeMigration(tx, m, ui)
	})
	return excluded, err
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/runtimemigration/ -v`
Expected: PASS. `TestFinalizeMigrationWarnsExcluded` has been deleted; every other existing test still passes.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/runtimemigration/copy.go internal/sandbox/runtimemigration/offer.go internal/sandbox/runtimemigration/offer_test.go internal/sandbox/runtimemigration/copy_test.go
git commit -m "feat(migration): show the legacy-runtime notice from Offer"
```

---

### Task 6: Docs, CHANGELOG, and brain catalog

**Files:**
- Create: `docs/install/upgrading.md`
- Modify: `docs/install.md` (link the new page)
- Modify: `docs/troubleshooting.md` (cross-link)
- Modify: `README.md` (positioning line)
- Modify: `CHANGELOG.md` (`[Unreleased]` → `### Added`)
- Modify: `.operator-shared/catalog.md` (list the new plan)

**Interfaces:**
- Consumes: the notice behavior from Tasks 1–5.
- Produces: user-facing docs.

- [ ] **Step 1: Create `docs/install/upgrading.md`**

Front matter and content (Jekyll links use `{% link %}` relative to `docs/`):

```markdown
---
title: Upgrading
description: "What changes when you upgrade agents-sandbox from 0.5.x or earlier, and how to clean up the legacy ~/.microsandbox afterwards."
layout: default
parent: Install
nav_order: 30
---

# Upgrading

## From 0.5.x or earlier

Earlier versions used your system-wide microsandbox (`~/.microsandbox`) as the
runtime. agents-sandbox now provisions its own microsandbox runtime under its
data directory (`~/.local/share/agents-sandbox/msb`, or
`$XDG_DATA_HOME/agents-sandbox/msb`); it never reads or writes your own
`~/.microsandbox`, and a system-wide microsandbox is no longer required.

On the first interactive `run`/`shell` after upgrading, agents-sandbox offers
once to copy your old `~/.microsandbox/volumes/agents-sandbox-home-*` volumes
into its private home — see
[Legacy home volumes after upgrading]({% link troubleshooting.md %}#legacy-home-volumes-after-upgrading).
Once that is resolved, it shows a one-time notice explaining that your
system-wide microsandbox is now optional. The notice is informational only:
agents-sandbox never deletes `~/.microsandbox`.

`~/.microsandbox` holds two different things:

- **Ours**, safe to remove:
  - `~/.microsandbox/volumes/agents-sandbox-home-*`
  - `~/.microsandbox/sandboxes/agents-sandbox-vm-*`
  - `~/.microsandbox/sandboxes/agents-sandbox-task-*`
- **Yours** — any other microsandbox data. agents-sandbox never touches it.
  Remove the whole `~/.microsandbox` only if microsandbox was installed solely
  for agents-sandbox.

`agents-sandbox doctor` reports the private runtime home and its location.
```

- [ ] **Step 2: Link the new page from `docs/install.md`**

Add to the list:

```markdown
- [Upgrading]({% link install/upgrading.md %})
```

- [ ] **Step 3: Cross-link from `docs/troubleshooting.md`**

In the "Microsandbox runtime" section, after the sentence that ends "expected, not a defect.", add:

```markdown
If you upgraded from 0.5.x or earlier, see
[Upgrading]({% link install/upgrading.md %}) for what changed and how to clean
up the old `~/.microsandbox`.
```

- [ ] **Step 4: Add the README positioning line**

In `README.md`, near the microsandbox description, add one sentence stating that a system-wide microsandbox is not required (agents-sandbox provisions its own isolated runtime).

- [ ] **Step 5: Add the CHANGELOG entry**

Under `## [Unreleased]` → `### Added`:

```markdown
- After the one-time legacy home-volume migration, an interactive `run`/`shell` now shows a one-time notice that
  agents-sandbox runs its own microsandbox runtime and a system-wide microsandbox is no longer required. It names
  which parts of `~/.microsandbox` are ours and safe to remove (and gives the commands) versus your own data, is
  acknowledged with a keypress and never shown again, prints without acknowledgment in non-interactive sessions, and
  never deletes anything. See the new [Upgrading](https://inoio.github.io/agents-sandbox/install/upgrading.html) page.
```

- [ ] **Step 6: List the plan in the shared catalog**

In `.operator-shared/catalog.md`, under `### plans/ - TDD implementation plans`, add:

```markdown
- `2026-10-19-post-migration-legacy-runtime-notice.md`
  - Description: TDD plan for the `runtime-home-decoupling.md` "Post-migration legacy-runtime notice": `termio.Acknowledge`, the `acknowledged` migration status, prefix-based legacy-artifact detection, notice rendering/acknowledgment (`legacy_runtime_notice`), and wiring all four trigger points into `runtimemigration.Offer`.
  - Read If: Implementing or reviewing issue #207, the post-migration notice, or `termio.Acknowledge`.
```

- [ ] **Step 7: Verify docs and run the full check**

Run: `make check`
Expected: fmt, lint, tests, and `ci/check-docs.sh` all pass.

- [ ] **Step 8: Commit**

```bash
git add docs/install/upgrading.md docs/install.md docs/troubleshooting.md README.md CHANGELOG.md .operator-shared/catalog.md
git commit -m "docs: document the post-migration legacy-runtime notice"
```

---

## Self-Review

- **Spec coverage:** detection (Task 3), notice content incl. selective/wholesale commands and the excluded paragraph (Task 4), once-ever + non-interactive (Tasks 4–5), all four trigger points (Task 5), the `Acknowledge` primitive (Task 1), docs/CHANGELOG/doctor discoverability (Task 6). `doctor` already reports the private home, so no task is needed for it.
- **Type consistency:** `CopyVolumes` keeps `error`; `copyVolumesCampaign` and `finalizeCompletedCampaign` return `([]string, error)` for the excluded names; `showNotice(acknowledged bool, excluded []string, ui termio.UI) error` is used identically at every call site.
- **Open question resolved as spec'd:** no in-product re-arm of the terminal acknowledgment; a future re-arm is a status change in the registry, not a new field.
