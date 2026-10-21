# Per-checkout Project Identity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split project identity into a repo-scoped slug (per-slug user config, sandbox grouping label) and a checkout-scoped slug (state, VM name, home volume, locks/claims, runner image tag), with an automatic, noticed migration of the legacy origin-keyed identity.

**Architecture:** `internal/git` gains one derivation pass (`ResolveIdentity`) that returns the workspace layout and both slugs; every cwd-derived command resolves identity through it. Adoption (destroy the legacy VM, then move the legacy state directory) lives in `internal/sandbox/vm` and runs from the same entry points, under the root-state flock. Prune gains a home-volume reference guard.

**Tech Stack:** Go 1.26, cobra, viper, go-git (config parsing only), microsandbox SDK (`internal/sandbox/msb`), testify.

**Spec:** `.operator-shared/specs/checkout-identity.md` — read it with this plan; the plan implements the spec and only pins the decisions the spec leaves to the builder.

## Global Constraints

- `internal/git` stays a **leaf package**: no imports of `internal/configpaths`, `internal/sandbox/*`, or anything that would make the derivation depend on state or config paths. This is what keeps `cmd`/`session`/`vm` cycle-free.
- No new module dependencies; `go.mod` unchanged.
- Never invoke a `git` binary. Identity derivation is plain-file inspection plus `go-git`'s `config.ReadConfig` for the origin URL.
- Every `*_test.go` package keeps real host state untouched (the `TestMain` fail-fast/mock seams documented in `.operator-shared/specs/test-isolation.md`). Identity tests use temp directories, never the developer's cwd.
- Slug shape is unchanged: `<sanitized folder>-<14-char base36 hash>`; `naming.ParseVMName`/`ParseHomeVolumeName`/`ParseImageTag` are not modified.
- `state.Key` keeps exactly its two fields (`Slug`, `Agent`). The grouping label is passed separately.
- Gate every task on `make check` (fmt, lint, test). In a worktree run `golangci-lint cache clean` first.
- Target platforms: Linux (KVM) and macOS (Apple Silicon). Symlink resolution must be internal to identity derivation so `/tmp` and `/private/tmp` agree.

## Review Focus

Inputs the spec implies but no task's happy path covers. Each has a test in the owning task.

1. **A `--separate-git-dir` checkout or a submodule** must not be treated as an outside worktree. `ResolveWorkspaceLayout` flags both `OutsideWorktree` today only because their git dir is not named `.git`; Task 1 narrows the flag to real worktree registrations (a `/worktrees/<name>` gitdir carrying a `commondir` marker).
2. **A no-origin repo reached through a symlinked path** gets a new repo slug once (because resolution is now internal), so its per-slug config dir moves. Pinned by a test, named in the spec's Known limitations.
3. **`--dry-run` on a project with a pending migration** must not present post-migration artifacts: the remainder of the pipeline keeps the legacy identity. Task 4.
4. **An outside worktree** gets its own identity rather than a refusal, so a cwd under this repo's own `~/.local/share/opencode/worktree/<n>/<name>` stays launchable. Task 1 derives its checkout slug from the worktree root.
5. **An unparsable legacy state file** must fail closed: adoption never clobbers it. Tasks 2, 3.

---

### Task 1: Identity derivation in `internal/git`

**Files:**
- Create: `internal/git/identity.go`
- Create: `internal/git/identity_test.go`
- Modify: `internal/git/git.go` (remove `ProjectSlug`/`projectSlugAt`/`worktreeRoot`), `internal/git/workspace.go` (remove `WorkspaceLayout`/`ResolveWorkspaceLayout`)
- Modify: `internal/git/git_test.go`, `internal/git/workspace_test.go` (migrate the old tests to the new API)

**Interfaces:**
- Produces:
  - `type Identity struct { BindRoot string; CheckoutRoot string; OutsideWorktree bool; RepoSlug string; CheckoutSlug string }`
  - `func ResolveIdentity(cwd string) Identity` — total; never returns an error, and never fails on an outside worktree either: that case is reported as the flag and gets an identity of its own.
  - `func RepoSlug() string` — `ResolveIdentity(".").RepoSlug`
  - `func CheckoutSlug() string` — `ResolveIdentity(".").CheckoutSlug`
  - `HashID`, `projectSlug`, `sanitizeFolderName`, `resolveGitDir`, `originURL`, `lastPathSegment` keep their current behavior (package-internal ones stay unexported).
- Consumes: nothing from other tasks.

- [ ] **Step 1: Write the failing tests** in `internal/git/identity_test.go`

Table-driven `TestResolveIdentity`, each case building a temp layout (helper `newRepo(t, origin string) string` using `os.MkdirAll` + writing `.git/config` with `[remote "origin"] url = <origin>`, matching the existing `git_test.go` fixture style):

```go
func TestResolveIdentityRepoWithOrigin(t *testing.T) {
	root := newRepo(t, "https://example.com/acme/widget.git")
	id := ResolveIdentity(root)
	// The repo slug is byte-identical to the pre-change value: the per-slug user
	// config dir must not move for existing users.
	wantRepo := projectSlug(sanitizeFolderName("widget"), "https://example.com/acme/widget.git")
	require.Equal(t, wantRepo, id.RepoSlug)
	require.Equal(t, projectSlug(sanitizeFolderName("widget"), resolveGitDir(root)), id.CheckoutSlug)
	require.False(t, id.OutsideWorktree)
	require.Equal(t, root, id.BindRoot)
	require.Equal(t, root, id.CheckoutRoot)
}
```

