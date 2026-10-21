# State by ownership scope Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fold the last two feature-named launcher-global state sources (`upgrade.json`, and the legacy top-level `agents:` key) into an `updates:` namespace in root `state.yaml`, silently and backwards-compatibly.

**Architecture:** `internal/sandbox/state` reshapes `RootState` to carry `Updates{Launcher, Agents}` and folds the legacy top-level `agents:` key during its pure parse step. `internal/upgrade` stops owning a file: it gains a small `Store` interface (`Load`/`Save`) and a one-time `upgrade.json` fold that writes through that store, so it never imports `internal/sandbox/state`. `cmd` wires a root-state-backed `Store` and it is exercised only on the non-dry-run upgrade-check path.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3` (100% — it honours an `IsZero() bool` method for `omitempty`), standard library `encoding/json` for the legacy file, `internal/state`'s `WithRootState` transaction, `internal/configpaths` for path resolution.

**Spec:** `.operator-shared/specs/state-by-scope.md` (issue #141).

## Global Constraints

- Module `github.com/inoio/agents-sandbox`, Go 1.26. Follow idiomatic Go and the repo's style; run `make lint` after edits and `make check` at the end.
- Never add `//exhaustruct:ignore` unless the linter asks; `RootState` already carries one.
- New test packages/`TestMain` mocks: tests that touch state must isolate paths with `configpaths.WithMockConfigPaths(t)` — never real host state (`specs/test-isolation.md`).
- Root-state writes go only through `WithRootState`; a corrupt document is never clobbered.
- `--dry-run` writes nothing. `checkForUpgrade` is already gated by `!isDryRun` (`cmd/agents-sandbox/commands_cli.go:131`), so the fold inherits that; do not add a separate guard.
- Field name is `last_checked` everywhere; `upgrade.json`'s `last_check` is legacy only.
- Commit after each task with a conventional-commit message.

## Review Focus

These are the input classes the spec implies but a "happy path" test would miss; each gets its test in the owning task.

1. **An old-binary `state.yaml`** (top-level `agents:`) must fold into `updates.agents`, not vanish. — Task 2.
2. **A corrupt or empty `upgrade.json`** must fold nothing, be removed, and not crash. — Task 4.
3. **A `upgrade.json` whose `Save` fails or whose root lock is contended** must be kept for a later retry (no data loss, no prompt storm). — Task 4.
4. **Both a legacy `upgrade.json` and a populated `updates.launcher`** must union dismissals and let the present `last_checked` win. — Task 4.
5. **A contended root lock during `Check`** must defer (silent verbose note, empty `Result`), never block startup or clobber state. — Tasks 3 and 5.

---

### Task 1: Reshape `RootState` with the `updates:` namespace

**Files:**
- Modify: `internal/sandbox/state/root.go` (struct + accessors, lines ~14-106)
- Modify: `internal/sandbox/state/root_test.go` (fixtures at lines ~251, ~292, ~324)
- Modify: `internal/sandbox/state/root_txn_test.go:55`
- Modify: `internal/sandbox/vm/upgrade_migration_test.go:36,131`
- Modify: `internal/sandbox/vm/upgrade_test.go:555,599,755,859,885,920,1035`
- Test: `internal/sandbox/state/updates_test.go` (create)

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `type Updates struct { Launcher LauncherUpdateState \`yaml:"launcher,omitempty"\`; Agents map[string]AgentCheckState \`yaml:"agents,omitempty"\` }`
  - `func (u Updates) IsZero() bool`
  - `type LauncherUpdateState struct { LastChecked time.Time \`yaml:"last_checked"\`; DismissedVersions []string \`yaml:"dismissed_versions,omitempty"\` }`
  - `func (l LauncherUpdateState) IsZero() bool`
  - `RootState.Updates Updates \`yaml:"updates,omitempty"\`` (the `Agents` field is removed)
  - `func (s *RootState) AgentCheck(name string) AgentCheckState` and `SetAgentCheck(name string, c AgentCheckState)` (now read/write `Updates.Agents`)
  - `func (s *RootState) LauncherUpdate() LauncherUpdateState` and `SetLauncherUpdate(l LauncherUpdateState)`

