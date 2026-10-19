# opencode2 Worktree API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `--worktree <name>[:<base>]` work with the `opencode2` agent by calling v2's project-scoped worktree routes instead of v1's 404ing ones.

**Architecture:** The `agent.WorktreeProvider` seam gains the launch directory (the guest CWD), which v2 needs to resolve the project id (`GET /api/location?location[directory]=…`); v1 ignores it. The v2 list drops the project's canonical root entry, and the v2 create sends `Worktree.CreateInput` (`{projectID, name, branch?}`) instead of the v1 body. `vm.ResolveTarget` validates a requested base against the launch directory *before* creating, so v2's only realistic create failure surfaces as the existing friendly error.

**Tech Stack:** Go 1.26, `curl` + `jq` inside the VM, stdlib tests, `golangci-lint`.

**Spec:** `.operator-shared/specs/opencode2-worktree-api.md`

## Global Constraints

- Module `github.com/inoio/agents-sandbox`, Go 1.26.
- `make fmt` / `golangci-lint fmt` to format (never `go fmt`); `make lint` (never `go vet`).
- TDD: write the failing test, run it, implement, run it green, commit.
- No behavior change for the `opencode` (v1) agent beyond the new (ignored) target parameter.
- The v2 create body is exactly `Worktree.CreateInput` (`additionalProperties: false`): only `projectID`, `name`, and `branch` — never `strategy`, `directory`, `from`, or `startCommand`.
- Every v2 request keeps the baked credential (`opencode2CurlAuth`, `-u opencode:opencode`).
- Targets come from `vm.ResolveTarget`'s `defaultDir`; nothing else may be sent as the v2 location.
- Verified against `opencode v2.0.32` (the pinned version); do not change the pin.

## Review Focus

- A worktree name equal to the repository directory name → creates a worktree, never attaches to the main checkout (the list drops `project.canonical`).
- `--worktree <name>:<bad-base>` where the worktree already exists → still reuses it and warns that the base is ignored; the base is not validated on reuse.
- A base that does not resolve → fails **before** any create request, leaving no orphan worktree on the home volume.
- `/api/location` failing, or returning no project id → the shell command exits non-zero; a request is never sent with an empty `projectID`.
- A target containing shell metacharacters (spaces, single quotes) → single-quoted in the location query and in the jq arguments.

---

### Task 1: The worktree seam carries the target directory

**Files:**
- Modify: `internal/agent/capabilities.go` (`WorktreeProvider`)
- Modify: `internal/agent/agent.go` (`WorktreeSpec`)
- Modify: `internal/agent/opencode.go`
- Modify: `internal/agent/opencode2.go` (signature only)
- Modify: `internal/sandbox/vm/worktree.go` (`ResolveTarget`)
- Test: `internal/sandbox/vm/worktree_test.go`, `internal/sandbox/vm/resolve_target_test.go`, `internal/sandbox/vm/coverage_more_test.go`, `internal/agent/opencode_test.go`, `internal/agent/opencode2_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `agent.WorktreeProvider{ WorktreeListCmd(target string) string; WorktreeCreateCmd(spec WorktreeSpec) string; WorktreeParseDir(stdout string) (string, bool) }`; `agent.WorktreeSpec{Name, Base, Target string}` where `Target` is the launch directory (guest CWD) and may be empty.
- Produces: `vm.ResolveTarget` calls `provider.WorktreeListCmd(defaultDir)` and passes `agent.WorktreeSpec{Name: spec.Name, Base: spec.Base, Target: defaultDir}` to the create.
- Produces: v2's still-broken commands compile by ignoring the target; Task 2 replaces them.

- [ ] **Step 1: Write the failing seam test**

In `internal/sandbox/vm/worktree_test.go` add a recording provider and a test that pins the target reaching both calls:

```go
// recordingWorktreeAgent records the target and spec ResolveTarget hands to the
// provider, so the seam contract is pinned without a real agent.
type recordingWorktreeAgent struct {
	fakeAgent
	listTarget string
	createSpec agent.WorktreeSpec
}