Plus, in the same file:

- `TestResolveIdentityClonesShareRepoSlug` — two temp clones with the same origin URL: equal `RepoSlug`, different `CheckoutSlug`.
- `TestResolveIdentityWorktreeSharesCheckoutSlug` — a nested worktree (`.git` file containing `gitdir: <root>/.git/worktrees/wt`): equal `RepoSlug` and `CheckoutSlug` to the main clone, `BindRoot == root`, `OutsideWorktree` false.
- `TestResolveIdentitySubdirectoryMatchesCheckoutRoot` — a subdirectory of a checkout resolves identically to the checkout root.
- `TestResolveIdentityNonRepoDirectory` — a temp dir with no `.git`: `RepoSlug == CheckoutSlug == projectSlug(sanitizeFolderName(base), abs)`, `OutsideWorktree` false, `BindRoot == abs`.
- `TestResolveIdentityResolvesSymlinks` — a repo reached through a symlinked parent yields the same `RepoSlug`/`CheckoutSlug` as through the physical path.
- `TestResolveIdentitySeparateGitDirIsNotOutside` — a `.git` file whose gitdir is **not** under a `worktrees/` segment: `OutsideWorktree` false, `CheckoutRoot == root`, `BindRoot == root`.
- `TestRepoSlugOutsideWorktree` / `TestCheckoutSlugOutsideWorktreeOwnsItsIdentity` — a worktree outside its checkout: `RepoSlug()` returns the same origin slug as its checkout (they share the per-slug config dir), while `CheckoutSlug()` hashes the **worktree root**, so it equals neither its checkout's slug nor a sibling worktree's. `OutsideWorktree` stays true and `BindRoot` is the worktree root. Cover the subdirectory case too: from `<worktree>/sub/dir` both the slug and `BindRoot` must be unchanged, which is what distinguishes hashing the worktree root from hashing the cwd.
- `TestRepoSlugWithoutOriginUnchanged` — the no-origin fallback still hashes the checkout root, so only symlinked paths change.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/git/ -run 'TestResolveIdentity|TestRepoSlug|TestCheckoutSlug' -v`
Expected: FAIL — `undefined: ResolveIdentity`.

- [ ] **Step 3: Implement `internal/git/identity.go`**

Derivation, in this order:

1. `resolved` = `filepath.EvalSymlinks(cwd)`; if that fails, `filepath.Abs(cwd)`; if that fails, `cwd`.
2. `worktreeRoot := findWorktreeRoot(resolved)` (existing helper in `workspace.go`); if `""`, return the non-repo identity: both slugs `projectSlug(sanitizeFolderName(filepath.Base(resolved)), resolved)`, `BindRoot: resolved`.
3. `commonGitDir := resolveGitDir(worktreeRoot)`; if `""`, same non-repo identity as step 2.
4. `linkedWorktree` = the `.git` entry at `worktreeRoot` is a **file** whose parsed gitdir contains a `/worktrees/` segment. Implement by extending `resolveGitDir` with a companion `isLinkedWorktree(root string) bool` (same file reads), or by having `resolveGitDir` return a small struct — keep `resolveGitDir`'s current signature and add the predicate.
5. Layout:
   - linked worktree with `within(checkoutRoot, worktreeRoot)`: `CheckoutRoot: checkoutRoot`, `BindRoot: checkoutRoot`, `OutsideWorktree: false`; the checkout slug hashes `commonGitDir`, shared with the checkout and its inside-worktrees.
   - linked worktree and not within: `CheckoutRoot: checkoutRoot`, `BindRoot: worktreeRoot`, `OutsideWorktree: true`, and the checkout slug hashes **`worktreeRoot`** instead of `commonGitDir`. Hashing the worktree root is what stops it colliding with its checkout's VM, and it is why nothing has to refuse; binding the worktree root (not the cwd) generalizes #145's "smallest enclosing checkout that contains the CWD", since under this rule the outside worktree is that checkout.
   - not a linked worktree (normal checkout, `--separate-git-dir`, submodule): `CheckoutRoot: worktreeRoot`, `BindRoot: worktreeRoot`, `OutsideWorktree: false`. `checkoutRoot` for the slug hash is still `commonGitDir`.
6. Slugs: `folder := sanitizeFolderName(lastPathSegment(originURL(worktreeRoot)))` when `originURL` is non-empty, else `sanitizeFolderName(filepath.Base(worktreeRoot))`; `RepoSlug = projectSlug(folder, originURL-or-worktreeRoot)`; `CheckoutSlug = projectSlug(folder, commonGitDir)`.

There is no fail-fast accessor: `CheckoutSlug()` returns the worktree-root-derived slug, so an outside worktree simply runs as its own project.

While implementing: delete `projectSlugAt`, `worktreeRoot` and the now-unused `go-git/v5` root import (keep `go-git/v5/config`). `ProjectSlug()`, `WorkspaceLayout` and `ResolveWorkspaceLayout(cwd)` stay as thin shims while Tasks 4 and 5 migrate their callers, each marked `// Deprecated: replaced by ResolveIdentity; removed with the #144 wiring.` Task 5 deletes all three. Rewrite `git_test.go`'s and `workspace_test.go`'s cases against the new API in this task, so **no test still covers a shim** — otherwise Task 5's deletion breaks the `internal/git` suite.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/git/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/git/
git commit -m "feat(git): resolve repo and checkout identity in one pass (#144)"
```

---

### Task 2: State slug-directory move

**Files:**
- Create: `internal/sandbox/state/move.go`
- Create: `internal/sandbox/state/move_test.go`

**Interfaces:**
- Produces:
  - `func SlugDir(slug string) string` — `<state root>/<slug>`, delegating to the package's existing unexported `slugDir` so the two cannot drift.
  - `func MoveSlugDir(fromSlug, toSlug string) ([]string, error)` — recursively merges `SlugDir(fromSlug)` into `SlugDir(toSlug)`, never overwriting an existing file: a free destination name is renamed, both-sides-directories recurses, and a colliding file or a file/directory type mismatch is left in place and reported. Returns the source-relative paths left behind. It is the only mutation primitive adoption needs, and it must always place `<agent>/state.yaml` — the adoption predicate depends on that file arriving. Unnamed results: this package's linter forbids named ones.
- Consumes: Task 1 is not required; this task is independent.

- [ ] **Step 1: Write the failing tests** in `internal/sandbox/state/move_test.go`

Install the state-path mock first (`configpaths.WithMockConfigPaths(t)`), then build a source tree.

```go
func TestMoveSlugDirMovesEveryEntry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "state.yaml"), "home_volume: v1\n")
	writeFile(t, filepath.Join(SlugDir("old-slug"), "opencode", "clients", "lease"), "x")
	writeFile(t, filepath.Join(SlugDir("old-slug"), "claims", "notify.claim"), "y")

	skipped, err := MoveSlugDir("old-slug", "new-slug")
	require.NoError(t, err)
	require.Empty(t, skipped)
	require.FileExists(t, filepath.Join(SlugDir("new-slug"), "opencode", "state.yaml"))
	require.FileExists(t, filepath.Join(SlugDir("new-slug"), "opencode", "clients", "lease"))
	require.FileExists(t, filepath.Join(SlugDir("new-slug"), "claims", "notify.claim"))
	require.NoDirExists(t, SlugDir("old-slug"))
}
```

Plus, for the recursion: `TestMoveSlugDirMergesIntoAnExistingDestinationDirectory` (pre-create `<new>/opencode/ensure-vm.lock` and `<new>/opencode/clients/keep`, then move: assert `<new>/opencode/state.yaml` **arrived**, the pre-existing `ensure-vm.lock` and `clients/keep` survive untouched, `state.yaml` no longer exists under the old slug, `skipped` lists only `opencode/ensure-vm.lock`, and a sibling `claims/notify.claim` moved); `TestMoveSlugDirSkipsCollidingFile` (the destination already holds a *file* of an entry's name: that entry stays in the legacy location, `skipped` lists it, siblings still move); `TestMoveSlugDirSkipsTypeMismatch` (destination has a file where the source has a directory, and vice versa); `TestMoveSlugDirNoOpForSameSlug`; `TestMoveSlugDirMissingSource` (`skipped` empty, no error, destination not created); `TestMoveSlugDirCreatesDestinationParent`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/state/ -run TestMoveSlugDir -v`
Expected: FAIL — `undefined: SlugDir`.

