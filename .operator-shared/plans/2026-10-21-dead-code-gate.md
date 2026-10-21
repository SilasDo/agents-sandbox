# Dead-code gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the repo fail CI when a production function is unreachable from any `main` package and carries no reason-bearing marker, and clear the day-one baseline so the gate ships green.

**Architecture:** A pinned `golang.org/x/tools/cmd/deadcode` run per build-tag configuration, whose JSON is fed to a new Go checker (`ci/deadcodecheck`) that resolves `//deadcode:ignore` markers through the Go AST. The checker owns the pass/fail decision, because deadcode always exits 0. A POSIX-sh wrapper (`ci/check-deadcode.sh`, in the `ci/check-docs.sh` style) runs the matrix; a Makefile target and a CI job expose it. Baseline resolution is two-part: mark intentional test seams and test-support files, delete what is actually dead.

**Tech Stack:** Go 1.26 (`go/parser`, `go/ast`, `go/token`), POSIX sh, `jq`-free JSON via `encoding/json`, Make, GitHub Actions.

**Spec:** `.operator-shared/specs/dead-code-gate.md` (read it; the plan argues from it). Tool survey: `.operator-shared/guides/dead-code-detection-go.md`.

**Isolation:** implement on a branch in a dedicated worktree, created with the `using-git-worktrees` skill. Do not edit `/workspace` directly.

## Global Constraints

- `DEADCODE_VERSION = v0.52.0` — a `Makefile` constant, invoked as `go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION)`. Never a globally installed binary. Not visible to dependabot; documented in `.agents/skills/release/NON-GO-DEPS.md`.
- Marker spelling is exactly `//deadcode:ignore reason="…"`. The `reason` is **required and non-empty**. Two scopes, distinguished by AST position, not by spelling: in the package doc comment group (file scope), or in a declaration's associated comment groups — doc block or trailing comment (declaration scope). Anything else is an unusable marker and fails.
- deadcode exits 0 whether or not it finds anything. Never `set -e` on it expecting failure; never treat an empty finding set as clean unless the analysis itself succeeded.
- A non-zero deadcode exit, a non-JSON payload, or a truncated document is a **hard error** (exit 2), never "no findings".
- The gate runs the host's own `GOOS` (no pin, no cross toolchain). `GOARCH` is not pinned. No `integration`-only or `msb`-only non-test file exists today, so all four tag configurations report the same 249 findings; the matrix is retained as future-proofing.
- Tag matrix: `''`, `integration`, `msb`, `integration msb`. The gate fails if **any** configuration reports an unmarked finding.
- Names come from the spec and the triage below verbatim: `ci/deadcodecheck` (package `main`), `ci/check-deadcode.sh`, `ci/test-check-deadcode.sh`, `make deadcode`, `VerifyMode`-free — do not rename.
- `make fmt` / `make lint` (`golangci-lint`) after every edit cluster, never `go vet` / `go fmt`. `make check` before the final commit.
- Keep `CHANGELOG.md` `[Unreleased]` and the docs in sync; `ci/check-docs.sh` runs inside `make check`.

## Review Focus

The spec implies these; no task's happy path exercises them, so each gets a test in its owning task.

1. A bare `//deadcode:ignore` with no `reason=`, one with `reason=""`, one on a `type`/`var`, and two on one declaration must all fail — the first three as unusable, the pair as ambiguous — rather than silently excusing a finding (Task 2).
2. A marker in a `_test.go` file must fail as stale rather than being ignored (Task 3).
3. deadcode returning non-zero, an empty body, a JSON object instead of an array, or a truncated array must exit 2, never 0 (Task 1, Task 4).
4. A function reported dead in the default configuration but live under `integration` is still a failure (union across configurations), and a single marker must suppress it in every configuration (Task 3, Task 4).
5. A `PKG=`-scoped run must not report markers elsewhere in the module as stale (Task 3, Task 4).

---

### Task 1: Report parsing

**Files:**
- Create: `ci/deadcodecheck/main.go`
- Test: `ci/deadcodecheck/main_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Finding struct { Package, Function, File string; Line, Column int; Source string }`; `func ParseReport(data []byte) ([]Finding, error)`. `Source` is left empty by `ParseReport` and set by the CLI to the report file's base name, so a diagnosis can name the configuration that produced it.