- [ ] **Step 1: Write the failing tests** in `internal/sandbox/state/updates_test.go`

```go
package state

import "testing"

func TestAgentCheckLivesUnderUpdates(t *testing.T) {
	if err := writeRootState(RootState{Updates: Updates{Agents: map[string]AgentCheckState{
		"opencode": {LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRootState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Updates.Agents["opencode"].LatestVersion != "2.0.0" {
		t.Fatalf("Updates.Agents = %+v", got.Updates.Agents)
	}
}

func TestRootStateMarshalsUpdatesKey(t *testing.T) {
	rs := &RootState{}
	rs.SetLauncherUpdate(LauncherUpdateState{DismissedVersions: []string{"1.5.2"}})
	rs.SetAgentCheck("opencode", AgentCheckState{LatestVersion: "2.0.0"})
	data, err := marshalRootState(*rs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"updates:", "launcher:", "dismissed_versions:", "agents:", "last_checked:"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("marshalled root state missing %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "\nagents:") { // no top-level agents key
		t.Fatalf("root state still has a top-level agents: key:\n%s", data)
	}
}

func TestRootStateOmitsEmptyUpdates(t *testing.T) {
	data, err := marshalRootState(RootState{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "updates:") {
		t.Fatalf("empty root state should omit updates:\n%s", data)
	}
}

func TestLauncherUpdateAccessorsRoundTrip(t *testing.T) {
	rs := &RootState{}
	if !rs.LauncherUpdate().IsZero() {
		t.Fatal("zero RootState should have a zero LauncherUpdate")
	}
	rs.SetLauncherUpdate(LauncherUpdateState{DismissedVersions: []string{"2.0.0"}})
	if got := rs.LauncherUpdate().DismissedVersions; len(got) != 1 || got[0] != "2.0.0" {
		t.Fatalf("LauncherUpdate = %+v", got)
	}
}
```

