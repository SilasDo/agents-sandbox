# Prefix-Free Artifact Naming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Names and microsandbox references of project VMs, task sandboxes, home volumes and msb runner images drop the `agents-sandbox-`/`agents-sandbox/runner-` prefix while Docker images keep theirs, with existing artifacts still found and never rebuilt or reimported.

**Architecture:** `internal/sandbox/naming` becomes the single owner of both naming schemes (new constructors + `Legacy…` constructors + parsers that accept either) and of sandbox kind classification. Callers create under the new scheme and resolve existing artifacts by trying the new form then the legacy form (sandbox/image) or by the recorded name (volume). Artifact discovery stops using the prefix as an ownership marker: the private runtime home makes ownership positional, and a task label (plus a numeric-suffix name heuristic) replaces the prefix for the project-vs-task split.

**Tech Stack:** Go 1.26, Cobra/Viper CLI, microsandbox Go SDK v0.7.8, `stretchr/testify`, table-driven tests.

**Spec:** `.operator-shared/specs/artifact-naming.md`

**Status:** Implemented on `artifact-naming` (PR #277), all 7 tasks. A whole-branch review (`reviewer-large`)
found two Important defects, fixed before the PR: (1) `ensureProjectVM` reused the found legacy handle's
name on the recreate path, so a replaced legacy VM came back under its legacy name — now reset to
`projectVMName(k)` when the replace reports it gone; (2) `state.ImageReferences` reported only the
prefix-free ref after `RunnerImageRef` changed, contrary to the spec's "consider both refs" — the
extractor now returns the prefix-free and the prefixed (`DockerImageRef`) ref, and
`projectStateReferences` takes a multi-name extractor. Minors: pinned the longest task-sandbox name
(Review Focus 5), the cross-scheme `sameImageIdentity` tolerance, and the recreated-VM name.
Deliberately unchanged: `refuseNonRunnerRefs`'s `ParseImageTag` shape check (a hash-shape tightening
would break the fictional test slugs and the fuzz round-trip property; a foreign ref in the private
home always fails not-found) and `pruning/state.go`'s name-only task heuristic (every task sandbox we
create carries both the label and the digit suffix).

A second, adversarial red-team review then found a real parser regression: `findHashSuffix` anchored
on the *first* hash-shaped segment, so a folder whose own last segment is 14 alphanumerics
(`go-1234567890abcd-<hash>`) shadowed the real slug hash, and the prefix-free home-volume parser (new
here) mis-parsed it, defeating `volume.checkForActiveVMs`. Fixed by scanning right-to-left for the
rightmost 14-alnum run that is a whole hyphen-delimited segment — a naive last match is wrong, because
a task sandbox's 19-digit nanos run also holds a `-`-preceded 14-alnum window. Pinned by regression
tests and a new `FuzzArtifactNameRoundTrip` (the pre-existing fuzz only covered `ParseImageTag`, which
is why the bug slipped through). The adversarial review's other findings were accepted as positional
ownership (`python:3.12`-shaped no-slash refs) or unproven/unreachable (mixed-version concurrency,
transient `ImageInspect` errors).

Greptile then raised two P1s, both real and reproduced. (1) A leftover stopped task sandbox put its
project's real `(slug, agent)` key in `ToPrune` while the running project VM put the same key in
`ToKeep`, so the next auto-prune reclaimed the live project's runner image and an unrecorded saved
home volume. Fixed by moving task sandboxes out of the key maps into `PruneState.Tasks` (reclaimed by
`PruneSandboxes` only). (2) A project whose folder begins with a legacy prefix produced a prefix-free
name that starts with that prefix, which `ParseVMName`/`ParseHomeVolumeName` stripped by prefix alone,
yielding a garbage slug (or an empty one). Fixed by the clean-slate label scheme plus the anchored
legacy-strip rule. The user chose the label-based identity (not just a `type` marker): every created
sandbox now carries `org.agents-sandbox.type` (`project`/`task`), `.slug` and `.agent`, resolved by
`naming.SandboxIdentity` (labels first, name fallback), consumed by `vm/list.go`,
`pruning/state.go`, `volume/operations.go` and `image/references.go`. Legacy labels are read, not
written.