- [ ] **Step 3: Implement `internal/sandbox/state/move.go`**

`SlugDir` delegates to the package's existing `slugDir` (see `state.go:34`) rather than re-deriving the root, so the two cannot drift. `MoveSlugDir` walks one level of `os.ReadDir` on the source and merges it into the destination, recursing where both sides are directories:

- destination name free → `os.MkdirAll` the destination directory, then `os.Rename`;
- both sides directories → recurse into that entry;
- destination name occupied by anything else (a file, or a file/directory type mismatch) → leave the source entry untouched, record its source-relative path in `skipped`, do not descend.

Never overwrite: every `os.Rename` targets a name that did not exist; an existing destination directory keeps its permissions and contents, and existing destination files are never clobbered. Finish by removing each source directory the merge emptied, best-effort, ignoring `ENOTEMPTY`/`ENOENT`. Return `skipped` so the caller can warn; a skip is not an error. Use `os.Lstat` for the occupancy test so a dangling symlink counts as occupied.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/state/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state/
git commit -m "feat(state): add SlugDir and entry-wise MoveSlugDir (#144)"
```

---

### Task 3: Adoption in `internal/sandbox/vm`

**Files:**
- Create: `internal/sandbox/vm/adopt.go`
- Create: `internal/sandbox/vm/adopt_test.go`

**Interfaces:**
- Produces:
  - `type IdentityAdoption struct { Slug string; Pending bool; Applied bool }` — `Slug` is the effective project slug for the caller's remaining work: the checkout slug, or the legacy repo slug while a pending migration is left unapplied.
  - `func AdoptLegacyIdentity(ctx context.Context, client msb.Client, identity git.Identity, agentName string, apply bool, ui termio.UI) (IdentityAdoption, error)`
- Consumes: `state.SlugDir`, `state.MoveSlugDir` (Task 2); `git.Identity` (Task 1); existing `projectVMName`, `msb.Client.GetSandbox`, `msb.SandboxHandle.Destroy`, `msb.IsNotFound`, `msb.IsReplaced`, `state.ReadState`, `state.ErrStateNotFound`, `state.WithRootState`, `naming.RunnerImageRef`.

- [ ] **Step 1: Write the failing tests** in `internal/sandbox/vm/adopt_test.go`

Use the package's existing fake-client and mock-UI helpers (see `prepare_sandbox_test.go`), `configpaths.WithMockConfigPaths(t)`, and a fixed identity:

```go
identity := git.Identity{RepoSlug: "widget-aaa", CheckoutSlug: "widget-bbb", BindRoot: "/repo", CheckoutRoot: "/repo"}
```

- `TestAdoptLegacyIdentityMovesStateAndDestroysVM` — legacy `state.yaml` written under `SlugDir("widget-aaa")/opencode/` with `home_volume: agents-sandbox-home-widget-aaa-opencode-1`; `apply=true`; assert: the fake client records `Destroy` on `agents-sandbox-vm-widget-aaa-opencode`, the state file now exists under `SlugDir("widget-bbb")`, the legacy slug dir is gone, and the result is `{Slug: "widget-bbb", Pending: true, Applied: true}`.
- `TestAdoptLegacyIdentityNoticeNamesEveryPath` — assert the rendered block contains `SlugDir("widget-aaa")`, `SlugDir("widget-bbb")`, `agents-sandbox-vm-widget-aaa-opencode`, `agents-sandbox-vm-widget-bbb-opencode`, `agents-sandbox-home-widget-aaa-opencode-1`, and both image tags (`naming.RunnerImageRef("widget-aaa", "opencode")` → `naming.RunnerImageRef("widget-bbb", "opencode")`).
- `TestAdoptLegacyIdentityNoLegacyState` — no legacy state file: no client calls, no output, result `{Slug: "widget-bbb"}`.
- `TestAdoptLegacyIdentityAlreadyAtCheckoutSlug` — checkout-slug state file present: no-op, `Pending` false.
- `TestAdoptLegacyIdentityUnparsableLegacyState` — legacy `state.yaml` containing `": ["`: returns an error, no `Destroy`, no move.
- `TestAdoptLegacyIdentityApplyFalseIsReadOnly` — `apply=false` with a pending migration: notice printed, nothing moved, no client calls, result `{Slug: "widget-aaa", Pending: true, Applied: false}`.
- `TestAdoptLegacyIdentityLegacyVMAlreadyGone` — `GetSandbox` returns a not-found error: the move still happens.
- `TestAdoptLegacyIdentityLegacyVMReplacedFailsClosed` — `Destroy` returns a replaced error (`msb.IsReplaced` true): error returned, state not moved.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/vm/ -run TestAdoptLegacyIdentity -v`
Expected: FAIL — `undefined: AdoptLegacyIdentity`.