- [ ] **Step 1: Write the failing test**

```go
func TestParseReport(t *testing.T) {
	// deadcode -json shape: [{Name, Path, Funcs:[{Name, Position:{File,Line,Col}}]}]
	data := []byte(`[{"Name":"image","Path":"github.com/inoio/agents-sandbox/internal/sandbox/image",
		"Funcs":[{"Name":"WithMockAgentVersion","Position":{"File":"internal/sandbox/image/fetch.go","Line":26,"Col":6}}]}]`)
	got, err := ParseReport(data)
	// want exactly one Finding{Package: ".../internal/sandbox/image", Function: "WithMockAgentVersion",
	//   File: "internal/sandbox/image/fetch.go", Line: 26, Column: 6}
}

func TestParseReportRejectsFailedAnalysis(t *testing.T) {
	// table: "" ; "null" ; "{}" ; `[{"Path":"p"}]` (no Funcs key) ; `[{"Path":"p","Funcs":[{"Position":{}}]}]` (no File)
	// every case: err != nil and got == nil, so an empty finding set can never be
	// mistaken for a successful analysis that found nothing.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./ci/deadcodecheck/ -run TestParseReport -v`
Expected: FAIL, `undefined: ParseReport`

- [ ] **Step 3: Implement `ParseReport`**

Decode into a local struct mirroring deadcode's JSON schema; reject `null`, a non-array, and any entry missing `Path`, `Funcs`, or a `Funcs[].Position.File`. A `Funcs` of length zero is valid (a package with no findings). Sort the result by `File`, then `Line`, so output and tests are stable.

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test ./ci/deadcodecheck/ -run TestParseReport -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add ci/deadcodecheck/main.go ci/deadcodecheck/main_test.go
git commit -m "feat(ci): parse deadcode JSON reports"
```

---

### Task 2: Marker scanning

**Files:**
- Modify: `ci/deadcodecheck/main.go`
- Test: `ci/deadcodecheck/main_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Scope int` with `ScopeDeclaration`, `ScopeFile`; `type Marker struct { File string; Line int; Reason string; Scope Scope; SiteLine int }`; `func ScanMarkers(path string, src []byte) ([]Marker, error)`.

The marker line is `^\s*//\s*deadcode:ignore\s+reason="(.+)"\s*$` — the whole comment text must match, the reason must be non-empty. For a `ScopeDeclaration` marker, `SiteLine` is the declaration's own line, so it can be matched against a `Finding.Line`.

- [ ] **Step 1: Write the failing test**

```go
func TestScanMarkers(t *testing.T) {
	// one fixture source per case, run through ScanMarkers:
	//  - marker as the package doc comment            -> ScopeFile (SiteLine 0)
	//  - marker as a func's doc comment               -> ScopeDeclaration, SiteLine == decl line
	//  - marker as a trailing comment on a one-line func -> ScopeDeclaration, SiteLine == decl line
	//  - marker above a type / var                    -> error (unusable)
	//  - marker separated from the func by a blank line -> error (unusable)
	//  - two markers on one declaration               -> error (ambiguous)
	//  - `//deadcode:ignore` (no reason) and `reason=""` -> error (unusable)
	//  - `//deadcode:ignore reason="x"` followed by other prose on the same line -> error
}