A second Greptile round raised one P2: `findHashSuffix`'s rightmost-segment anchor made a legacy
digest-named home volume (`agents-sandbox-home-<folder>-<hash>-<digest>`, both 14-char base36) parse
its digest *as* the project hash, folding the digest into the slug so `pruneHomeVolumes` could not
match the saved volume to its stale project and left it on disk. Fixed in `legacyDigestName`: when
the final hash-shaped segment has nothing after it and a hash-shaped segment directly before it, the
right segment is the digest and the left one the project hash.

## Global Constraints

- Target name shapes (exact): project VM `<slug>-<agent>`; task sandbox `<slug>-<agent>-<nanos>`; home volume `<slug>-<agent>-<ts>`; msb runner image ref `<slug>:<agent>-latest`; Docker runner image `agents-sandbox/runner-<slug>:<agent>-latest` (unchanged).
- Legacy shapes to keep readable: `agents-sandbox-vm-<slug>-<agent>`, `agents-sandbox-task-<slug>-<agent>-<nanos>`, `agents-sandbox-home-<slug>-<agent>-<ts>`, `agents-sandbox/runner-<slug>:<agent>-latest`.
- `internal/sandbox/naming` stays dependency-free (only `strings`) — it must not import `options`, `state`, or `msb`.
- Labels: project VMs already carry `org.agents-sandbox.project`/`.image`; add `org.agents-sandbox.task` for task sandboxes. The label is authoritative; the trailing all-digit `nanos` segment is the name-level fallback.
- Every added/changed production line must be executed by a test (`specs/test-coverage.md`); run `make fmt && make lint && make test` (and `make compile-all`) before a task is done.
- Use `make fmt`/`golangci-lint fmt` and `make lint`; never `go fmt`/`go vet`.
- If executing in the shared `/workspace` bind mount, do not commit/checkout/stash; surface each task's diff to the user and ask how to finalize. The `git commit` steps apply only in an isolated worktree.

## Review Focus

Inputs/conditions the spec implies that a reasonable person would expect to keep working; each is pinned by the task that owns it.

1. A project whose home volume, msb image ref and project VM all predate the change: all three are found and reused (volume resolved, image not reimported while its digest matches, VM not recreated). → Task 5/2.
2. Hyphenated agent names (`claude-code`): volume and sandbox names must parse back to `agent="claude-code"`, not `"code"`. → Task 1/4.
3. Two agents for one slug (`opencode`, `pi`) and two checkouts of one repo: names, keys and discovery stay distinct. → Task 2/3/4.
4. `image remove` of a nonexistent or foreign reference still fails cleanly instead of deleting nothing silently. → Task 6.
5. A task sandbox name at the 64-byte hostname limit (long slug + `claude-code` + 19-digit nanos): creation must not error and the name must not be truncated into another artifact's namespace. → Task 3.

---

### Task 1: `naming` — prefix-free constructors, dual-scheme parsers, legacy predicates