- [ ] **Step 3: Implement `internal/sandbox/vm/adopt.go`**

Structure:

```go
// legacyStatePending reports whether the legacy origin-keyed state exists and the
// checkout-keyed state file does not.
func legacyStatePending(repoKey, checkoutKey state.Key) (bool, error)
```

`legacyStatePending` returns `true` only when `state.ReadState(repoKey)` succeeds and `state.ReadState(checkoutKey)` returns `state.ErrStateNotFound`; an unparsable legacy state returns the error; a read error other than not-found on the checkout key returns the error.

`AdoptLegacyIdentity`:
1. Build `repoKey`/`checkoutKey` from `identity.RepoSlug`/`identity.CheckoutSlug` + `agentName`; call `legacyStatePending`. Not pending → `IdentityAdoption{Slug: identity.CheckoutSlug}`, nil.
2. Read the legacy state for `HomeState.HomeVolume` (the notice names it).
3. `ui.Warnf("%s", adoptionNotice(...))` — the block is built by `adoptionNotice(identity git.Identity, agentName, legacyVolume string, apply bool) string`, one string, headline line then indented rows exactly as in the spec's Settled decisions section. With `apply` false the headline reads "Would migrate this checkout to its own project identity (repository + checkout slugs)."
4. `apply` false → `IdentityAdoption{Slug: identity.RepoSlug, Pending: true}`.
5. `state.WithRootState(true, func(tx *state.RootTxn) error { ... })`:
   - `handle, err := client.GetSandbox(ctx, projectVMName(repoKey))`; on a non-not-found error return it; when found, `handle.Destroy(ctx)` and classify exactly as `replaceSandboxForReconfig` does (`vm_lifecycle.go:157-182`): `msb.IsReplaced` → fail closed with the same wording, `msb.IsNotFound` → continue, other non-nil → return.
   - `skipped, err := state.MoveSlugDir(identity.RepoSlug, identity.CheckoutSlug)`; log `skipped` via `ui.Warnf` per entry. Then **verify the adoption completed**: re-read the checkout-keyed state; if it is still `state.ErrStateNotFound`, the merge left the state file behind (a directory-level collision). Return an error naming the skipped paths and the legacy state file instead of continuing — continuing would run under the checkout identity with no state, which silently provisions a fresh home volume and loses the agent's auth. Do not fall back to the legacy slug here: a loud failure is the only outcome that cannot lose the user's volume.