Add `"strings"` to the import block.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/state/ -run 'Updates|LauncherUpdate|AgentCheck' -v`
Expected: FAIL — `undefined: Updates`, `undefined: LauncherUpdateState`, `rs.SetLauncherUpdate undefined`.

- [ ] **Step 3: Reshape `root.go`**

Replace the `RootState` struct body: remove `Agents map[string]AgentCheckState` and add `Updates Updates \`yaml:"updates,omitempty"\``. Add the `Updates` and `LauncherUpdateState` types with their `IsZero` methods (yaml.v3's `IsZeroer` makes `omitempty` work on a value struct; `time.Time` is already handled). Retarget `AgentCheck`/`SetAgentCheck` to `s.Updates.Agents`, and add `LauncherUpdate`/`SetLauncherUpdate` reading/writing `s.Updates.Launcher`. Keep the existing doc comments' meaning; update them to say the keys live under `updates:` (agent checks) and `updates.launcher` (launcher self-upgrade, formerly `upgrade.json`).

- [ ] **Step 4: Update the compile-breaking fixtures**

In every listed test file, rewrite `RootState{Agents: map[string]AgentCheckState{...}}` to `RootState{Updates: Updates{Agents: map[string]AgentCheckState{...}}}` and `s.Agents["opencode"]` to `s.Updates.Agents["opencode"]`. Keep the seeded values identical.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/state/ ./internal/sandbox/vm/`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

```bash
make lint
git add internal/sandbox/state internal/sandbox/vm
git commit -m "refactor(state): group root state under an updates namespace"
```

---

### Task 2: Fold the legacy top-level `agents:` key

**Files:**
- Modify: `internal/sandbox/state/root.go` (`parseRootState`, ~line 138)
- Test: `internal/sandbox/state/updates_test.go`

**Interfaces:**
- Consumes: `Updates`, `LauncherUpdateState`, `AgentCheckState` from Task 1.
- Produces: `parseRootState` unchanged in signature, now normalising the legacy key.

- [ ] **Step 1: Write the failing tests** (append to `updates_test.go`)

```go
func TestParseRootStateFoldsLegacyAgentsKey(t *testing.T) {
	legacy := []byte("agents:\n  opencode:\n    last_checked: 2026-01-01T00:00:00Z\n    latest_version: 1.2.3\n")
	got, err := parseRootState(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if got.Updates.Agents["opencode"].LatestVersion != "1.2.3" {
		t.Fatalf("legacy agents key not folded: %+v", got.Updates)
	}
}

func TestParseRootStateNewShapeWinsOverLegacyKey(t *testing.T) {
	both := []byte("updates:\n  agents:\n    opencode:\n      latest_version: new\nagents:\n  opencode:\n    latest_version: old\n")
	got, err := parseRootState(both)
	if err != nil {
		t.Fatal(err)
	}
	if got.Updates.Agents["opencode"].LatestVersion != "new" {
		t.Fatalf("new shape did not win: %+v", got.Updates.Agents)
	}
}

func TestRootStateWriteDropsLegacyAgentsKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := []byte("agents:\n  opencode:\n    latest_version: 1.2.3\n")
	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootStateFile(), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := WithRootState(true, func(tx *RootTxn) error {
		tx.State().SetLauncherUpdate(LauncherUpdateState{DismissedVersions: []string{"1.0.0"}})
		return tx.Commit()
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(rootStateFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\nagents:") {
		t.Fatalf("legacy top-level agents key survived a write:\n%s", data)
	}
	if !strings.Contains(string(data), "opencode") {
		t.Fatalf("folded agent check was lost on write:\n%s", data)
	}
}
```

Add `"os"` to the import block; `configpaths` is already imported by the package's tests via other files — add it here if not.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/state/ -run 'LegacyAgents' -v`
Expected: FAIL — a legacy `agents:` key is not folded (the new-shape-wins and drop tests may vacuously pass; the first fails).

- [ ] **Step 3: Fold in `parseRootState`**

Unmarshal into an unexported wire struct that has both `Updates Updates \`yaml:"updates"\`` and `LegacyAgents map[string]AgentCheckState \`yaml:"agents"\``, then set `state.Updates.Agents = wire.LegacyAgents` only when `wire.Updates.Agents` is empty (present wins), and copy `wire.Updates` as-is otherwise. Return `RootState{Migrations: wire.Migrations, Updates: wire.Updates}`. Keep it pure; no I/O.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/state/ ./internal/sandbox/vm/`
Expected: PASS.

- [ ] **Step 5: Extend the fuzz seed corpus and run the fuzz target briefly**

Add a legacy-shaped seed to `internal/sandbox/state/fuzz_test.go`'s seed list if the target uses one; otherwise just run:
`go test ./internal/sandbox/state/ -run '^$' -fuzz '^FuzzParseState$' -fuzztime 15s`
Expected: no crashers.

- [ ] **Step 6: Commit**

```bash
make lint
git add internal/sandbox/state
git commit -m "refactor(state): fold the legacy top-level agents key into updates"
```

---

### Task 3: Replace `internal/upgrade`'s file state with a `Store` seam

**Files:**
- Create: `internal/upgrade/store.go`
- Modify: `internal/upgrade/upgrade.go` (Options ~line 89, `Check` ~line 122, `loadState`/`saveState`/`state` ~lines 102-113 and 439-462)
- Test: `internal/upgrade/store_test.go` (create), `internal/upgrade/upgrade_test.go`, `internal/upgrade/upgrade_extra_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks (independent package).
- Produces:
  - `type LauncherState struct { LastChecked time.Time; DismissedVersions []string }`
  - `type Store interface { Load() (LauncherState, error); Save(LauncherState) error }`
  - `Options.Store Store` (`Options.StatePath` removed)
  - an unexported in-memory `Store` used when `Options.Store` is nil (checking still works, nothing persists; the launcher always wires the real one)

- [ ] **Step 1: Write the failing tests**

In `internal/upgrade/store_test.go`:

```go
package upgrade

import "testing"

func TestMemoryStoreRoundTrips(t *testing.T) {
	var s memoryStore
	got, err := s.Load()
	if err != nil || !got.LastChecked.IsZero() || len(got.DismissedVersions) != 0 {
		t.Fatalf("empty load = %+v, %v", got, err)
	}
	want := LauncherState{DismissedVersions: []string{"2.0.0"}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err = s.Load()
	if err != nil || len(got.DismissedVersions) != 1 || got.DismissedVersions[0] != "2.0.0" {
		t.Fatalf("round-trip = %+v, %v", got, err)
	}
}
```

Replace `mockOptions` (`upgrade_test.go:43-52`) to use a fake store instead of `StatePath`:

```go
type fakeStore struct {
	state LauncherState
	err   error
}

func (f *fakeStore) Load() (LauncherState, error)  { return f.state, f.err }
func (f *fakeStore) Save(s LauncherState) error {
	f.state = s
	return f.err
}
```

and set `Store: &fakeStore{}` in the returned `Options`. Update the three `opts.StatePath` read/write sites (`upgrade_test.go:126-127,151,285-286`) to seed/inspect the fake store's `state` instead. Convert `TestCheckDefaultsIntervalAndStatePath` to `TestCheckDefaultsIntervalAndStore`: keep the `configpaths.WithMockConfigPaths(t)` call and assert a nil `Store` still checks without error.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/upgrade/ -run 'Store' -v`
Expected: FAIL — `undefined: memoryStore`, `LauncherState`, `Store`.

- [ ] **Step 3: Implement `store.go` and rewire `upgrade.go`**

Add `LauncherState`, `Store`, and `memoryStore` to `store.go` (with a `Store` doc comment naming `state.Store`'s root-state-backed implementation as the real one). In `upgrade.go`: add `Store Store` to `Options`; delete `StatePath`, the `state` struct, `loadState`, and `saveState`, and the `atomicwrite` import; add `func (o Options) store() Store { if o.Store == nil { return &memoryStore{} }; return o.Store }`. In `Check`, replace the `path := opts.statePath(); st := loadState(path)` block with `s := opts.store(); st, err := s.Load(); if err != nil { opts.UI.Verbosef("upgrade state unavailable: %v", err); return Result{}, nil }`, and replace `_ = saveState(path, st)` with `if err := s.Save(st); err != nil { opts.UI.Verbosef("could not persist upgrade state: %v", err) }`. Rename `st.LastCheck` to `st.LastChecked`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/upgrade/`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/upgrade
git commit -m "refactor(upgrade): persist launcher state through an injected store"
```

---

### Task 4: Fold legacy `upgrade.json` into `updates.launcher`

**Files:**
- Modify: `internal/upgrade/store.go` (add the fold), `internal/upgrade/upgrade.go` (`Check`)
- Test: `internal/upgrade/store_test.go`

**Interfaces:**
- Consumes: `Store`, `LauncherState` (Task 3); `configpaths` (already imported in the package).
- Produces: `func FoldLegacyState(s Store, ui termio.UI) error`; `const legacyStateFileName = "upgrade.json"`.

- [ ] **Step 1: Write the failing tests**

```go
func TestFoldLegacyStateCopiesAndRemoves(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"last_check":"2026-09-01T00:00:00Z","dismissed_versions":["1.5.2"]}`)
	s := &fakeStore{}
	if err := FoldLegacyState(s, &termio.Mock{}); err != nil {
		t.Fatal(err)
	}
	if s.state.LastChecked.IsZero() || len(s.state.DismissedVersions) != 1 {
		t.Fatalf("fold = %+v", s.state)
	}
	if _, err := os.Stat(legacyStatePath()); !os.IsNotExist(err) {
		t.Fatalf("legacy file not removed: %v", err)
	}
}

func TestFoldLegacyStateUnionsDismissalsAndKeepsPresentLastChecked(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"last_check":"2026-09-01T00:00:00Z","dismissed_versions":["1.5.2"]}`)
	present := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s := &fakeStore{state: LauncherState{LastChecked: present, DismissedVersions: []string{"2.0.0"}}}
	if err := FoldLegacyState(s, &termio.Mock{}); err != nil {
		t.Fatal(err)
	}
	if !s.state.LastChecked.Equal(present) {
		t.Fatalf("present last_checked overridden: %v", s.state.LastChecked)
	}
	if len(s.state.DismissedVersions) != 2 {
		t.Fatalf("dismissals not unioned: %+v", s.state.DismissedVersions)
	}
}

func TestFoldLegacyStateCorruptRemovedWithoutFolding(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, "{not json")
	s := &fakeStore{}
	if err := FoldLegacyState(s, &termio.Mock{}); err != nil {
		t.Fatal(err)
	}
	if !s.state.LastChecked.IsZero() || len(s.state.DismissedVersions) != 0 {
		t.Fatalf("corrupt file folded something: %+v", s.state)
	}
	if _, err := os.Stat(legacyStatePath()); !os.IsNotExist(err) {
		t.Fatalf("corrupt legacy file not removed: %v", err)
	}
}

func TestFoldLegacyStateKeepsFileOnSaveFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"dismissed_versions":["1.5.2"]}`)
	s := &fakeStore{err: errors.New("boom")}
	if err := FoldLegacyState(s, &termio.Mock{}); err == nil {
		t.Fatal("expected the save failure to surface")
	}
	if _, err := os.Stat(legacyStatePath()); err != nil {
		t.Fatalf("legacy file removed despite save failure: %v", err)
	}
}
```