**Files:**
- Modify: `internal/sandbox/naming/naming.go`, `internal/sandbox/naming/artifact.go`, `internal/sandbox/naming/labels.go`
- Test: `internal/sandbox/naming/naming_test.go`, `internal/sandbox/naming/artifact_test.go` (create), `internal/sandbox/naming/labels_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (all in package `naming`):
  - `Prefix = "agents-sandbox"`; `DockerImagePrefix = Prefix + "/runner-"`; `LegacyVMPrefix`, `LegacyTaskPrefix`, `LegacyHomePrefix` (= `Prefix + "-vm-"`, `Prefix + "-task-"`, `Prefix + "-home-"`).
  - `ProjectVMName(slug, agent string) string` → `slug + "-" + agent`.
  - `LegacyProjectVMName(slug, agent string) string` → `LegacyVMPrefix + slug + "-" + agent`.
  - `TaskSandboxName(slug, agent string, nanos int64) string` → `slug + "-" + agent + "-" + strconv.FormatInt(nanos, 10)`.
  - `LegacyTaskSandboxName(slug, agent string, nanos int64) string`.
  - `HomeVolumeName(slug, agent, timestamp string) string` → `slug + "-" + agent + "-" + timestamp`.
  - `LegacyHomeVolumeName(slug, agent, timestamp string) string`.
  - `RunnerImageRef(slug, agent string) string` → `slug + ":" + agent + "-latest"`.
  - `LegacyRunnerImageRef(slug, agent string) string` and `DockerImageRef(slug, agent string) string` → `DockerImagePrefix + slug + ":" + agent + "-latest"`.
  - `ArtifactInfo` gains field `Task bool`.
  - `ParseVMName(name string) ArtifactInfo` — strips `LegacyVMPrefix`/`LegacyTaskPrefix` if present, else the whole name; extracts slug via `findHashSuffix`, then the agent; sets `Task` when the segment after the hash ends in an all-digit `nanos`.
  - `ParseHomeVolumeName(name string) ArtifactInfo` — strips `LegacyHomePrefix` if present; slug = `<folder>-<hash>` via `findHashSuffix`; agent = the middle segments between hash and the trailing timestamp (empty for a legacy agent-less volume); legacy digest form still recognised.
  - `ParseImageTag(name string) ArtifactInfo` — strips `DockerImagePrefix` if present; `CutLast(":")`; `-latest` tag → agent, otherwise digest.
  - `IsLegacyProjectSandbox`, `IsLegacyTaskSandbox`, `IsLegacyLauncherSandbox`, `IsLegacyHomeVolume(name string) bool` — prefix-based, for the foreign `~/.microsandbox` scan only.
  - `LabelTask = "org.agents-sandbox.task"` in `labels.go`.

- [ ] **Step 1: Write failing tests**

In `naming_test.go` / `artifact_test.go`, table-driven, using `testify/require` equal on the struct:

```go
func TestProjectVMNameIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1abc-1jkl-opencode", ProjectVMName("myproj-1abc-1jkl", "opencode"))
	require.Equal(t, "agents-sandbox-vm-myproj-1abc-1jkl-opencode",
		LegacyProjectVMName("myproj-1abc-1jkl", "opencode"))
}

func TestTaskSandboxNameIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1abc-1jkl-opencode-1729000000000000000",
		TaskSandboxName("myproj-1abc-1jkl", "opencode", 1729000000000000000))
}

func TestHomeVolumeNameIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1abc-1jkl-opencode-20260812T123456",
		HomeVolumeName("myproj-1abc-1jkl", "opencode", "20260812T123456"))
	require.Equal(t, "agents-sandbox-home-myproj-1abc-1jkl-opencode-20260812T123456",
		LegacyHomeVolumeName("myproj-1abc-1jkl", "opencode", "20260812T123456"))
}

func TestRunnerImageRefIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1abc-1jkl:opencode-latest", RunnerImageRef("myproj-1abc-1jkl", "opencode"))
	require.Equal(t, "agents-sandbox/runner-myproj-1abc-1jkl:opencode-latest",
		DockerImageRef("myproj-1abc-1jkl", "opencode"))
}

func TestParseVMNameBothSchemes(t *testing.T) {
	cases := []struct{ name string; want ArtifactInfo }{
		{"myproj-1abc-1jkl-opencode", ArtifactInfo{Slug: "myproj-1abc-1jkl", Agent: "opencode"}},
		{"agents-sandbox-vm-myproj-1abc-1jkl-opencode", ArtifactInfo{Slug: "myproj-1abc-1jkl", Agent: "opencode"}},
		{"myproj-1abc-1jkl-opencode-1729000000000000000",
			ArtifactInfo{Slug: "myproj-1abc-1jkl", Agent: "opencode", Task: true}},
		{"myproj-1abc-1jkl-claude-code", ArtifactInfo{Slug: "myproj-1abc-1jkl", Agent: "claude-code"}},
		{"myproj-1abc-1jkl-claude-code-1729000000000000000",
			ArtifactInfo{Slug: "myproj-1abc-1jkl", Agent: "claude-code", Task: true}},
	}
	for _, tc := range cases { require.Equal(t, tc.want, ParseVMName(tc.name), tc.name) }
}