6. Return `IdentityAdoption{Slug: identity.CheckoutSlug, Pending: true, Applied: true}`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/vm/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/vm/
git commit -m "feat(vm): adopt a legacy origin-keyed identity (#144)"
```

---

### Task 4: Wire identity and adoption through `vm` and `session`

**Files:**
- Modify: `internal/sandbox/vm/run_orchestrate.go` (identity in `PrepareSandbox`, delete `resolveBindLayout`), `internal/sandbox/vm/vm_lifecycle.go` (label + `createProjectVM`/`ensureProjectVM` signature), `internal/sandbox/vm/vm_control.go` (Stop/Kill)
- Modify: `internal/sandbox/session/run.go` (use the session's key instead of re-resolving)
- Modify: `internal/sandbox/vm/prepare_sandbox_test.go` and the other `git.ProjectSlug()` call sites in tests

**Interfaces:**
- Produces: `func (s *Session) Key() state.Key` — the key `PrepareSandbox` prepared the session with. `session.Run`/`Shell` use `ses.Key().Slug` for the notify dedup, the client lease and `runAttach`.
- Consumes: `git.ResolveIdentity` and its `RepoSlug`/`CheckoutSlug` fields (Task 1); `AdoptLegacyIdentity`, `IdentityAdoption` (Task 3).
- Corrections to the current code (all in this task): `createProjectVM` takes an added `repoSlug string` parameter so `naming.LabelProject` gets the **repo** slug while the VM name keeps `k.Slug` (`vm_lifecycle.go:482`); `ensureProjectVM` gains the same parameter and forwards it.

- [ ] **Step 1: Write the failing tests**

Add to `internal/sandbox/vm/`:
- `TestPrepareSandboxUsesCheckoutSlugForStateAndRepoSlugForLabel` — two temp clones of one origin (built like Task 1's fixture): the created sandbox name and image tag carry the checkout-slug hash, the label `naming.LabelProject` carries the repo slug, and the two clones' sandbox names differ.
- `TestPrepareSandboxOutsideWorktreeUsesItsOwnKey` — a cwd that is a worktree outside its checkout, the shape this repo's own `~/.local/share/opencode/worktree/<n>/<name>` worktrees have: `PrepareSandbox` succeeds, and the state key, VM name and image tag carry the worktree-root-derived slug rather than the checkout's.
- `TestPrepareSandboxDryRunKeepsLegacySlugWhileMigrationPending` — legacy state present, `opts.DryRun == true`: the image tag the fake build records carries the **repo** slug, and the notice is printed.
- `TestPrepareSandboxMigratesOnFirstRun` — legacy state + legacy VM present, no dry-run: the legacy VM is destroyed, the state file is under the checkout slug, and the created VM is named with the checkout slug.

Add to `internal/sandbox/session/`: `TestRunUsesSessionKeyForLease` — the lease/claim path is keyed by `ses.Key().Slug` (assert via the state dir the claim lands in).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/vm/ ./internal/sandbox/session/ -run 'TestPrepareSandbox|TestRunUsesSessionKey' -v`
Expected: FAIL (labels and slug assertions).

- [ ] **Step 3: Implement**

`PrepareSandbox` becomes:

```go
cwd, err := resolveWorkspaceDir()          // unchanged: EvalSymlinks for the virtiofs bind
identity := git.ResolveIdentity(cwd)
a, ok := agent.Lookup(opts.Agent)
...
apply := !opts.DryRun && !opts.DryRunVM
adoption, err := AdoptLegacyIdentity(ctx, msb.Get(), identity, a.Name(), apply, ui)
if err != nil {
	return nil, err
}
k := state.Key{Slug: adoption.Slug, Agent: a.Name()}
guestCWD := guestWorkspacePath(identity.BindRoot, cwd)
```

Adoption runs **before** `determineAgentVersion`/`EnsureImage`, so the notice explains the pause and the image builds under the effective tag. `resolveBindLayout` is deleted; `guestWorkspacePath` stays. Store `k` on the returned `Session` and expose `Key()`.

`vm_control.go`: `StopProjectVM`/`KillProjectVM` resolve `identity := git.ResolveIdentity(".")`, call `AdoptLegacyIdentity(ctx, msb.Get(), identity, a.Name(), !dryRun, ui)` and build `k` from `adoption.Slug`.