Add `writeLegacy(t, body string)` (mkdir `UserStateDir` 0700, write `legacyStatePath()` 0600) and `errors`/`time`/`os`/`configpaths`/`termio` imports as needed. Note: `fakeStore.Save` must not mutate `f.state` when `f.err != nil`; adjust Task 3's fake if needed so the save-failure test is honest.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/upgrade/ -run 'FoldLegacy' -v`
Expected: FAIL — `undefined: FoldLegacyState`, `legacyStatePath`, `writeLegacy`.

- [ ] **Step 3: Implement `FoldLegacyState` and call it from `Check`**

Add to `store.go`: `legacyStatePath()` returning `filepath.Join(configpaths.Get().UserStateDir(), legacyStateFileName)`; a lenient JSON parse of `{last_check time.Time, dismissed_versions []string}`; `FoldLegacyState` reading the file (missing → nil), loading via `s.Load()`, filling `LastChecked` only when zero, unioning `DismissedVersions` (preserve order, dedupe), saving via `s.Save`, and removing the file only on a nil save error. A missing file or a successful fold returns nil; a corrupt file folds nothing but is still removed; a `Save` error returns the error and leaves the file.

In `Check`, call `if err := FoldLegacyState(s, opts.UI); err != nil { opts.UI.Verbosef("upgrade state migration deferred: %v", err) }` immediately after `s := opts.store()`, before `s.Load()` — so it runs only on the real (non-dev, non-homebrew, non-dry-run) check path.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/upgrade/`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/upgrade
git commit -m "refactor(upgrade): fold the retired upgrade.json into the root state"
```

