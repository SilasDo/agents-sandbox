# Bare Checkout Project Config Root Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** In a bare checkout, read all project config (`.agents-sandbox`) from the bare root next to `.git`, not from the launched worktree, leaving every non-bare and non-repo project byte-identical.

**Architecture:** Add `core.bare` to the existing `git.WorkspaceLayout` resolution, then route every `configpaths` project accessor through one pure base-dir resolver that returns the absolute `<checkoutRoot>/.agents-sandbox` in a bare checkout and today's relative `.agents-sandbox` everywhere else. Expose the ignored worktree-level `.agents-sandbox` as a fact and warn about it from the one image path every VM command passes through.

**Tech Stack:** Go 1.26, go-git (`config.ReadConfig` for `core.bare`), Cobra, `termio`. Tests use `testutil` (`InitRepo`/`RunGit`) and the package mocks (`msb`, `docker`, `configpaths`).

**Spec:** `.operator-shared/specs/project-config-root.md`

**Status:** implemented on `bare-project-config-root` (PR #247). A review fix moved the ignored-worktree detection from
the CWD to the **worktree root** (`git.WorkspaceLayout.WorktreeRoot`), so a launch from a worktree subdirectory still
warns; additions: `TestIgnoredWorktreeConfigDirFromWorktreeSubdirectory`,
`TestWarnIgnoredProjectConfigWarnsFromWorktreeSubdirectory`, and the `WorktreeRoot` assertions in
`internal/git/workspace_test.go`.

## Global Constraints

- Follow `AGENTS.md`: idiomatic Go, self-documenting non-abbreviated identifiers.
- Format and lint with `make fmt` and `make lint` (`golangci-lint fmt` / `golangci-lint run`); never `go fmt` or `go vet`.
- No host `git` binary in production code: derive `core.bare` with go-git's `config.ReadConfig` (`cfg.Core.IsBare`), the route `internal/git/git.go`'s `originURL` uses. Tests may use `testutil.RunGit`.
- The project config dir name is the existing constant `.agents-sandbox` (`configpaths.projectConfigDir`); do not rename it.
- Non-bare checkout and non-repo resolution stay byte-identical: the relative constant, resolved against the **logical** `os.Getwd()`, with no `EvalSymlinks`. Only the bare branch resolves symlinks.
- `internal/git` imports no internal package; `configpaths` may import it (no cycle). Do not add other new internal edges.
- Adding a `ConfigPaths` method requires updating `configpaths/testmock.go` (`mockConfigPaths` and `failFastConfigPaths`) and `configpaths/failfast_test.go`.
- Update `CHANGELOG.md` (Unreleased) and every doc listed in the spec's "Docs and the beta recipe" section; `ci/check-docs.sh` runs in `make check`.
- Run `make check` before the final commit.

## Review Focus

Behaviors the spec implies that no happy-path task test exercises, most likely to bite first. Each gets its test in the owning task.

1. A bare checkout with no `.agents-sandbox` at either the bare root or the worktree: reads defaults, no error, no panic. (Task 2)
2. A repo whose shared `.git/config` is missing or unparseable: treated as non-bare (today's behavior), never an error. (Task 1)
3. A CWD reached through a symlink (`/tmp` → `/private/tmp`): the bare base equals the physical bind root; non-bare strings are unchanged. (Task 2)
4. Launching from the bare root itself, or from a subdirectory of a worktree: the base is still the bare root, and a worktree `.agents-sandbox` is ignored with a warning. (Task 2, Task 3)
5. An outside worktree of a bare repo: unchanged (its own CWD config), no warning, no cross-tree read. (Task 2)

---

### Task 1: `core.bare` on the workspace layout

**Files:**
- Modify: `internal/git/workspace.go:11-42`
- Modify: `internal/git/git.go:120-152` (add `isBare` beside `resolveGitDir`)
- Test: `internal/git/workspace_test.go`

**Interfaces:**
- Produces: `WorkspaceLayout` gains `Bare bool`; unexported `isBare(gitDir string) bool`.

- [ ] **Step 1: Assert `Bare` in the existing layout tests**

In `internal/git/workspace_test.go`, add a `Bare` assertion to each existing layout test with the expected value: `false` for `TestResolveWorkspaceLayoutNonRepo`, the repo-root, subdirectory, nested-worktree, outside-worktree, and malformed-`.git` tests; `true` for `TestResolveWorkspaceLayoutBareWorktree` (all three subtests) and for `TestResolveWorkspaceLayoutBareGitDirNotNamedDotGit` (that clone is also bare). Shape:

```go
if got.Bare != want {
    t.Errorf("Bare = %v, want %v", got.Bare, want)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/git/ -run ResolveWorkspaceLayout`
Expected: FAIL to build — `got.Bare` undefined.

- [ ] **Step 3: Add `Bare` and `isBare`**

In `internal/git/workspace.go`, add `Bare bool // the shared git dir is a bare repository` to `WorkspaceLayout`, and set it on the two returned layouts that have a `commonGitDir` (`Bare: isBare(commonGitDir)`); leave the two early returns (`worktreeRoot == ""`, `commonGitDir == ""`) at `Bare: false`.

In `internal/git/git.go`, add next to `resolveGitDir`:

```go
// isBare reports whether gitDir (a repository's shared git directory) is a bare
// repository, read from its config's core.bare. A missing or unreadable config
// means "not bare": that is today's behavior for an unrecognized repo.
func isBare(gitDir string) bool {
    f, err := os.Open(filepath.Join(gitDir, "config"))
    if err != nil {
        return false
    }
    defer f.Close()
    cfg, err := config.ReadConfig(f)
    if err != nil {
        return false
    }
    return cfg.Core.IsBare
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/git/ -run ResolveWorkspaceLayout`
Expected: PASS.

- [ ] **Step 5: Add the missing/unparseable-config case (Review Focus 2)**

Add `TestResolveWorkspaceLayoutBareUnknownWhenConfigMissing`: a temp dir containing a `.git/` directory (via `os.MkdirAll`) with no `config` file, and a subdirectory `wt/` with `.git` a link file reading `gitdir: ../.git/worktrees/wt\n`. Assert `Bare == false` and no panic. Add a second subtest whose `.git/config` holds garbage (`"not a config"`), asserting `Bare == false`.

- [ ] **Step 6: Run it to verify it passes, then commit**

Run: `go test ./internal/git/`
Expected: PASS.

```bash
git add internal/git/workspace.go internal/git/git.go internal/git/workspace_test.go
git commit -m "feat(git): report core.bare on the workspace layout"
```

---

### Task 2: Project config base-dir resolver

**Files:**
- Modify: `internal/configpaths/configpaths.go:12-33` (interface), `:128-199` (project accessors)
- Modify: `internal/configpaths/testmock.go:12-15`, `:104-148`, `:176-203`
- Modify: `internal/configpaths/failfast_test.go:30-38`
- Test: `internal/configpaths/project_config_root_test.go` (create), `internal/configpaths/mockpaths_test.go` (only if a mock assertion needs updating)

**Interfaces:**
- Consumes: `git.ResolveWorkspaceLayout(cwd).Bare` / `.CheckoutRoot` / `.OutsideWorktree` (Task 1).
- Produces: `ConfigPaths.IgnoredWorktreeConfigDir() string`; unexported `(c *realConfigPaths) projectConfigBase() string`; unexported `resolveProjectConfig(cwd string) (baseDir, ignoredDir string)`.

- [ ] **Step 1: Write the failing resolver tests**

Create `internal/configpaths/project_config_root_test.go`. Use a shared helper that builds a real bare checkout with the git binary (mirrors `internal/git/workspace_test.go`):

```go
func writeBareCheckout(t *testing.T) string {
    t.Helper()
    origin := testutil.InitRepo(t)
    root := t.TempDir()
    testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
    testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))
    return root
}
```

Each test pins `HOME` and the four `XDG_*` vars to `t.TempDir()` (isolation), calls `WithRealConfigPaths(t)`, and `t.Chdir`s. No test may be `t.Parallel` (it uses `t.Setenv`/`t.Chdir`).

- `TestProjectConfigBaseBareWorktree`: chdir `<root>/main`; assert `ProjectConfigDir()` and `ProjectDockerfile()` and `ProjectEnvFile()` and `ProjectEnvSecretFile()` equal `<root>/.agents-sandbox` joined with the right file, and `ProjectConfigDirAbs()` equals `<root>/.agents-sandbox`.
- `TestProjectConfigBaseBareWorktreeSubdirectory`: chdir `<root>/main/internal` (create it); assert the same absolute bare base.
- `TestProjectConfigBaseBareRoot`: chdir `<root>`; assert the bare base.
- `TestProjectConfigBaseBareWithoutConfigDir`: chdir `<root>/main` without creating any `.agents-sandbox`; assert the base is still `<root>/.agents-sandbox` and no error (Review Focus 1).
- `TestProjectConfigBaseBareViaSymlink`: create `link := filepath.Join(t.TempDir(), "link")` as a symlink to `root`, chdir `<link>/main`; assert the base uses the physical `root` (Review Focus 3).
- `TestProjectConfigBaseNonBareSubdirectoryUnchanged`: an ordinary `testutil.InitRepo(t)` with a subdir; chdir the subdir; assert `ProjectConfigDir() == ".agents-sandbox"` (relative) and `ProjectDockerfile() == ".agents-sandbox/Dockerfile"`.
- `TestProjectConfigBaseNonRepoUnchanged`: chdir a plain `t.TempDir()`; assert the relative strings.
- `TestIgnoredWorktreeConfigDir`: bare checkout; create `<root>/main/.agents-sandbox`; chdir `<root>/main`; assert `IgnoredWorktreeConfigDir()` equals `<root>/main/.agents-sandbox`, while the base is still the bare root.
- `TestIgnoredWorktreeConfigDirEmptyForNonBare`: ordinary repo dir with a `.agents-sandbox`; assert `""`.
- `TestIgnoredWorktreeConfigDirEmptyForOutsideWorktree`: a bare repo whose worktree is created under a separate temp dir (`testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", wt)` with `wt` outside `root`); chdir `wt` with a `.agents-sandbox`; assert base is relative and `IgnoredWorktreeConfigDir() == ""` (Review Focus 5).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/configpaths/ -run 'ProjectConfigBase|IgnoredWorktree'`
Expected: FAIL to build — `IgnoredWorktreeConfigDir` undefined.

- [ ] **Step 3: Add the interface method and resolver**

In `configpaths.go`: add `IgnoredWorktreeConfigDir() string` to the `ConfigPaths` interface, and implement the resolver:

```go
// resolveProjectConfig returns the directory project config is read from and,
// when a bare checkout ignores a worktree-level .agents-sandbox, its path.
// In a bare checkout the base is the absolute bare-root .agents-sandbox; the
// worktree is inside the bare root, so a copy there is ignored. Everywhere else
// baseDir is the relative constant, resolved against the CWD by the callers.
func resolveProjectConfig(cwd string) (baseDir, ignoredDir string) {
    physical, err := filepath.EvalSymlinks(cwd)
    if err != nil {
        physical = cwd
    }
    layout := git.ResolveWorkspaceLayout(physical)
    if !layout.Bare || layout.OutsideWorktree || layout.CheckoutRoot == "" {
        return projectConfigDir, ""
    }
    base := filepath.Join(layout.CheckoutRoot, projectConfigDir)
    ignored := filepath.Join(physical, projectConfigDir)
    if info, statErr := os.Stat(ignored); statErr == nil && info.IsDir() {
        return base, ignored
    }
    return base, ""
}

// projectConfigBase returns the config dir for this process's CWD. It never
// creates a directory. When os.Getwd fails it degrades to the relative constant,
// as ProjectConfigDirAbs does.
func (c *realConfigPaths) projectConfigBase() string {
    cwd, err := os.Getwd()
    if err != nil {
        return projectConfigDir
    }
    base, _ := resolveProjectConfig(cwd)
    return base
}
```

Then route the accessors:

- `ProjectConfigDir()` → `return c.projectConfigBase()`.
- `ProjectConfigDirAbs()` → `base := c.projectConfigBase(); if filepath.IsAbs(base) { return base }; cwd, err := os.Getwd(); if err != nil { return base }; return filepath.Join(cwd, base)`.
- `ProjectDockerfile()` → `filepath.Join(c.projectConfigBase(), dockerfileName)`.
- `ProjectEnvFile()` → `filepath.Join(c.projectConfigBase(), envFileName)`.
- `ProjectEnvSecretFile()` → `filepath.Join(c.projectConfigBase(), envSecretFileName)`.
- `ProjectEnvSecretYAMLFile()` stays `filepath.Join(c.ProjectConfigDir(), envSecretYAMLFileName)`, which is now base-aware.

Add the import `"github.com/inoio/agents-sandbox/internal/git"`.

- [ ] **Step 4: Add the mock and fail-fast implementations**

In `testmock.go`: `mockConfigPaths` gains `func (m *mockConfigPaths) IgnoredWorktreeConfigDir() string { return "" }`; `failFastConfigPaths` gains `func (f *failFastConfigPaths) IgnoredWorktreeConfigDir() string { f.mustMock(); return "" }`.

In `failfast_test.go`, add `{"IgnoredWorktreeConfigDir", func() { f.IgnoredWorktreeConfigDir() }}` to the method table.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/configpaths/`
Expected: PASS, including the existing `TestProjectConfigDirAbs`, `TestProjectConfigDirAbsFallsBackWhenCwdUnreadable`, and `mockpaths_test.go`.

- [ ] **Step 6: Sweep for consumers that bypassed `ProjectConfigDir()`**

Run: `rg -n 'projectConfigDir' internal/configpaths internal/sandbox internal/viperconfig`
Expected: no accessor joins `projectConfigDir` directly except inside `projectConfigBase`/`resolveProjectConfig`.

- [ ] **Step 7: Commit**

```bash
git add internal/configpaths
git commit -m "feat(configpaths): read bare-checkout project config from the bare root"
```

---

### Task 3: Warn about the ignored worktree config

**Files:**
- Create: `internal/sandbox/image/warn.go`
- Modify: `internal/sandbox/image/build.go:566-592`
- Modify: `cmd/agents-sandbox/commands_system.go:523-537`
- Test: `internal/sandbox/image/warn_test.go` (create)

**Interfaces:**
- Consumes: `configpaths.ConfigPaths.IgnoredWorktreeConfigDir()` (Task 2).
- Produces: `image.WarnIgnoredProjectConfig(ui termio.UI)`.

- [ ] **Step 1: Write the failing test**

Create `internal/sandbox/image/warn_test.go`. Build a bare checkout by hand (no git binary needed): `<root>/.git/config` containing `[core]\n\tbare = true\n`, `<root>/main/.git` containing `gitdir: ../.git/worktrees/main\n`, and `<root>/main/.agents-sandbox/` as a directory. Pin `HOME`/`XDG_*` to `t.TempDir()`, `configpaths.WithRealConfigPaths(t)`, `t.Chdir(filepath.Join(root, "main"))`. Call `WarnIgnoredProjectConfig(ui)` with a `&termio.Mock{}` and assert some `ui.WarnCalls` entry contains `<root>/main/.agents-sandbox`. Add a second case with no worktree `.agents-sandbox` asserting no warning.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/sandbox/image/ -run WarnIgnoredProjectConfig`
Expected: FAIL to build — `WarnIgnoredProjectConfig` undefined.

- [ ] **Step 3: Implement the helper and wire the call sites**

Create `internal/sandbox/image/warn.go`:

```go
package image

// WarnIgnoredProjectConfig warns when a bare checkout ignores a worktree-level
// .agents-sandbox (project config is read from the bare root). It is a no-op
// elsewhere and on a non-bare checkout.
func WarnIgnoredProjectConfig(ui termio.UI) {
    if dir := configpaths.Get().IgnoredWorktreeConfigDir(); dir != "" {
        ui.Warnf(
            "ignoring %s: in a bare checkout the project config is read from the bare root, not the worktree",
            dir,
        )
    }
}
```

In `build.go`, call `WarnIgnoredProjectConfig(ui)` as the first statement of both `EnsureImage` and `Build`. In `commands_system.go`, call `image.WarnIgnoredProjectConfig(ui)` at the top of `buildDockerfileCmd`'s `RunE` (before `RenderProjectDockerfile`).

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/sandbox/image/ -run WarnIgnoredProjectConfig`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image/warn.go internal/sandbox/image/warn_test.go internal/sandbox/image/build.go cmd/agents-sandbox/commands_system.go
git commit -m "feat(image): warn when a bare worktree's project config is ignored"
```

---

### Task 4: End-to-end bare resolution through the image and CLI

**Files:**
- Test: `internal/sandbox/image/dockerfile_test.go` (modify or create)
- Create: `cmd/agents-sandbox/cli_bare_checkout_test.go`

**Interfaces:**
- Consumes: `readProjectDockerfile()` (unexported, existing), `image.RenderProjectDockerfile`, `image.WarnIgnoredProjectConfig` (Task 3), `git`/`configpaths` from Tasks 1-2.
- Produces: `setupBareCheckoutFixtures(t *testing.T, args ...string) (*cobra.Command, *termio.Mock, string)` in the `main` test package.

- [ ] **Step 1: Write the failing image test**

In `internal/sandbox/image/dockerfile_test.go`, add `TestReadProjectDockerfileBareWorktree`: a synthesized bare checkout (as in Task 3), with `<root>/.agents-sandbox/Dockerfile` = `"FROM bare-root\n"` and `<root>/main/.agents-sandbox/Dockerfile` = `"FROM worktree\n"`; pin `HOME`/`XDG_*`, `configpaths.WithRealConfigPaths(t)`, `t.Chdir(filepath.Join(root, "main"))`; assert `string(readProjectDockerfile()) == "FROM bare-root\n"`.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/sandbox/image/ -run ReadProjectDockerfileBareWorktree`
Expected: FAIL — currently reads the worktree copy (`"FROM worktree\n"`).

- [ ] **Step 3: Run it again after Task 2 is in place**

Run: `go test ./internal/sandbox/image/ -run ReadProjectDockerfileBareWorktree`
Expected: PASS (Task 2 already makes `ProjectDockerfile()` bare-aware; this test pins that the image path follows it).

- [ ] **Step 4: Write the CLI fixture and the failing CLI tests**

Create `cmd/agents-sandbox/cli_bare_checkout_test.go` with:

```go
func setupBareCheckoutFixtures(t *testing.T, args ...string) (*cobra.Command, *termio.Mock, string) {
    t.Helper()
    origin := testutil.InitRepo(t)
    root := t.TempDir()
    testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
    testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))
    t.Chdir(filepath.Join(root, "main"))
    t.Setenv("HOME", t.TempDir())
    t.Setenv("XDG_CONFIG_HOME", t.TempDir())
    t.Setenv("XDG_STATE_HOME", t.TempDir())
    t.Setenv("XDG_DATA_HOME", t.TempDir())
    t.Setenv("XDG_CACHE_HOME", t.TempDir())
    configpaths.WithRealConfigPaths(t)
    image.WithMockAgentVersion(t, "0.0.0-test")
    msb.WithMsbMock(t, &msb.MockMsbClient{})
    docker.WithNoopDockerMock(t)
    doctor.MockedCheckAll(t, true)
    doctor.MockedCheckDocker(t, true)
    oldPrepare := prepareMSBRuntime
    prepareMSBRuntime = func(context.Context, termio.UI, string) error { return nil }
    t.Cleanup(func() { prepareMSBRuntime = oldPrepare })
    ui := &termio.Mock{}
    cmd := buildRootCmd(ui)
    cmd.SetArgs(args)
    return cmd, ui, root
}
```

Then two tests (not `t.Parallel`):

- `TestBuildDockerfileCommandReadsBareRoot`: write `<root>/.agents-sandbox/Dockerfile` = `"FROM bare-root\n"` and `<root>/main/.agents-sandbox/Dockerfile` = `"FROM worktree\n"`, run `setupBareCheckoutFixtures(t, cmdBuild, cmdDockerfile)`, `Execute`, assert stdout contains `bare-root` and not `worktree`, and that a warning names `<root>/main/.agents-sandbox`.
- `TestRunCommandReadsBareRoot`: create only `<root>/main/.agents-sandbox/` (a directory), run `setupBareCheckoutFixtures(t, cmdRun, flagDryRun)`, `Execute`, assert no error and that a warning names `<root>/main/.agents-sandbox` (proving the real `run` path resolved the bare branch and reached `EnsureImage`).

- [ ] **Step 5: Run the CLI tests to verify they pass**

Run: `go test ./cmd/agents-sandbox/ -run 'BareCheckout|ReadsBareRoot|BuildDockerfileCommandReadsBareRoot|RunCommandReadsBareRoot'`
Expected: PASS.

- [ ] **Step 6: Run the affected packages together, then commit**

Run: `go test ./cmd/agents-sandbox/ ./internal/sandbox/image/ ./internal/configpaths/ ./internal/git/`
Expected: PASS.

```bash
git add internal/sandbox/image/dockerfile_test.go cmd/agents-sandbox/cli_bare_checkout_test.go
git commit -m "test: cover bare-checkout project config resolution end to end"
```

---

### Task 5: Docs, beta recipe, changelog, spec status

**Files:**
- Modify: `docs/recipes/bare-worktrees.md`
- Modify: `.agents/skills/bare-worktrees/SKILL.md` (1:1 content copy)
- Modify: `docs/configuration/launcher.md`, `docs/commands.md`, `docs/how-it-works.md`, `docs/sandboxes.md`
- Modify: `CHANGELOG.md`
- Modify: `.operator-shared/specs/project-config-root.md` (status), `.operator-shared/catalog.md` if the description moved

**Interfaces:** none (documentation only).

- [ ] **Step 1: Update the recipe and mark it beta**

In `docs/recipes/bare-worktrees.md`, add a leading blockquote below the `# ` heading:

```markdown
> **Beta** — the bare-checkout layout and its configuration rules may still change.
```

Then state the config rule in "Working with it" and "Compatibility": project config (`.agents-sandbox/Dockerfile`, `config.yaml`, `env`, `env.secret`, home sources, agent config) is read from the bare root next to `.git`; a worktree-level `.agents-sandbox` is ignored, with a warning.

- [ ] **Step 2: Mirror the recipe into the skill**

Copy the updated recipe body into `.agents/skills/bare-worktrees/SKILL.md` so the two stay identical in content (required by `bare-worktree-layout.md`), including the Beta notice.

- [ ] **Step 3: Update the reference docs**

- `docs/configuration/launcher.md`: note that in a bare checkout `.agents-sandbox/` is read from the bare root (next to `.git`), not the worktree.
- `docs/commands.md`: the `build` and `build dockerfile` entries note the bare-root resolution.
- `docs/how-it-works.md` and `docs/sandboxes.md`: one sentence each where `.agents-sandbox/Dockerfile` is named.

- [ ] **Step 4: Add the changelog entry**

Under `[Unreleased]`, add a `### Fixed` (or the section in use) line: bare checkouts now read project config from the bare root next to `.git`; a `.agents-sandbox` in the launched worktree is ignored and warned about. Note it changes nothing for non-bare and non-repo projects.

- [ ] **Step 5: Mark the spec implemented and verify**

Set the spec status line to implemented with the branch/PR, and run `make check` (fmt, lint, test, docs link check) plus `make compile-all`.

```bash
git add docs .agents/skills/bare-worktrees/SKILL.md CHANGELOG.md .operator-shared
git commit -m "docs: bare-checkout project config root and beta recipe"
```

---

## Self-Review

- **Spec coverage:** Decision + behavior table → Tasks 1-2; resolver contract and representation → Task 2; ignored-worktree warning → Tasks 3-4; test seam → Task 4; docs/beta recipe → Task 5; non-goals/anti-goals → nothing built beyond them; deferred layering → not implemented (recorded in the spec).
- **Step scan:** Each step names one action and a checkable result; code appears only where the signature and tests do not determine it (`isBare`, `resolveProjectConfig`).
- **Type consistency:** `WorkspaceLayout.Bare` (Task 1) is consumed as `layout.Bare` (Task 2); `ConfigPaths.IgnoredWorktreeConfigDir()` (Task 2) is consumed by `WarnIgnoredProjectConfig` (Task 3); `WarnIgnoredProjectConfig` (Task 3) is called from the CLI test's path (Task 4). `readProjectDockerfile` is pre-existing.
- **Review Focus:** (1) Task 2 `TestProjectConfigBaseBareWithoutConfigDir`; (2) Task 1 `TestResolveWorkspaceLayoutBareUnknownWhenConfigMissing`; (3) Task 2 `TestProjectConfigBaseBareViaSymlink`; (4) Task 2 `TestIgnoredWorktreeConfigDir` + Task 4 `TestRunCommandReadsBareRoot`; (5) Task 2 `TestIgnoredWorktreeConfigDirEmptyForOutsideWorktree`.