`session/run.go`: delete both `git.ProjectSlug()` calls; use `ses.Key().Slug` for `startNotifyWatcher`, `runServeOnlySession` and `runAttach`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/vm/ ./internal/sandbox/session/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/vm/ internal/sandbox/session/
git commit -m "feat(vm): key state, VM and image by checkout; label by repo (#144)"
```

---

### Task 5: Wire identity and adoption through `cmd`

**Files:**
- Modify: `cmd/agents-sandbox/commands.go:110` (config resolver → `git.RepoSlug()`), `cmd/agents-sandbox/commands_system.go` (`buildVolumeOpsCmd`, `buildBuildCmd`)
- Modify: `internal/git/git.go`, `internal/git/workspace.go` (delete the Task 1 delegates)
- Modify: `cmd/agents-sandbox/cli_volume_test.go`, `cli_volume_rebuild_test.go`, `cli_system_coverage_test.go` (replace `git.ProjectSlug()`). Task 4 owns the `internal/sandbox/vm` and `internal/sandbox/session` test call sites; this task only touches `cmd/agents-sandbox` tests.

**Interfaces:**
- Consumes: `git.RepoSlug`, `git.ResolveIdentity`, `AdoptLegacyIdentity`.
- Produces: no new exported API; after this task `git.ProjectSlug` and `git.ResolveWorkspaceLayout` no longer exist.

- [ ] **Step 1: Write the failing tests**

- `cmd/agents-sandbox/cli_volume_test.go`: `TestVolumeOpsUseCheckoutSlug` — with two clones of one origin, `volume list`/`volume reset --dry-run` in the second clone resolve to that clone's checkout slug (assert the message names the second clone's VM/volume key).
- `cmd/agents-sandbox/cli_system_coverage_test.go`: `TestBuildUsesCheckoutSlug` and `TestBuildDryRunPrintsMigrationNoticeAndMovesNothing`. The dry-run test names what it can observe: a `build --dry-run` returns before `image.Build`, so nothing at that layer sees the effective slug — the strong dry-run slug pin is the volume-op test through the docker inspect mock.
- `cmd/agents-sandbox/cli_*_test.go`: `TestConfigCommandsWorkOutsideWorktree` — for `config agent`, `config home` and (Task 7) `config paths`, an outside-worktree cwd must succeed and report the repo slug, which stays shared with the checkout.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/agents-sandbox/ -run 'TestVolumeOpsUseCheckoutSlug|TestBuildUsesCheckoutSlug|TestBuildDryRunKeepsLegacySlug|TestConfigCommandsWorkOutsideWorktree' -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

- `commands.go:110` → `launcherconfig.NewResolver(cmd, git.RepoSlug())`.
- `buildVolumeOpsCmd`: `identity := git.ResolveIdentity(".")`; `adoption, err := vm.AdoptLegacyIdentity(c.Context(), msb.Get(), identity, a.Name(), !dryRun, ui)`; use `adoption.Slug` for both `image.EnsureImage` and the `state.Key`. (`vm` is already imported.)
- `buildBuildCmd`: the same identity/adoption pair, using `adoption.Slug` for `image.Build`.
- Delete the `ProjectSlug`, `ResolveWorkspaceLayout` and `WorkspaceLayout` shims (Task 1 already removed the internals), and migrate every remaining `cmd` test call site.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/... ./internal/... 2>&1 | tail -20`
Expected: PASS, and `rg 'ProjectSlug\(|ResolveWorkspaceLayout\('` returns nothing. (Test-function names covering the still-live internal `projectSlug` may remain — the criterion is about call sites, not identifiers.)

- [ ] **Step 5: Commit**

```bash
git add cmd/ internal/
git commit -m "feat(cli): resolve checkout identity in volume ops and build (#144)"
```

---

### Task 6: Prune home-volume reference guard

**Files:**
- Modify: `internal/sandbox/pruning/volumes.go`
- Test: `internal/sandbox/pruning/volumes_test.go`

**Interfaces:**
- Produces: `PruneVolumes` keeps its signature; referenced volumes are skipped and warned, and never counted in `report.VolumesPruned` or fed to `removeStateForGoneKeys`.
- Consumes: existing `state.HomeVolumeReferences`.

- [ ] **Step 1: Write the failing tests**

- `TestPruneVolumesSkipsReferencedVolume` — a stale volume whose name `state.HomeVolumeReferences()` returns: it is not removed, `report.VolumesPruned == 0`, `report.Details` is empty, a warning naming the volume and the referencing key is printed via `ui.Warnf`, and `state.RemoveHomeState` is **not** called for its key (assert the state file survives).
- `TestPruneVolumesStillRemovesUnreferencedVolume` — a second, unreferenced stale volume in the same run is removed and reported.
- `TestPruneVolumesFailsClosedOnUnreadableState` — an unreadable state file makes `PruneVolumes` return an error and remove nothing.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/pruning/ -run TestPruneVolumes -v`
Expected: FAIL — the referenced volume is removed.

- [ ] **Step 3: Implement**

In `PruneVolumes`, call `state.HomeVolumeReferences()` once before `pruneHomeVolumes` and return its error unchanged (fail closed). Pass the map into `pruneHomeVolumes`; before the `msb.Get().RemoveVolume` call, when `len(references[name]) > 0`, `ui.Warnf("keeping home volume %s: still referenced by %s", name, state.FormatKeys(references[name]))` and `continue` — before the `dryRun` branch, so a dry run reports the same set it would keep. `removeStateForGoneKeys` needs no change (it only sees keys already in `removed`), but add a test assertion in Step 1 to pin that.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/pruning/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/pruning/
git commit -m "fix(prune): keep home volumes still referenced by a state file (#144)"
```

---

### Task 7: `config paths`

**Files:**
- Modify: `internal/configpaths/configpaths.go` (interface + `realConfigPaths`), `internal/configpaths/testmock.go` (`mockConfigPaths`, `failFastConfigPaths`), `internal/viperconfig/viperconfig.go`
- Create: `internal/viperconfig/config_layers_test.go`
- Modify: `cmd/agents-sandbox/commands_system.go` (add the subcommand), `cmd/agents-sandbox/constants.go` (`cmdPaths`)
- Create: `cmd/agents-sandbox/cli_config_paths_test.go`

**Interfaces:**
- Produces:
  - `configpaths.ConfigPaths` gains `UserSlugConfigDir(slug string) string`; all three implementations implement it; `viperconfig.NewResolver` uses it in place of its inline `filepath.Join(configpaths.Get().UserConfigDir(), slug)` (`viperconfig.go:169`).
  - `type ConfigLayer struct { Name string; Path string; Present bool }` and `func ConfigLayers(slug string) []ConfigLayer` in `viperconfig`; `NewResolver` drives its file merging from `ConfigLayers(slug)` so the display order and the merge order cannot drift.