func TestParseHomeVolumeNameHyphenatedAgent(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: "myproj-1abc-1jkl", Agent: "claude-code"},
		ParseHomeVolumeName("myproj-1abc-1jkl-claude-code-20260812T123456"))
	require.Equal(t, ArtifactInfo{Slug: "myproj-1abc-1jkl", Agent: "claude-code"},
		ParseHomeVolumeName("agents-sandbox-home-myproj-1abc-1jkl-claude-code-20260812T123456"))
}

func TestParseImageTagBothSchemes(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: "myproj-1abc-1jkl", Agent: "opencode"},
		ParseImageTag("myproj-1abc-1jkl:opencode-latest"))
	require.Equal(t, ArtifactInfo{Slug: "myproj-1abc-1jkl", Agent: "opencode"},
		ParseImageTag("agents-sandbox/runner-myproj-1abc-1jkl:opencode-latest"))
}
```

Note: `myproj-1abc-1jkl` is a 19-char fiction standing in for `<folder>-<hash>`; `findHashSuffix` needs a real 14-char base36 hash, so use e.g. `myproj-1abc1234defg5` (7+1+14) in the fixtures. Pick one fixture and use it everywhere; adjust `want` accordingly.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/naming/...`
Expected: FAIL / compile error (`ProjectVMName` undefined, `Task` field missing).

- [ ] **Step 3: Implement the naming API**

Add the constants, constructors, parsers and legacy predicates per Interfaces above. Reuse `findHashSuffix`/`isBase36Hash`/`isHomeTimestamp`. `ParseHomeVolumeName` must anchor the slug on `findHashSuffix` (fixing the hyphenated-agent bug) and the timestamp (or legacy digest) on the right. Keep `Prefix = "agents-sandbox"`. Remove `ArtifactFor` if no production caller remains after Tasks 2–6; keep it until then.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/naming/... && go test ./internal/sandbox/naming/... -run Fuzz -fuzz FuzzRunnerImageRefRoundTrip -fuzztime 10s`
Expected: PASS; extend the existing `FuzzRunnerImageRefRoundTrip` to assert the prefix-free form round-trips.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/naming
git commit -m "feat(naming): add prefix-free constructors and dual-scheme parsers"
```

---

### Task 2: Project VMs — prefix-free names, lookup fallback, discovery

**Files:**
- Modify: `internal/sandbox/vm/vm_name.go`, `internal/sandbox/vm/vm_lifecycle.go`, `internal/sandbox/vm/run_orchestrate.go`, `internal/sandbox/vm/reconfig.go`, `internal/sandbox/vm/vm_control.go`, `internal/sandbox/vm/list.go`, `internal/sandbox/vm/remove.go`, `internal/sandbox/vm/adopt.go`, `internal/sandbox/volume/operations.go`
- Test: `internal/sandbox/vm/vm_name_test.go` (create if absent), `internal/sandbox/vm/list_test.go`, `internal/sandbox/vm/remove_test.go`, `internal/sandbox/volume/operations_test.go`

**Interfaces:**
- Consumes: Task 1's `naming.ProjectVMName`, `LegacyProjectVMName`, `ParseVMName`, `LabelTask`.
- Produces (package `vm`):
  - `projectVMName(k state.Key) string` → truncates `naming.ProjectVMName(k.Slug, k.Agent)` to `options.MaxSandboxNameLen` (keep the slug-truncating logic from today).
  - `legacyProjectVMName(k state.Key) string` → same truncation on `naming.LegacyProjectVMName`.
  - `findProjectVM(ctx context.Context, client msb.Client, k state.Key) (msb.SandboxHandle, bool, error)` — `GetSandbox(projectVMName(k))`, then `GetSandbox(legacyProjectVMName(k))`; `(handle, true, nil)` on either, `(nil, false, nil)` on not-found, error otherwise.
  - `isTaskSandbox(name string, labels map[string]string) bool` → `labels[naming.LabelTask] != "" || naming.ParseVMName(name).Task`.