func TestScanMarkersInTestFileIsNotSpecial(t *testing.T) {
	// a marker in a _test.go file must still return a Marker (Task 3 rejects it as stale);
	// ScanMarkers itself must not silently drop it.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./ci/deadcodecheck/ -run TestScanMarkers -v`
Expected: FAIL, `undefined: ScanMarkers`

- [ ] **Step 3: Implement `ScanMarkers`**

Parse with `parser.ParseFile(fset, path, src, parser.ParseComments)`; build `ast.NewCommentMap(fset, file, file.Comments)`. The file-scope site is the comment group immediately preceding the `package` keyword (`file.Doc`). Every other marker must fall in the comment groups `cmap` associates with a `*ast.FuncDecl`; associate it with that declaration's line. Record the source line of each marker for diagnostics.

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test ./ci/deadcodecheck/ -run TestScanMarkers -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add ci/deadcodecheck/main.go ci/deadcodecheck/main_test.go
git commit -m "feat(ci): resolve //deadcode:ignore markers through the AST"
```

---

### Task 3: The check and the command

**Files:**
- Modify: `ci/deadcodecheck/main.go`
- Test: `ci/deadcodecheck/main_test.go`

**Interfaces:**
- Consumes: `ParseReport`, `ScanMarkers`, `Finding`, `Marker`, `Scope`.
- Produces: `type Diagnosis struct { Unmarked []Finding; Stale []Marker; Unusable []Marker }`; `func (d Diagnosis) Empty() bool`; `func (d Diagnosis) Report() string`; `func Check(findings []Finding, markers []Marker, checkStale bool) Diagnosis`.

A finding matches a `ScopeDeclaration` marker when `File` and `Line == SiteLine` agree, and a `ScopeFile` marker when only `File` agrees. `Stale` is every marker matching no finding, and is empty when `checkStale` is false; `Unusable` markers from `ScanMarkers` errors are reported, not fatal, so one bad marker does not hide the rest. Findings are deduped by `File` and `Line` before matching, so the same function dead in several configurations is reported once and named with every configuration that saw it. `Report()` prints the `Source` configuration for each entry.

CLI contract, also exercised through the built binary in `ci/test-check-deadcode.sh` (Task 4): `-reports <dir>` (each regular file is one `ParseReport` payload, named after its configuration), `-scope <all|single>` (default `all`; `single` passes `checkStale=false` for a `PKG=`-scoped inspection, Review Focus 5), module-relative source paths on stdin one per line, exit 0 clean / 1 diagnosis / 2 hard error.

- [ ] **Step 1: Write the failing test**

```go
func TestCheck(t *testing.T) {
	f := Finding{File: "a.go", Line: 10, Function: "Dead", Column: 6, Source: "integration+msb.json"}
	//  - no markers, checkStale true     -> Unmarked == [f], Empty() == false
	//  - decl marker Line 10             -> Empty() == true
	//  - file marker for a.go            -> Empty() == true
	//  - decl marker Line 11             -> Stale == [that marker], Unmarked == [f]
	//  - union across configs: the same File/Line twice with two Sources -> one entry
}

func TestCheckScopedSkipsStale(t *testing.T) {
	// the Line 11 marker above with checkStale=false stays out of Stale and Empty()
	// is true when the finding itself is marked.
}

func TestDiagnosisReportNamesTheTools(t *testing.T) {
	// Report() mentions "unreachable func: Dead", its Source, and the marker's
	// file:line for every stale and unusable entry, so a red build is actionable
	// without rerunning.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./ci/deadcodecheck/ -run 'TestCheck|TestDiagnosis' -v`
Expected: FAIL, `undefined: Check`

- [ ] **Step 3: Implement `Check` and `main`**

`main` reads `-reports` (required) and `-scope`, globs the directory in sorted order, `ParseReport`s each file and stamps `Source`, reads module-relative source paths from stdin one per line, `ScanMarkers` each, `Check`, prints `Diagnosis.Report()`, and exits 1 when non-empty. Exit 2 with a message on stderr for a missing `-reports`, an unreadable report or source file, an unknown `-scope`, or a `ParseReport`/`ScanMarkers` error.

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test ./ci/deadcodecheck/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add ci/deadcodecheck/main.go ci/deadcodecheck/main_test.go
git commit -m "feat(ci): decide the dead-code gate and expose it as a command"
```

---

### Task 4: The script and its test

**Files:**
- Create: `ci/check-deadcode.sh`, `ci/test-check-deadcode.sh`
- Test: `ci/test-check-deadcode.sh`

**Interfaces:**
- Consumes: the `ci/deadcodecheck` CLI (`-reports <dir>`, source paths on stdin, exit 0/1/2).
- Produces: `ci/check-deadcode.sh`, read by `make deadcode` (Task 5) and by `ci/test-check-deadcode.sh`.

Environment the script reads: `DEADCODE_VERSION` (required, from the Makefile), `PKG` (default `./...`), `TAGS` (default: the four-configuration matrix; when set, a single configuration). Test seams, documented as test-only in a comment: `DEADCODE_FIXTURE_DIR` (report JSON per configuration instead of running the tool) and `DEADCODE_FILES` (a file listing source paths instead of `git ls-files '*.go'`). The checker is always invoked as `go run ./ci/deadcodecheck` from the module root, so the test must not change the working directory.

- [ ] **Step 1: Write the failing test**

```sh
# ci/test-check-deadcode.sh, in the ci/test-check-docs.sh style: build a temp
# fixture tree, run ci/check-deadcode.sh against it, assert the exit code and
# the message. Cases, one assertion each:
#  - marked finding, all four configs      -> exit 0
#  - unmarked finding in one config only   -> exit 1, message names the func and config
#  - stale marker                          -> exit 1, message names file:line
#  - malformed report JSON                 -> exit 2, not 1 and not 0
#  - DEADCODE_FILES listing a file that does not exist -> exit 2
#  - TAGS='' (no tags) fixture present, integration fixture absent -> exit 0,
#    proving a single-configuration run requests only that configuration
```

- [ ] **Step 2: Run test to verify it fails**

Run: `ci/test-check-deadcode.sh`
Expected: FAIL, `ci/check-deadcode.sh: not found`

- [ ] **Step 3: Implement `ci/check-deadcode.sh`**

`set -eu`; require `DEADCODE_VERSION`. Build the configuration list (matrix or `$TAGS`) and, per configuration, write the JSON into a `mktemp -d` report directory; run the pinned tool with `-json -tags "$tags" "$PKG"` and abort with exit 2 unless it exits 0. Then feed the source-path list on stdin to `go run ./ci/deadcodecheck -reports "$report_dir"` and propagate its status. `PKG`-scoped runs pass `-scope` so the checker skips the stale-marker pass and says so (Review Focus 6).

- [ ] **Step 4: Run test to verify it passes**

Run: `ci/test-check-deadcode.sh`
Expected: PASS, one line per case

- [ ] **Step 5: Commit**

```bash
git add ci/check-deadcode.sh ci/test-check-deadcode.sh
git commit -m "feat(ci): add the dead-code gate script and its tests"
```

---

### Task 5: The target

**Files:**
- Modify: `Makefile` (constants block near `GOVULNCHECK_VERSION`; `.PHONY` list; a new target after `vulncheck`)

**Interfaces:**
- Consumes: `ci/check-deadcode.sh`.
- Produces: `make deadcode`, `DEADCODE_VERSION`.

`make deadcode` is **not** added to `verify` in this task — the baseline is still red. Task 10 wires it.

- [ ] **Step 1: Add the constant and target**

```make
DEADCODE_VERSION = v0.52.0
```

Add `deadcode` to `.PHONY`, and after `vulncheck`:

```make
# Hard gate: production code unreachable from any main package must be deleted or
# carry a //deadcode:ignore marker with a reason. deadcode always exits 0, so
# ci/check-deadcode.sh decides. Never silently skipped: a failed analysis exits 2.
#   make deadcode
#   make deadcode PKG=./internal/foo    # inspection only; stale markers not checked
#   make deadcode TAGS=integration      # one configuration instead of the matrix
deadcode:
	DEADCODE_VERSION='$(DEADCODE_VERSION)' PKG='$(PKG)' TAGS='$(TAGS)' ci/check-deadcode.sh
```

`PKG` and `TAGS` need defaults: `PKG ?= ./...` already exists for `mutation`; add `TAGS ?=`.

- [ ] **Step 2: Verify it runs and fails on the unmarked baseline**

Run: `make deadcode 2>&1 | tail -20`
Expected: non-zero, with the 249 baseline findings listed. This is the measurement every later task reduces.

Measured on this branch's base (`origin/main`, e17a06e): 249 findings — 228 in the ten test-support files, 21 scattered across 16 files. Locate the declarations marked in later tasks by symbol, not by this plan's line numbers, which shift with every merged PR: on this base `agent.Register` is at `agent.go:52`, `vm.SetRuntimeHomeMigrationOffer` at `run_orchestrate.go:87`, and `viperconfig.NewResolverWithConfig` at `viperconfig.go:365`.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "feat(ci): add the make deadcode target"
```

---

### Task 6: File-scope markers for the test-support files

**Files:**
- Modify: `internal/sandbox/msb/testmock.go`, `internal/termio/mock.go`, `internal/sandbox/docker/testmock.go`, `internal/configpaths/testmock.go`, `test/harness/pty.go`, `test/harness/harness.go`, `internal/sandbox/doctor/testmock.go`, `internal/testutil/testutil.go`, `internal/testutil/testgit.go`, `internal/testutil/mocks/testutil.go`

**Interfaces:**
- Consumes: the marker contract from Task 2.
- Produces: nothing other tasks consume.

These ten files hold 228 of the 249 findings. Each gets one marker in its package doc comment group, immediately above the `package` clause, reason naming what the file is:

```go
//deadcode:ignore reason="test support: referenced only by _test.go files"
package msb
```

If a file already has a package doc comment, append the marker as the last line of that group.

- [ ] **Step 1: Add the marker to all ten files**

- [ ] **Step 2: Verify the count drops to 21**

Run: `make deadcode 2>&1 | tail -30`
Expected: non-zero, 21 findings remaining, none of them in the ten files above.

- [ ] **Step 3: Verify nothing else changed**

Run: `CGO_ENABLED=1 go test ./... 2>&1 | tail -5`
Expected: PASS (comments only)

- [ ] **Step 4: Commit**

```bash
git add internal/sandbox/msb/testmock.go internal/termio/mock.go internal/sandbox/docker/testmock.go internal/configpaths/testmock.go test/harness/pty.go test/harness/harness.go internal/sandbox/doctor/testmock.go internal/testutil/testutil.go internal/testutil/testgit.go internal/testutil/mocks/testutil.go
git commit -m "chore: mark test-support files for the dead-code gate"
```

---

### Task 7: Declaration markers for the intentional seams

**Files:**
- Modify: `internal/agent/agent.go`, `internal/git/identity.go`, `internal/viperconfig/viperconfig.go`, `internal/sandbox/docker/docker.go`, `internal/sandbox/image/fetch.go`, `internal/sandbox/vm/daemon.go`, `internal/sandbox/vm/run_orchestrate.go`

**Interfaces:**
- Consumes: the marker contract from Task 2.
- Produces: nothing other tasks consume.

Nine declarations. Each reason must name the actual justification; the strings below are the reviewed ones, use them verbatim:

| Declaration | reason |
|---|---|
| `agent.Register` (agent.go:52) | `"internal registry extension point; only registry tests register stubs"` |
| `git.RepoSlug` (identity.go:99) | `"cwd-derived convenience for tests; production reads the ResolveIdentity fields"` |
| `git.CheckoutSlug` (identity.go:104) | `"cwd-derived convenience for tests; production reads the ResolveIdentity fields"` |
| `viperconfig.NewResolverWithConfig` (viperconfig.go:365) | `"construction seam for cmd tests; see doc comment"` |
| `docker.RealClient` (docker.go:58) | `"integration tests opt back into the real docker daemon"` |
| `image.WithMockAgentVersion` (fetch.go:26) | `"test seam establishing a pinned agent version"` |
| `image.WithMockAgentVersionResolver` (fetch.go:34) | `"test seam establishing a custom agent version resolver"` |
| `vm.SetDaemonShellFunc` (daemon.go:146) | `"test seam replacing the daemon shell"` |
| `vm.SetRuntimeHomeMigrationOffer` (run_orchestrate.go:87) | `"test seam replacing the runtime migration offer"` |

- [ ] **Step 1: Add the nine markers**

Use the declaration's doc comment where one exists; otherwise a comment line immediately above it.

- [ ] **Step 2: Verify the count drops to 12**

Run: `make deadcode 2>&1 | tail -20`
Expected: non-zero, 12 findings remaining, none of the seven above.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/agent.go internal/git/identity.go internal/viperconfig/viperconfig.go internal/sandbox/docker/docker.go internal/sandbox/image/fetch.go internal/sandbox/vm/daemon.go internal/sandbox/vm/run_orchestrate.go
git commit -m "chore: mark intentional test seams for the dead-code gate"
```

---

### Task 8: Delete the superseded homeconfig wrappers

**Files:**
- Modify: `internal/homeconfig/homeconfig.go`
- Test: `internal/homeconfig/homeconfig_test.go`, `internal/homeconfig/homeconfig_extra_test.go`, `internal/sandbox/vm/prepare_sandbox_test.go`, `internal/sandbox/reprovision/coverage_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing — the three functions have no callers outside tests.

These are thin wrappers over the functions production actually uses, kept so tests can call a simpler signature. The tests move to the production functions rather than being deleted.

| Delete | File:line | Tests that use it | Retarget to |
|---|---|---|---|
| `MergeManifests` | homeconfig.go:176 | homeconfig_test.go | drop the test if it only asserted the wrapper; otherwise merge through `LoadLayers`/`BuildHomeFilesWithModes` |
| `BuildHomeFiles` | homeconfig.go:293 | homeconfig_test.go, homeconfig_extra_test.go, vm/prepare_sandbox_test.go, reprovision/coverage_test.go | `BuildHomeFilesWithModes`, taking `.Data` from each `HomeFile` |
| `DescribeManifest` | homeconfig.go:378 | homeconfig_test.go, homeconfig_extra_test.go | `LoadLayers` + `DescribeLayers` |

- [ ] **Step 1: Delete `MergeManifests` and retarget its test**

Run: `go build ./... && CGO_ENABLED=1 go test ./internal/homeconfig/`

- [ ] **Step 2: Delete `BuildHomeFiles` and retarget its four test files**

Run: `go build ./... && CGO_ENABLED=1 go test ./internal/homeconfig/ ./internal/sandbox/vm/ ./internal/sandbox/reprovision/`

- [ ] **Step 3: Delete `DescribeManifest` and retarget its two test files**

Run: `go build ./... && CGO_ENABLED=1 go test ./internal/homeconfig/`

- [ ] **Step 4: Verify no test was weakened**

Read the diff of the retargeted tests: each assertion must still compare the same production-produced value it did before, only reached a different way. Deleting an assertion instead of retargeting it is a failure of this task.

- [ ] **Step 5: Commit**

```bash
git add internal/homeconfig internal/sandbox/vm/prepare_sandbox_test.go internal/sandbox/reprovision/coverage_test.go
git commit -m "refactor: drop homeconfig wrappers superseded by their production twins"
```

---

### Task 9: Delete the remaining dead functions

**Files:**
- Modify: `internal/sandbox/runtime/runtime.go`, `internal/upgrade/upgrade.go`, `internal/termio/termio.go`, `internal/sandbox/image/dockerfile.go`, `internal/sandbox/reprovision/config_files.go`, `internal/sandbox/state/root_txn.go`, `internal/sandbox/volume/prompt.go`, `internal/notify/event.go`
- Test: `internal/sandbox/runtime/runtime_test.go`, `internal/upgrade/upgrade_test.go`, `internal/termio/coverage_extra_test.go`, `internal/notify/event_extra_test.go`, `internal/sandbox/volume/volume_test.go`, `internal/sandbox/state/root_txn_test.go`, `internal/sandbox/reprovision/config_files_test.go`, `internal/sandbox/reprovision/config_compare_test.go`, `internal/sandbox/vm/setupsandbox_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing — these symbols have no callers outside tests.

| Delete | File:line | Tests that use it | Retarget to |
|---|---|---|---|
| `InspectRuntime` | runtime.go:107 | runtime_test.go | delete every `TestInspectRuntime*` — the feature (runtime version-matching recovery) was retired; no replacement exists |
| `IsNewer` | upgrade.go:193 | upgrade_test.go | delete `TestIsNewerExportedWrapper`; `isNewer` keeps its own coverage |
| `OutToVerboseRedirect` + `Out` + `Outf` | termio.go:145-152 | termio/coverage_extra_test.go | delete `TestOutToVerboseRedirectOut`/`Outf`; `cmd`'s `autoPruneOutToVerboseRedirect` is the live implementation and keeps `TestAutoPruneOutToVerboseRedirect` |
| `imageIdentity.values` | dockerfile.go:124 | image tests construct `imageIdentity` but never call it | no retarget |
| `AgentConfigPath` | config_files.go:154 | reprovision tests (oracle), vm/setupsandbox_test.go | assert against the production-produced `ConfigFile.MergedPath` value, or a literal |
| `RootTxn.Exists` | root_txn.go:28 | state/root_txn_test.go | no retarget; `RootTxn` itself stays |
| `VolumeAction.String` | prompt.go:22 | volume_test.go | delete `TestVolumeActionString`; `actionLabel` is the live formatter |
| `HandleEventType` | event.go:116 | notify/event_extra_test.go | delete the test; `Tracker.Handle` is the live entry point |

- [ ] **Step 1: Delete row by row, keeping the build green**

After each row: `go build ./... && CGO_ENABLED=1 go test ./<package>/`.

- [ ] **Step 2: Verify the whole module**

Run: `CGO_ENABLED=1 go test ./... 2>&1 | tail -20`
Expected: PASS. A failure here means a symbol had a caller deadcode could not see — restore it and report it instead of forcing the deletion.

- [ ] **Step 3: Verify the gate is green**

Run: `make deadcode; echo "exit=$?"`
Expected: `exit=0`

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor: remove functions unreachable from any main package"
```

---

### Task 10: Wire the gate in

**Files:**
- Modify: `Makefile` (the `verify` target), `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `make deadcode`.
- Produces: the CI job name `deadcode`.

- [ ] **Step 1: Add the prerequisite**

`verify:` gains `deadcode` after `vulncheck`. Leave `check` untouched.

- [ ] **Step 2: Add the CI job**

After the `vulncheck` job, a `deadcode` job with `runs-on: ubuntu-latest` and the standard `checkout` + `setup-go` (`go-version-file: .go-version`, `cache: true`) steps, then `- run: make deadcode`. Do **not** add it to `release`'s `needs:`.

- [ ] **Step 3: Verify locally**

Run: `make verify 2>&1 | tail -20`
Expected: PASS end to end.

- [ ] **Step 4: Verify the gate actually fails**

Temporarily add an unreachable function to any `internal/` package, run `make deadcode`, confirm exit 1 with the function named, then revert.

- [ ] **Step 5: Commit**

```bash
git add Makefile .github/workflows/ci.yml
git commit -m "ci: gate pull requests on the dead-code check"
```

---

### Task 11: Documentation and brain

**Files:**
- Modify: `CHANGELOG.md` (`[Unreleased]`), `CONTRIBUTING.md` (Testing), `.agents/skills/release/NON-GO-DEPS.md`, `.operator-shared/specs/dead-code-gate.md`, `.operator-shared/catalog.md`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing.

- [ ] **Step 1: `CONTRIBUTING.md`**

Under Testing: what `make deadcode` enforces, how to write a marker and when, that `PKG=` skips the stale pass, and that the gate runs in CI on every PR but not on release.

- [ ] **Step 2: `CHANGELOG.md`**

An `[Unreleased]` entry: added `make deadcode` and the PR gate; removed 12 internal functions unreachable from any `main` package (internal only, no user-visible behavior change).

- [ ] **Step 3: `.agents/skills/release/NON-GO-DEPS.md`**

A `deadcode` row with `DEADCODE_VERSION = v0.52.0` and the note that dependabot cannot see it, alongside the existing `gomutants`/`govulncheck` rows.

- [ ] **Step 4: Spec status**

In `.operator-shared/specs/dead-code-gate.md`: mark it implemented on this branch, resolve the "Open items" that this plan decided — the marker is resolved through the Go AST rather than by line arithmetic, spelling is `//deadcode:ignore` with the scope taken from position, and the reason is delimited by double quotes — and record the triage outcome (12 deleted, 7 marked, 10 file-scope markers). Keep it in the present tense.

- [ ] **Step 5: `make check` and commit**

Run: `make check`
Expected: PASS

```bash
git add CHANGELOG.md CONTRIBUTING.md .agents/skills/release/NON-GO-DEPS.md .operator-shared/specs/dead-code-gate.md .operator-shared/catalog.md
git commit -m "docs: document the dead-code gate"
```