- Consumes: `git.RepoSlug` (Task 5); `resolverFromContext`.

- [ ] **Step 1: Write the failing tests**

- `internal/viperconfig/config_layers_test.go`: `TestConfigLayersOrderAndPaths` — with `configpaths.WithMockConfigPaths(t)` and a temp project config, `ConfigLayers("widget-bbb")` returns, lowest first: generic user (`Present` true when a `config.yaml` exists there), per-slug user, project, then the two env layers and flags with `Path == ""`; `UserSlugConfigDir("widget-bbb")` is the per-slug layer's path. `TestNewResolverMergesEveryLayer` — a value set only in the per-slug dir is resolved.
- `cmd/agents-sandbox/cli_config_paths_test.go`:
  - `TestConfigPathsPrintsLayersToStdout` — stdout contains one line per layer in precedence order with the resolved paths interpolated, `(missing)` appended when `Present` is false, and a trailing `repo slug: <slug>` line.
  - `TestConfigPathsPrintsHintToStderr` — stderr contains the "where to put what" sentence naming `.agents-sandbox/config.yaml` and the generic user config dir; stdout does not.
  - `TestConfigPathsIsQuietAware` — under `--quiet` the stdout layer lines are suppressed (they go through `ui.Outf`) while the stderr hint still prints.
  - Extend `cli_tree_test.go`'s expected tree with the new subcommand.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/viperconfig/ ./cmd/agents-sandbox/ -run 'TestConfigLayers|TestNewResolverMergesEveryLayer|TestConfigPaths' -v`
Expected: FAIL — `undefined: ConfigLayers`.

- [ ] **Step 3: Implement**

`ConfigLayers(slug string) []ConfigLayer` returns six entries in `viperconfig.go:160-161`'s documented order, using `findConfigFile` for `Present`; `NewResolver` iterates it for the three file layers. The command prints, in this order and shape:

```
config layers (lowest to highest precedence):
  generic user config  <dir>
  per-project user config  <dir>  (missing)
  project config  <dir>
  legacy env  OPENCODE_SANDBOX_*
  env  AGENTS_SANDBOX_*
  flags
repo slug:  <repo slug>
```

Layers via `ui.Outf`; then `ui.Infof("Repo-wide settings belong in the committed project config (%s) so every clone shares them; machine-specific settings belong in the generic user config (%s).", ".agents-sandbox/config.yaml", configpaths.Get().UserConfigDir())` for the stderr hint. No flags, no `--format json`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/configpaths/ ./internal/viperconfig/ ./cmd/agents-sandbox/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configpaths/ internal/viperconfig/ cmd/agents-sandbox/
git commit -m "feat(cli): add 'config paths' with the config layer order (#144)"
```

---

### Task 8: Docs, changelog, spec status

**Files:**
- Modify: `docs/commands.md` (new `config paths`), `docs/configuration/launcher.md` (per-slug user config is keyed by the **repo** slug and shared by every clone), `docs/sandboxes.md` (one VM per checkout; the two slugs; fix the stale `vm-ensure/<slug>.lock` claim at line 172 — the real lock is `locks/<slug>/<agent>.lock`), `docs/how-it-works.md` (the migration notice and destroy-then-move order), `CHANGELOG.md` (`[Unreleased]`)
- Modify: `.operator-shared/specs/checkout-identity.md` (Status → implemented)
- Modify: `.operator-shared/index/index.md` (the command tree and the identity-related entries)

**Interfaces:** none.

- [ ] **Step 1: Write the docs changes** listed above, using `{% link %}` for docs-to-docs links and `https://inoio.github.io/agents-sandbox/<page>.html` from root-level files.

- [ ] **Step 2: Verify the docs checker passes**

Run: `ci/check-docs.sh && ci/test-check-docs.sh`
Expected: no errors.

- [ ] **Step 3: Run the whole gate**

Run: `make check`
Expected: fmt, lint and tests all pass. In a worktree, run `golangci-lint cache clean` first.

- [ ] **Step 4: Commit**

```bash
git add docs/ CHANGELOG.md .operator-shared/
git commit -m "docs: document per-checkout identity and 'config paths' (#144)"
```

---

### Review-fix round (PR #244 review): durable recovered home volume