func (r *recordingWorktreeAgent) WorktreeListCmd(target string) string {
	r.listTarget = target
	return "list"
}

func (r *recordingWorktreeAgent) WorktreeCreateCmd(spec agent.WorktreeSpec) string {
	r.createSpec = spec
	return "create"
}

func (r *recordingWorktreeAgent) WorktreeParseDir(string) (string, bool) {
	return "/workspace/wt/feat-x", true
}

func TestResolveTargetPassesTargetToProvider(t *testing.T) {
	ui := &termio.Mock{}
	a := &recordingWorktreeAgent{}
	sb := &msb.MockSandbox{ShellOut: map[string]msb.ShellResult{
		"list":   msb.NewTestResult(true, 0, `[]`, "", nil),
		"create": msb.NewTestResult(true, 0, `{"directory":"/workspace/wt/feat-x"}`, "", nil),
	}}
	dir, err := ResolveTarget(
		context.Background(),
		a,
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/workspace/wt/feat-x" {
		t.Errorf("got dir %q, want /workspace/wt/feat-x", dir)
	}
	if a.listTarget != mounts.WorkspaceMountPath {
		t.Errorf("list target = %q, want %q", a.listTarget, mounts.WorkspaceMountPath)
	}
	if a.createSpec.Target != mounts.WorkspaceMountPath || a.createSpec.Name != "feat-x" {
		t.Errorf("create spec = %+v, want Target %q and Name feat-x", a.createSpec, mounts.WorkspaceMountPath)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/vm/ -run TestResolveTargetPassesTargetToProvider`
Expected: FAIL — `*recordingWorktreeAgent` does not implement `agent.WorktreeProvider` (wrong `WorktreeListCmd` signature), and the mock's `list`/`create` results are never used.

- [ ] **Step 3: Add the target to the seam**

`internal/agent/capabilities.go`: `WorktreeListCmd(target string) string` in the `WorktreeProvider` interface, with the comment extended: the target is the launch directory the daemon scopes its request to; agents with global routes ignore it.

`internal/agent/agent.go`:

```go
// WorktreeSpec describes a git worktree to create in the VM. It is a minimal,
// local type so the agent package does not depend on internal/sandbox/options.
// Target is the launch directory (the VM's working directory) the agent daemon
// scopes its worktree request to; agents with global routes ignore it.
//
//exhaustruct:ignore
type WorktreeSpec struct {
	Name   string
	Base   string
	Target string
}
```

`internal/agent/opencode.go`: `func (opencodeProfile) WorktreeListCmd(string) string { return "curl -sf http://127.0.0.1:4096/experimental/worktree" }` — v1 unchanged, target ignored.

`internal/agent/opencode2.go`: the same signature change; keep the current (broken) body for now.

`internal/sandbox/vm/worktree.go` in `ResolveTarget`:

```go
	listOut, err := sb.Shell(ctx, provider.WorktreeListCmd(defaultDir))
```

and

```go
	createSpec := agent.WorktreeSpec{
		Name:   spec.Name,
		Base:   spec.Base,
		Target: defaultDir,
	}
```

- [ ] **Step 4: Update the existing test call sites (compile-only)**

Every `provider.WorktreeListCmd()` becomes `provider.WorktreeListCmd(mounts.WorkspaceMountPath)`
(`worktree_test.go` ×4, `resolve_target_test.go` ×3, `coverage_more_test.go` ×1;
`internal/agent/opencode_test.go` and `internal/agent/opencode2_test.go` take a literal
`"/workspace"`). `WorktreeCreateCmd` call sites in those tests are unchanged: the v1 and v2
create commands do not read `Target` yet, so the strings still match.

- [ ] **Step 5: Verify the target is ignored by v1**

In `internal/agent/opencode_test.go`, extend `TestOpencodeWorktreeListCmd` to assert the exact command for a target with a space:

```go
	if got := worktree.WorktreeListCmd("/workspace/my notes"); got != "curl -sf http://127.0.0.1:4096/experimental/worktree" {
		t.Errorf("WorktreeListCmd = %q, want the global v1 route", got)
	}
```

Also assert `WorktreeCreateCmd` output is identical for two different `Target` values.

- [ ] **Step 6: Run the packages green**

Run: `go test ./internal/agent/... ./internal/sandbox/vm/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agent internal/sandbox/vm
git commit -m "refactor(agent): pass the launch directory through the worktree seam"
```

---

### Task 2: opencode2 worktree commands target v2's project-scoped routes

**Files:**
- Modify: `internal/agent/opencode2.go`
- Test: `internal/agent/opencode2_test.go`

**Interfaces:**
- Consumes: `agent.WorktreeSpec.Target` (Task 1).
- Produces: `opencode2ServerURL` (`http://127.0.0.1:4096`), `opencode2LocatePrelude(target string) string` (exporting `LOC`, `PID`, `CANON` in the shell), `opencode2Profile.WorktreeListCmd(target string) string`, `opencode2Profile.WorktreeCreateCmd(spec WorktreeSpec) string`.
- Removes: the `opencode2ProjectIDCmd` const.

- [ ] **Step 1: Write the failing tests**

Replace the three worktree command tests in `internal/agent/opencode2_test.go` with:

```go
func TestOpencode2WorktreeListCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeListCmd("/workspace")
	for _, part := range []string{
		"-u opencode:opencode",
		"--get --data-urlencode 'location[directory]=/workspace'",
		"/api/location) || exit 1",
		"jq -re '.project.id') || exit 1",
		"jq -r '.project.canonical'",
		"http://127.0.0.1:4096/api/worktree?projectID=$PID",
		`jq -c --arg canonical "$CANON" '[.[]|select(.directory != $canonical)]'`,
	} {
		if !strings.Contains(got, part) {
			t.Errorf("WorktreeListCmd = %q, want to contain %q", got, part)
		}
	}
	if strings.Contains(got, "/api/project/current") || strings.Contains(got, "/api/worktree/$PID") {
		t.Errorf("WorktreeListCmd still uses v1 routes: %q", got)
	}
}

func TestOpencode2WorktreeCreateCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature/x", Target: "/workspace"})
	want := `LOC=$(curl -sf -u opencode:opencode --get --data-urlencode 'location[directory]=/workspace' http://127.0.0.1:4096/api/location) || exit 1; PID=$(printf '%s' "$LOC" | jq -re '.project.id') || exit 1; CANON=$(printf '%s' "$LOC" | jq -r '.project.canonical'); curl -sf -u opencode:opencode -X POST http://127.0.0.1:4096/api/worktree -H 'Content-Type: application/json' -d "$(jq -nc --arg pid "$PID" --arg name 'feature/x' '{projectID:$pid,name:$name}')"`
	if got != want {
		t.Errorf("WorktreeCreateCmd = %q, want %q", got, want)
	}
}

func TestOpencode2WorktreeCreateCmdWithBase(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature-x", Base: "main", Target: "/workspace"})
	want := `LOC=$(curl -sf -u opencode:opencode --get --data-urlencode 'location[directory]=/workspace' http://127.0.0.1:4096/api/location) || exit 1; PID=$(printf '%s' "$LOC" | jq -re '.project.id') || exit 1; CANON=$(printf '%s' "$LOC" | jq -r '.project.canonical'); curl -sf -u opencode:opencode -X POST http://127.0.0.1:4096/api/worktree -H 'Content-Type: application/json' -d "$(jq -nc --arg pid "$PID" --arg name 'feature-x' --arg branch 'main' '{projectID:$pid,name:$name,branch:$branch}')"`
	if got != want {
		t.Errorf("WorktreeCreateCmd(with base) = %q, want %q", got, want)
	}
}

func TestOpencode2WorktreeCreateCmdQuotesTargetAndBase(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x", Base: "it's", Target: "/workspace/my notes"})
	if !strings.Contains(got, `'location[directory]=/workspace/my notes'`) {
		t.Errorf("target not single-quoted: %q", got)
	}
	if !strings.Contains(got, `--arg branch 'it'\''s'`) {
		t.Errorf("base not shell-quoted: %q", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run TestOpencode2Worktree`
Expected: FAIL — the commands still contain `/api/project/current`, `strategy`, and `$PID`.

- [ ] **Step 3: Implement the v2 commands**

In `internal/agent/opencode2.go`, delete `opencode2ProjectIDCmd` and add:

```go
// opencode2ServerURL is the loopback base URL of the opencode2 serve daemon,
// used by the launcher's server-API calls.
const opencode2ServerURL = "http://127.0.0.1:" + opencode2Port

// opencode2LocatePrelude resolves the project scoped to target and exports the
// worktree calls' inputs: LOC (the raw /api/location response), PID (the
// project id) and CANON (the project's canonical directory — the main checkout,
// which the worktree list must exclude). It aborts the command when the location
// call fails or no project id can be read, so a request is never sent with an
// empty projectID.
func opencode2LocatePrelude(target string) string {
	return "LOC=$(curl -sf " + opencode2CurlAuth + " --get --data-urlencode " +
		shellQuote("location[directory]="+target) + " " + opencode2ServerURL + "/api/location) || exit 1; " +
		"PID=$(printf '%s' \"$LOC\" | jq -re '.project.id') || exit 1; " +
		"CANON=$(printf '%s' \"$LOC\" | jq -r '.project.canonical'); "
}

// WorktreeListCmd lists the project's worktrees through v2's project-scoped
// route, dropping the canonical root entry so a worktree name equal to the
// repository directory name never reuses the main checkout.
func (opencode2Profile) WorktreeListCmd(target string) string {
	return opencode2LocatePrelude(target) +
		"curl -sf " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/worktree?projectID=$PID | " +
		"jq -c --arg canonical \"$CANON\" '[.[]|select(.directory != $canonical)]'"
}

// WorktreeCreateCmd creates a worktree through v2's project-scoped route. The
// body is Worktree.CreateInput (additionalProperties: false): the server picks
// the directory, and the base ref travels as "branch". jq builds the body so
// arbitrary ref names are quoted safely.
func (opencode2Profile) WorktreeCreateCmd(spec WorktreeSpec) string {
	args := "--arg pid \"$PID\" --arg name " + shellQuote(spec.Name)
	object := "{projectID:$pid,name:$name}"
	if spec.Base != "" {
		args += " --arg branch " + shellQuote(spec.Base)
		object = "{projectID:$pid,name:$name,branch:$branch}"
	}
	return opencode2LocatePrelude(spec.Target) +
		"curl -sf " + opencode2CurlAuth + " -X POST " + opencode2ServerURL + "/api/worktree " +
		"-H 'Content-Type: application/json' -d \"$(jq -nc " + args + " " + shellQuote(object) + ")\""
}
```

Leave `DaemonHealthCmd`, `AttachCommand`, and `DaemonStartCmd` untouched: their literals are pinned by
existing tests and are a separate concern.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/opencode2.go internal/agent/opencode2_test.go
git commit -m "fix(opencode2): create worktrees through v2's project-scoped API"
```

---

### Task 3: Validate the base before creating the worktree

**Files:**
- Modify: `internal/sandbox/vm/worktree.go` (`ResolveTarget`)
- Test: `internal/sandbox/vm/worktree_test.go`

**Interfaces:**
- Consumes: `validateWorktreeBase(ctx, sb, dir, base)` (unchanged), `defaultDir` (Task 1's target).
- Produces: `ResolveTarget` validates `spec.Base` against `defaultDir` after the reuse check and before the create; no post-create validation remains.

- [ ] **Step 1: Write the failing tests**

In `internal/sandbox/vm/worktree_test.go`, update the two base tests:

- `TestResolveTargetCreatesWithBaseValidatesAndSendsStartCommand`: the `ExecOut` key becomes `"git -C /workspace rev-parse --verify main^{commit}"` (the launch directory, not the new worktree directory). Keep the existing assertion that the v1 create body carries `git reset --hard main`.
- `TestResolveTargetCreateFailsOnUnresolvableBase`: same key change to `"git -C /workspace rev-parse --verify nope^{commit}"`, and add:

```go
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST") {
			t.Errorf("expected no create request for an unresolvable base: %q", call)
		}
	}
```

- `TestResolveTargetReuseWarnsIgnoredBase` is the reuse contract: it has no `ExecOut` entry, so if validation ever moves ahead of the reuse check it fails with "check base". Leave it as-is.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/vm/ -run TestResolveTargetCreates`
Expected: FAIL — the base is resolved in the new worktree directory, so the expected `ExecOut` key does not match and `git` is reported as missing.

- [ ] **Step 3: Move the validation**

In `internal/sandbox/vm/worktree.go`, after the reuse block and before the create:

```go
	if spec.Base != "" {
		if err := validateWorktreeBase(ctx, sb, defaultDir, spec.Base); err != nil {
			return "", fmt.Errorf("worktree %q: %w", spec.Name, err)
		}
	}
```

Delete the post-create `if spec.Base != ""` block at the end of `ResolveTarget`, and update its doc comment: the base is validated against the launch directory (no fetch) **before** the create; on reuse the base is still ignored with a warning.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/vm/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/vm
git commit -m "fix(worktree): validate the base ref before creating the worktree"
```

---

### Task 4: Docs, changelog, spec status

**Files:**
- Modify: `docs/branch-sessions.md`
- Modify: `CHANGELOG.md` (`## [Unreleased]` → `### Fixed`)
- Modify: `.operator-shared/specs/opencode2-worktree-api.md` (status line)

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

- [ ] **Step 1: Correct the worktree docs**

In `docs/branch-sessions.md`:

- Say worktrees are created under the guest home volume
  (`~/.local/share/opencode/worktree/<project>/<name>`), not `<opencode-data>/opencode/<name>`.
- Replace the unconditional "The worktree stays on its own `opencode/<name>` branch" with the two
  behaviors: `opencode` (v1) keeps its own branch with the base reset into it; `opencode2` (v2)
  leaves a **detached HEAD** at the base ref (or at the source tip without a base), so `git switch
  -c` is needed to work on a branch.
- Keep the slug rule (names like `feature/foo` are rejected) and note that it applies to both agents.
- `docs/configuration/agent.md` claims `opencode2` supports "worktree sessions"; that becomes true with
  this change, so only touch it if you find a statement the fix invalidates.

- [ ] **Step 2: Add the changelog entry**

Under `## [Unreleased]` → `### Fixed`, in the existing opencode2 bullet style, one entry: `--worktree`
for `opencode2` created worktrees through v1 routes that v2 does not serve and always failed with
404; it now uses v2's project-scoped worktree API, and an unresolvable `:<base>` ref fails before a
worktree is created (#235).

- [ ] **Step 3: Update the spec status**

Set the status line of `.operator-shared/specs/opencode2-worktree-api.md` to
`Status: implemented on <branch> (pending merge)`. If any command string or behavior diverged from the
spec during implementation, correct the spec's contract text in the same commit — the spec is the
source of truth for the next reader.

- [ ] **Step 4: Run the full check**

Run: `make check`
Expected: fmt, lint, and tests all pass. (`ci/check-docs.sh` must pass for the edited page.)

- [ ] **Step 5: Commit**

```bash
git add docs/branch-sessions.md CHANGELOG.md .operator-shared
git commit -m "docs: describe v2 worktree behavior and changelog the --worktree fix"
```

---

## Post-plan verification (manual, user-side)

The dev VM cannot start microsandbox VMs; the end-to-end paths below are the user's to run:

- `agents-sandbox run --agent opencode2 --worktree feature-x` creates a worktree and attaches; a
  second identical run reuses the same directory (no `-2`).
- `agents-sandbox run --agent opencode2 --worktree feature-x:main` ends up at `main`'s tip with a
  detached HEAD.
- `agents-sandbox run --agent opencode2 --worktree feat-x:nope` fails with
  `base "nope" does not resolve locally (no fetch is performed)` and creates nothing.
- `agents-sandbox run --agent opencode --worktree feature-x` behaves exactly as before.