- [ ] **Step 1: Write failing tests**

```go
func TestProjectVMNameIsPrefixFreeAndTruncated(t *testing.T) {
	require.Equal(t, "myproj-1abc1234defg5-opencode", projectVMName(state.Key{Slug: "myproj-1abc1234defg5", Agent: "opencode"}))
	require.LessOrEqual(t, len(projectVMName(state.Key{Slug: strings.Repeat("a", 200), Agent: "opencode"})), options.MaxSandboxNameLen)
}

func TestFindProjectVMFallsBackToLegacyName(t *testing.T) {
	// mock: GetSandbox(new)=not-found, GetSandbox(legacy)=handle
	handle, found, err := findProjectVM(ctx, mock, k)
	require.NoError(t, err); require.True(t, found)
	require.Equal(t, naming.LegacyProjectVMName(k.Slug, k.Agent), handle.Name())
}

func TestListSandboxesHidesTaskSandboxesBothSchemes(t *testing.T) {
	// mock returns a project VM named "myproj-1abc1234defg5-opencode",
	// a task sandbox labelled LabelTask, and a legacy task sandbox
	// "agents-sandbox-task-..."; expect only the project VM.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/vm/... ./internal/sandbox/volume/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

Replace name construction with `projectVMName`/`legacyProjectVMName`. Route every `client.GetSandbox(ctx, projectVMName(k))` used for *existence* through `findProjectVM` (`vm_lifecycle.go`, `run_orchestrate.go:396`, `reconfig.go:144`, `vm_control.go:30`, `adopt.go:460`, and the legacy-VM destroy in `adopt.go`). In `vm/list.go`, drop the `HasPrefix(name, naming.VMPrefix)` filter and instead skip `isTaskSandbox`; read `Config().Labels` for the label check (already read). In `vm/remove.go`, accept a name that parses as a project VM under either scheme (`naming.ParseVMName(name).Slug != "" && !…Task`) instead of the prefix check. In `volume/operations.go:29` `checkForActiveVMs`, replace the `naming.VMPrefix` filter with `!isTaskSandbox(...)` (using `Config().Labels`) and keep the existing key comparison via `naming.ParseVMName`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/vm/... ./internal/sandbox/volume/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/vm internal/sandbox/volume/operations.go
git commit -m "feat(vm): prefix-free project VM names with legacy lookup fallback"
```

---

### Task 3: Task sandboxes — prefix-free names, task label, prune classification

**Files:**
- Modify: `internal/sandbox/volume/volume.go`, `internal/sandbox/volume/operations.go`, `internal/sandbox/pruning/state.go`
- Test: `internal/sandbox/volume/volume_test.go`, `internal/sandbox/pruning/state_test.go`

**Interfaces:**
- Consumes: `naming.TaskSandboxName`, `naming.LegacyTaskSandboxName`, `naming.LabelTask`, `naming.ParseVMName`.
- Produces: `volume.TaskSandboxName(k state.Key, nanos int64) string` (thin wrapper over `naming.TaskSandboxName`), and a `msbSdk.WithLabels(map[string]string{naming.LabelTask: "1"})` option at each task-sandbox creation site.

- [ ] **Step 1: Write failing tests**