Greptile P1: the VM-only adoption arm destroyed the legacy VM but kept its home-volume name only in
`IdentityAdoption.RecoveredHomeVolume`, which the `stop`/`kill`/`build`/volume commands discard and which a
`run` can lose by failing after adoption but before home resolution — leaving the user's saved home volume
orphaned. Fix (the reviewer's "save the reference durably before destroying", test-first):

- `state.HomeState` gains `RecoveredHomeVolume` (`recovered_home_volume,omitempty`): the volume a VM-only
  adoption preserved, pending recovery into `HomeVolume`. `state.HomeVolumeReferences` reports it too, so
  prune protects the volume in the window before recovery.
- `vm.adoptLegacyIdentity` records it (through `state.RecoverHomeState`) *before* destroying the legacy VM;
  `IdentityAdoption` loses its in-memory `RecoveredHomeVolume` field and `prepareHomeVolume` its
  `legacyRecoveredVolume` parameter (the in-memory path was the bug).
- `volume.Manager.ResolveHomeVolume` recovers a state's pending `RecoveredHomeVolume` ahead of the live-VM
  branch; `priorOrNil` treats the marker as no prior, so recovery seeds the desired fingerprints instead of
  copying the marker's empty ones (the #183 rule).
- `legacyAdoptionPending` keeps the trigger live while the checkout state carries the marker, so a legacy VM
  left behind by a failed destroy is retried.

Tests: `TestAdoptLegacyIdentityAdoptsLiveLegacyVMWithoutState` (marker persisted),
`TestAdoptLegacyIdentityPersistsVolumeBeforeDestroyFails` (persist-before-destroy and retry),
`TestAdoptLegacyIdentityDryRunLiveLegacyVMIsReadOnly` (nothing recorded),
`TestResolveHomeVolume_RecoversPersistedAdoptionVolume`,
`TestPrepareHomeVolumeRecoversPersistedAdoptionVolume`, and
`TestHomeVolumeReferencesProtectsPendingRecoveredVolume`.

---

### Review-fix round 2 (PR #244 review): marker follow-through

The durable marker's follow-through paths still disagreed, so Greptile flagged three more P1s, plus a
cross-clone duplication of the marker itself. Fixes, test-first:

- **Volume commands miss the pending home** (`internal/sandbox/volume/operations.go`): `volumeOp` now falls
  back to `st.RecoveredHomeVolume` when `st.HomeVolume` is empty, and clears `RecoveredHomeVolume` in the
  commit, so a first `volume migrate`/`reset`/`edit` after adoption uses the preserved volume and cannot be
  undone by the next resolution. Tests: `TestVolumeOp_UsesRecoveredHomeVolumeAsSource`,
  `TestVolumeOp_ClearsRecoveredHomeVolumeWithExplicitSource`.
- **Corrupt checkout state blocks recovery** (`internal/sandbox/vm/adopt.go`): `legacyAdoptionPending` now
  skips migration (returns not-pending) on a non-not-found checkout-state read error instead of aborting the
  run, so `PrepareSandbox` reaches `ResolveHomeVolume`/`RecoverHomeState` and repairs it; legacy VMs and state
  stay untouched. Tests: `TestAdoptLegacyIdentityUnparsableCheckoutStateSkipsMigration`,
  `TestAdoptLegacyIdentityNonRepoCorruptStateDoesNotBlock`,
  `TestAdoptLegacyIdentityRecheckCorruptStateSkips`; the legacy-read fail-closed tests are unchanged.
- **Bare root's first run ignores saved settings** (`cmd/agents-sandbox`): new `effectiveConfigSlug` resolves
  the per-slug config dir to a bare root's legacy slug when it has a config and the repo-slug dir does not, and
  is used by both `PersistentPreRunE` and `config paths` (which now prints the effective layer but keeps the
  repo-slug label). Tests: `TestBareRootReadsLegacyConfigBeforeMigration`,
  `TestBareRootConfigPathsShowsEffectiveLayer`.
- **Two clones recover the same home volume** (`internal/sandbox/state/root.go`,
  `internal/sandbox/vm/adopt.go`): the marker is per-checkout, so when clone A's destroy fails and clone B
  adopts the surviving VM, both recorded the same volume. Added `RootState.AdoptedHomeVolumes` (keyed
  `<legacySlug>/<agent>`, methods `AdoptedHomeVolume`/`ClaimAdoptedHomeVolume`/`ForgetAdoptedHomeVolume` +
  `ClearAdoptedHomeVolume`); `adoptLegacyIdentity` now takes the `RootTxn`, claims the volume under the
  legacy identity and commits before the destroy, and skips the marker when another checkout owns it. The
  claim is retired (best-effort) by `clearAdoptionClaim` once the volume is folded into the project's own
  state or replaced by a volume op. Tests: `TestAdoptLegacyIdentityClaimsSharedVolumeOnce`,
  `TestResolveHomeVolume_ClearsAdoptionClaim`, `TestRootStateAdoptedHomeVolumeRoundTrip`,
  `TestRootStateForgetAdoptedHomeVolumeMatchesCheckoutAndVolume`, `TestClearAdoptedHomeVolumeAbsentIsNoOp`.

### Post-merge review round: claim ordering + fail-closed

Greptile's guidance on the shared-volume claim asked for two things the first claim patch did not do, plus
coverage of the review-added error branches:

- **Commit the claim before the marker** (`internal/sandbox/vm/adopt.go`): the patch wrote the
  `recovered_home_volume` marker and then committed the claim. A marker write that succeeded while the commit
  failed would leave the volume recorded without an owner, so a second clone could claim and recover it —
  the very race the claim exists to close. Extracted `persistAdoptionOwnership` (claim → `txn.Commit` →
  marker) so the claim is durable on its own, before the marker and the destroy.
- **Fail closed on a volume mismatch**: a claim this checkout owns that names a different volume than the live
  legacy VM mounts is unreconcilable, so `claimLegacyHomeVolume` returns an error instead of re-claiming and
  overwriting the record.
- **Coverage** of the review-added branches, first-failing then green:
  `internal/sandbox/vm/adopt_review_test.go` (`persistRecoveredHomeVolume` empty/read-error,
  marker-write failure aborts before the destroy, claim-commit failure aborts, volume mismatch refuses, locked
  re-check error propagates), `TestClearAdoptedHomeVolumeEmptyVolumeIsNoOp`/`CommitError`,
  `TestResolveHomeVolume_AdoptionClaimClearErrorWarns`, and `TestSlugHasUserConfigEmptySlugIsFalse`.