---

### Task 5: Wire the root-state-backed store in `cmd`

**Files:**
- Create: `cmd/agents-sandbox/launcher_store.go`, `cmd/agents-sandbox/launcher_store_test.go`
- Modify: `cmd/agents-sandbox/commands_cli.go` (`checkForUpgrade`, ~line 158)

**Interfaces:**
- Consumes: `upgrade.Store`, `upgrade.LauncherState` (Task 3); `state.RootState.LauncherUpdate`/`SetLauncherUpdate` (Task 1); `state.WithRootState`.
- Produces: `type launcherStore struct{}` implementing `upgrade.Store`.

- [ ] **Step 1: Write the failing test**

```go
func TestLauncherStoreRoundTripsThroughRootState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	s := launcherStore{}
	if got, err := s.Load(); err != nil || !got.LastChecked.IsZero() {
		t.Fatalf("empty load = %+v, %v", got, err)
	}
	want := upgrade.LauncherState{DismissedVersions: []string{"2.0.0"}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DismissedVersions) != 1 || got.DismissedVersions[0] != "2.0.0" {
		t.Fatalf("round-trip = %+v", got)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatal(err)
	}
	if root.Updates.Launcher.DismissedVersions[0] != "2.0.0" {
		t.Fatalf("not persisted under updates.launcher: %+v", root.Updates)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/agents-sandbox/ -run 'LauncherStore' -v`