```go
func TestPrefillSandboxNameIsPrefixFreeAndLabelled(t *testing.T) {
	// assert the name passed to CreateSandbox == naming.TaskSandboxName(slug, agent, nanos)
	// and that the CreateSandbox options include a label map with LabelTask.
}

func TestPruneStateClassifiesTaskSandboxesWithoutPrefix(t *testing.T) {
	// handles: "myproj-1abc1234defg5-opencode-1729" (task, digits) and
	//          "myproj-1abc1234defg5-opencode" (project); expect the task in ToPrune
	//          (when inactive) and the project in ToKeep.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/volume/... ./internal/sandbox/pruning/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

Change the three creation sites (`volume/volume.go:55` prefill, `volume/volume.go:200` copy, `volume/operations.go:144` edit) to `naming.TaskSandboxName(k.Slug, k.Agent, time.Now().UnixNano())` and add the `LabelTask` option. In `pruning/state.go`, replace `naming.IsLauncherSandbox(name)` with "always true" (all sandboxes in the private home are launcher sandboxes) and replace `naming.IsTaskSandbox(name)`/`naming.IsProjectSandbox(name)` with `naming.ParseVMName(name).Task` (name heuristic) so no extra `Config()` call is needed; a handle whose `ParseVMName` slug is empty is skipped.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/volume/... ./internal/sandbox/pruning/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/volume internal/sandbox/pruning/state.go
git commit -m "feat(volume): prefix-free task sandbox names with task label"
```

---

### Task 4: Home volumes — prefix-free names, discovery, prune

**Files:**
- Modify: `internal/sandbox/volume/volume.go`, `internal/sandbox/volume/list.go`, `internal/sandbox/pruning/volumes.go`
- Test: `internal/sandbox/volume/volume_test.go`, `internal/sandbox/volume/list_test.go`, `internal/sandbox/pruning/volumes_test.go`

**Interfaces:**
- Consumes: `naming.HomeVolumeName`, `naming.ParseHomeVolumeName`.
- Produces: `volume.HomeVolumeName(k state.Key) string` now returns the prefix-free form (`naming.HomeVolumeName(k.Slug, k.Agent, time.Now().UTC().Format("20060102T150405"))`).

- [ ] **Step 1: Write failing tests**

```go
func TestHomeVolumeNameIsPrefixFree(t *testing.T) {
	got := HomeVolumeName(state.Key{Slug: "myproj-1abc1234defg5", Agent: "opencode"})
	require.NotContains(t, got, naming.LegacyHomePrefix)
	require.Regexp(t, `^myproj-1abc1234defg5-opencode-\d{8}T\d{6}$`, got)
}

func TestListVolumesShowsBothSchemes(t *testing.T) {
	// mock ListVolumes returns a legacy-prefixed volume and a prefix-free one;
	// expect both in the result, including the prefix-free one.
}

func TestPruneVolumesClassifiesPrefixFreeAndLegacy(t *testing.T) {
	// both a legacy-prefixed and a prefix-free volume parse to the same key and
	// are pruned for a ToPrune key.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/volume/... ./internal/sandbox/pruning/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

Update `HomeVolumeName` to the prefix-free form. In `volume/list.go`, drop `HasPrefix(name, naming.HomePrefix)` and list all volumes (the private home holds only ours) whose `naming.ParseHomeVolumeName(name).Slug != ""`. In `pruning/volumes.go`, drop both `hasPrefix(…, naming.HomePrefix)` guards (lines 50, 78) and rely on `naming.ParseHomeVolumeName` (empty slug → skip). `HomeState.HomeVolume` is unchanged, so existing volumes keep resolving by their recorded name.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/volume/... ./internal/sandbox/pruning/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/volume internal/sandbox/pruning/volumes.go
git commit -m "feat(volume): prefix-free home volume names"
```

---

### Task 5: Image reference split and msb ref resolution

**Files:**
- Modify: `internal/sandbox/image/build.go`, `internal/sandbox/image/image.go`, `internal/sandbox/image/tags.go`, `internal/sandbox/image/inspect.go`, `internal/sandbox/vm/vm_lifecycle.go`, `internal/sandbox/volume/volume.go`, `internal/sandbox/volume/operations.go`, `internal/sandbox/vm/run_orchestrate.go`
- Test: `internal/sandbox/image/image_test.go`, `internal/sandbox/image/build_test.go` (create if absent), `internal/sandbox/vm/vm_lifecycle_test.go`

**Interfaces:**
- Consumes: `naming.RunnerImageRef`, `naming.LegacyRunnerImageRef`, `naming.DockerImageRef`.
- Produces:
  - `ImageInfo{ DockerTag string; Ref string; Digest string; DockerfileID string; Env map[string]string }` (`Tag` removed).
  - `runnerTag(projectSlug, agentName string) string` → `naming.DockerImageRef(...)`.
  - `EnsureLoaded(ctx context.Context, mclient msb.Client, info ImageInfo, ui termio.UI) (ImageInfo, error)` — returns `info` with `Ref` set to the ref that holds the current content: if `mclient.ImageGet(ctx, naming.LegacyRunnerImageRef(slug, agent))` succeeds **and** the legacy content matches (`ImageInspect(legacyRef).Digest == info.Digest`, or the dockerfile-id label fallback when msb records no digest), keep `info.Ref = legacyRef`; otherwise use `info.Ref = naming.RunnerImageRef(slug, agent)` and load the Docker image there.
  - All `WithImage(...)`/`LabelImage` sites use the `Ref` returned by `EnsureLoaded`.

- [ ] **Step 1: Write failing tests**

```go
func TestEnsureLoadedReusesLegacyRefWhenContentMatches(t *testing.T) {
	// Docker image tagged agents-sandbox/runner-<slug>:opencode-latest, digest D.
	// Mock ImageGet(legacyRef)=ok, ImageInspect(legacyRef).Digest==D.
	// Expect ImageLoad NOT called and returned info.Ref == legacyRef.
}

func TestEnsureLoadedLoadsUnderNewRefWhenLegacyAbsent(t *testing.T) {
	// ImageGet(legacyRef)=not-found. Expect ImageLoad(newRef, …) and info.Ref == newRef.
}

func TestCreateProjectVMUsesResolvedRef(t *testing.T) {
	// createProjectVM called with info.Ref == legacyRef must pass WithImage(legacyRef)
	// and set LabelImage to legacyRef.
}

func TestBuildDoesNotRebuildWhenDockerTagUnchanged(t *testing.T) {
	// runnerTag == agents-sandbox/runner-<slug>:opencode-latest (unchanged by this work).
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/image/... ./internal/sandbox/vm/...`
Expected: FAIL / compile error (`ImageInfo.Tag` removed).

- [ ] **Step 3: Implement**

Split `ImageInfo`: rename `Tag`→`DockerTag`; add `Ref`. `EnsureImageWithClient` sets `DockerTag: rTag, Ref: naming.RunnerImageRef(projectSlug, a.Name())`. Change `EnsureLoaded` to return `(ImageInfo, error)` and resolve `Ref` as above (factor the digest/`dockerfileIDLabelKey` comparison in `cachedImageMatches` so it can be applied to the legacy ref too). Update the four `EnsureLoaded` callers to use the returned `info.Ref` for the following `WithImage`, `LabelImage` (`vm_lifecycle.go:514,520`), and the verbose log (`run_orchestrate.go:146`). `planRunnerImage`/`inspectRunnerImage`/`ExistingAgentVersion` keep using the Docker tag via `runnerTag`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/image/... ./internal/sandbox/vm/... ./internal/sandbox/volume/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image internal/sandbox/vm internal/sandbox/volume
git commit -m "feat(image): split Docker tag from msb ref and reuse legacy refs"
```

---

### Task 6: Image discovery and reference scans without a prefix

**Files:**
- Modify: `internal/sandbox/image/list.go`, `internal/sandbox/image/remove.go`, `internal/sandbox/image/references.go`, `internal/sandbox/state/references.go`, `internal/sandbox/pruning/images.go`, `internal/sandbox/vm/adopt.go`
- Test: `internal/sandbox/image/list_test.go`, `internal/sandbox/image/remove_test.go`, `internal/sandbox/pruning/images_test.go`, `internal/sandbox/state/references_test.go`

**Interfaces:**
- Consumes: `naming.ParseImageTag`, `naming.RunnerImageRef`, `naming.DockerImageRef`, Task 1's parsers.
- Produces: `image.sandboxKey(name string) state.Key` uses `naming.ParseVMName` (not `ArtifactFor`).

- [ ] **Step 1: Write failing tests**

```go
func TestListImagesIncludesPrefixFreeRefs(t *testing.T) {
	// mock ImageList: "myproj-1abc1234defg5:opencode-latest", a legacy
	// "agents-sandbox/runner-…" ref, and "docker.io/library/foo:1" (no hash →
	// unparseable); expect the two runner refs, not the foreign one.
}