Expected: FAIL — `undefined: launcherStore`.

- [ ] **Step 3: Implement the adapter and wire it**

`launcherStore.Load()` opens `state.WithRootState(false, ...)`, copies `tx.State().LauncherUpdate()` into an `upgrade.LauncherState`, and errors when `ok` is false (the lock is held). `Save` opens the same transaction, calls `SetLauncherUpdate`, commits, and errors when `ok` is false. In `checkForUpgrade`, add `Store: launcherStore{}` to the `upgrade.Options` literal.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/agents-sandbox/ -run 'Upgrade|LauncherStore' -v`
Expected: PASS. (The existing `cli_upgrade_test.go` stubs `upgradeCheck`, so no host state is touched.)

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add cmd/agents-sandbox
git commit -m "refactor(cli): persist launcher upgrade state in the root state"
```

---

### Task 6: Docs, changelog, and spec sync

**Files:**
- Modify: `CHANGELOG.md` (Unreleased section)
- Modify: `.operator-shared/specs/upgrade-state-scoping.md` (the `RootState.agents` field path)
- Modify: `.operator-shared/catalog.md` (the `upgrade-state-scoping.md` entry's `RootState.agents` mention)
- Test: none (documentation)

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

- [ ] **Step 1: Add the CHANGELOG entry**

Under `## [Unreleased]` → `### Changed`, add a line in the repo's voice: launcher-global state now lives in one document, root `state.yaml`, under an `updates:` namespace (`updates.launcher` from the retired `upgrade.json`, `updates.agents` for agent update checks); both the old top-level `agents:` key and `upgrade.json` fold in silently on first run, and `upgrade.json`'s `last_check` becomes `last_checked`.

- [ ] **Step 2: Update the two brain references**

In `.operator-shared/specs/upgrade-state-scoping.md`, change the `RootState.agents` field path to `RootState.updates.agents` (semantics unchanged) wherever it names the field. In `.operator-shared/catalog.md`, make the same substitution in the `upgrade-state-scoping.md` entry's description. Do not restate the fold contract — that lives in `state-by-scope.md`.

- [ ] **Step 3: Run the full check**

Run: `make check`
Expected: PASS (fmt, lint, tests, docs linkcheck).

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md .operator-shared
git commit -m "docs: note the root-state updates namespace"
```

---

## Self-Review

**Spec coverage:** the `updates:` shape (Task 1), the `agents:` fold (Task 2), the `upgrade.json` fold with union/present-wins and save-failure behaviour (Task 4), the injected store replacing `Options.StatePath` (Tasks 3, 5), no deprecation-register entry (Task 6 records it in the changelog instead), and the invariants (dry-run inherited from the existing `!isDryRun` gate; corrupt-document and no-blocking defer covered by Tasks 3/5 handling of `Load`/`ok`). All covered.

**Type consistency:** `Updates.Launcher`/`Updates.Agents`, `LauncherUpdateState.LastChecked`/`DismissedVersions`, `LauncherState.LastChecked`/`DismissedVersions`, `Store.Load`/`Save`, and `launcherStore` are used with the same names and shapes across tasks.

**Proportion:** six tasks, mostly mechanical test-fixture updates plus two small new files; the plan states signatures and assertions rather than bodies.