func TestRemoveImagesRefusesUnparseableRef(t *testing.T) {
	// "docker.io/library/foo:1" → error; "myproj-1abc1234defg5:opencode-latest" → proceeds.
}

func TestImageReferencesPreferNewRefButKeepLegacyLatest(t *testing.T) {
	// state.ImageReferences() returns the prefix-free ref for a live key; the
	// prune shape rule still keeps a live slug's legacy "-latest" ref.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/image/... ./internal/sandbox/pruning/... ./internal/sandbox/state/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

`image/list.go`: drop the `ImagePrefix` filter; include a ref when `naming.ParseImageTag(ref).Slug != ""`. `image/remove.go`: replace `refuseNonRunnerRefs`'s prefix test with a `naming.ParseImageTag(ref).Slug == ""` test (error message updated to name the offending refs; no longer overridable by `--force`, as before). `image/references.go`: `sandboxKey` uses `naming.ParseVMName`. `state/references.go` `ImageReferences` returns `naming.RunnerImageRef(k.Slug, k.Agent)`. `pruning/images.go`: drop `hasPrefix(ref, naming.ImagePrefix)` and use `naming.ParseImageTag(ref).Slug != ""`. `vm/adopt.go`: the notice's image rows use `naming.DockerImageRef`. Remove `naming.ArtifactFor` and its tests once no caller remains.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox
git commit -m "feat(image): discover prefix-free runner images; drop prefix ownership"
```

---

### Task 7: Legacy foreign scan, docs, CHANGELOG

**Files:**
- Modify: `internal/sandbox/runtimemigration/legacy.go`, `CHANGELOG.md`, `docs/how-it-works.md`, `docs/sandboxes.md`, `docs/runner-image.md`
- Test: `internal/sandbox/runtimemigration/legacy_test.go`

**Interfaces:**
- Consumes: `naming.IsLegacyHomeVolume`, `naming.IsLegacyLauncherSandbox`.
- Produces: nothing new.

- [ ] **Step 1: Write failing test**

```go
func TestLegacyScanMatchesOnlyPrefixedArtifacts(t *testing.T) {
	// ~/.microsandbox fixtures: "agents-sandbox-home-…" and "agents-sandbox-vm-…"
	// are matched; "myproj-1abc1234defg5-opencode" and a foreign "theirs" are not.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/runtimemigration/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

Switching `legacy.go` to the `IsLegacy…` predicates. Update the docs: `how-it-works.md` (the home-volume example name), `sandboxes.md` (the volume/sandbox name examples), `runner-image.md` (the msb reference vs the Docker tag). Add a CHANGELOG `[Unreleased]` line describing the prefix-free names and the Docker-vs-msb reference split.

- [ ] **Step 4: Run the full check**

Run: `make fmt && make lint && make test && make compile-all && ci/check-docs.sh`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/runtimemigration CHANGELOG.md docs
git commit -m "docs: record the prefix-free artifact naming strategy"
```

---

## Self-review notes

- Spec coverage: target scheme (Tasks 1–5), Docker/msb split (Task 5), write-new/read-both (Tasks 2, 4, 5), ownership-vs-classification (Tasks 2, 3, 6), legacy foreign scan + docs/CHANGELOG (Task 7). `runtimemigration` is the only remaining name-shape caller.
- The plan leaves the exact truncation helper in `vm` (it needs `options.MaxSandboxNameLen`, which `naming` must not import).
- Execution method: not yet chosen — ask before starting.
