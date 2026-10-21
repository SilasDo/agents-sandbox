# `--dry-run` Preview Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the user-facing `--dry-run` a true make-no-changes preview of `run`/`shell` (and the `volume …` ops) that inspects the existing image and home volume, reports would-build/would-reuse, and performs no build, volume create, state write, VM start, attach, prompt, or network request.

**Architecture:** Split the existing mutate-everything preparation into a read-only plan path. `image` gains a pure plan/decision entry that never builds or resolves a version over the network; `volume` gains a read-only home-volume resolution; `vm.PreviewSandbox` composes both; `session.Run`/`Shell` render the preview and return before `PrepareSandbox`. `--dry-run-vm` is untouched.

**Tech Stack:** Go 1.26, cobra, microsandbox Go SDK (`msb`), Docker (moby), test mocks in `internal/sandbox/{docker,msb}` and `internal/configpaths`.

**Spec:** `.operator-shared/specs/dry-run-preview.md`

## Global Constraints

- Module `github.com/inoio/agents-sandbox`, Go 1.26.
- TDD: write the failing test, watch it fail, implement minimally, watch it pass, commit.
- `make check` (fmt, lint, test, docs linkcheck) and `make compile-all` green at the end. Run `make lint` after at most 3 edits.
- Never use `go vet`; never `go fmt`/`gofmt` by hand — use `make fmt` (golangci-lint fmt).
- Tests must not read/write real host state or make network requests (`.operator-shared/specs/test-isolation.md`). Use the docker/msb/configpaths mocks; a network call in a test must fail the test.
- State writes go through the transaction API (`WithHomeState`/`RecoverHomeState`/`WithRootState`/`RemoveHomeState`); no lock-free writer (`.operator-shared/specs/project-state-transactions.md`).
- Every new branch needs a test (near-100% coverage bar).

## Review Focus

The spec implies these input classes/failure modes; each gets a test in the owning task below.

1. `--rebuild --dry-run`: must report `would build` (forced reason) and build nothing.
2. Corrupt or unreadable `state.yaml` under `--dry-run`: no write, no error surfaced as a failure, reports `would create`.
3. `--dry-run-vm`: unaffected — still prepares fully (image ensured, volume ensured, state written) and skips only the VM.
4. `--agent-version <v>` pinned under `--dry-run`: no network, reported version is the pin.
5. `--dry-run` with `--serve-only`: a pure preview, no serve, no hang.

---

### Task 1: Read-only image preview

**Files:**
- Modify: `internal/sandbox/image/build.go` (extract a pure plan/decision helper; `EnsureImageWithClient` at `:364`)
- Create: `internal/sandbox/image/preview.go`
- Test: `internal/sandbox/image/preview_test.go`

**Interfaces:**
- Produces:
  - `type ImagePreview struct { Tag string; NeedsBuild bool; Reasons []string; AgentVersion string; ResolveVersionAtBuild bool }`
  - `func PreviewImage(ctx context.Context, a agent.Agent, projectSlug string, buildOpts BuildOptions, ui termio.UI) (ImagePreview, error)` — exported entry; warns ignored project config (parity with `EnsureImage`), reads the project Dockerfile, and never builds or resolves a version.
- Consumes: existing `RenderDockerfile`, `computeDockerfileID`, `computeImageIdentity`, `buildDecision`, `initialBuildVersions`, `runnerTag`, `readProjectDockerfile`, `WarnIgnoredProjectConfig`.

- [ ] **Step 1: Extract the pure plan/decision helper**

In `build.go`, add an unexported helper and route `EnsureImageWithClient` through it (behavior-preserving):

```go
// planRunnerImage renders the build plan and computes the local build decision.
// It performs no build and no network access.
func planRunnerImage(
	ctx context.Context, a agent.Agent, projectDockerfile []byte, projectSlug string,
	dockerFlag bool, agentVersion, identityVersion string, force bool,
) (plan buildPlan, tag string, needsBuild bool, reasons []string, err error)
```

`EnsureImageWithClient` keeps its `resolveAgentVersion` calls (network), its `buildFromPlan` call, and its control flow exactly: it calls `planRunnerImage` **once** for the initial plan and decision, and on the deferred branch it re-renders with a bare `renderBuildPlan` — it must **not** recompute `needsBuild`/`reasons` after the version resolves, because the resolved version changes the plan's identity and could flip the decision, which would change behavior.

- [ ] **Step 2: Write the failing test**

```go
// internal/sandbox/image/preview_test.go
func TestPreviewImageNeverResolvesVersion(t *testing.T) {
	WithMockAgentVersionResolver(t, func(context.Context, agent.Agent, string) (string, error) {
		t.Fatal("PreviewImage must not resolve the agent version")
		return "", nil
	})
	docker.WithNoopDockerMock(t)
	opencode, _ := agent.Lookup("opencode")
	p, err := previewRunnerImage(context.Background(), opencode, nil, "slug",
		BuildOptions{AgentVersion: "1.2.3"}, &termio.Mock{})
	// assert err nil, p.NeedsBuild true (no image present), p.Tag == runnerTag("slug","opencode")
	// assert p.AgentVersion == "1.2.3", p.ResolveVersionAtBuild false
}

func TestPreviewImageReportsReuseWhenDockerfileIDMatches(t *testing.T) {
	// render the same plan to obtain the dockerfile-id the code will compare:
	//   opencode, _ := agent.Lookup("opencode")
	//   rendered, _ := RenderDockerfile(opencode, "1.2.3", nil, false)
	//   id := computeDockerfileID(rendered, "1.2.3")
	// install a docker mock whose ImageInspectFn returns Config.Labels{dockerfileIDLabelKey: id}
	docker.WithDockerMock(t, &docker.MockDockerClient{ImageInspectFn: ...})
	// assert p.NeedsBuild false, p.Reasons empty
}

func TestPreviewImageForcedBuild(t *testing.T) {
	// BuildOptions{AgentVersion:"1.2.3", Force:true} with noop docker
	// assert p.NeedsBuild true and p.Reasons contains "forced by --rebuild"
}

func TestPreviewImageEmptyVersionReportsResolveAtBuild(t *testing.T) {
	// BuildOptions{} (no version) with noop docker
	// assert p.ResolveVersionAtBuild true
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/sandbox/image/ -run TestPreviewImage -v`
Expected: FAIL — `previewRunnerImage`/`ImagePreview` undefined.

- [ ] **Step 4: Implement `preview.go`**

```go
package image

// ImagePreview is a read-only description of what EnsureImage would do.
type ImagePreview struct {
	Tag                   string
	NeedsBuild            bool
	Reasons               []string
	AgentVersion          string
	ResolveVersionAtBuild bool
}

// previewRunnerImage renders the plan + decision for a supplied Dockerfile and
// decided version. It never builds and never resolves a version over the network.
func previewRunnerImage(
	ctx context.Context, a agent.Agent, projectDockerfile []byte, projectSlug string,
	buildOpts BuildOptions, ui termio.UI,
) (ImagePreview, error) {
	agentVersion, identityVersion, _ := initialBuildVersions(buildOpts)
	_, tag, needsBuild, reasons, err := planRunnerImage(
		ctx, a, projectDockerfile, projectSlug, buildOpts.Docker, agentVersion, identityVersion, buildOpts.Force,
	)
	if err != nil {
		return ImagePreview{}, err
	}
	return ImagePreview{
		Tag: tag, NeedsBuild: needsBuild, Reasons: reasons,
		AgentVersion: buildOpts.AgentVersion, ResolveVersionAtBuild: agentVersion == "",
	}, nil
}

// PreviewImage reports what EnsureImage would do without building anything.
func PreviewImage(ctx context.Context, a agent.Agent, projectSlug string, buildOpts BuildOptions, ui termio.UI) (ImagePreview, error) {
	WarnIgnoredProjectConfig(ui)
	return previewRunnerImage(ctx, a, readProjectDockerfile(), projectSlug, buildOpts, ui)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/sandbox/image/ -v`
Expected: PASS (all new tests plus the existing image suite).

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/image/build.go internal/sandbox/image/preview.go internal/sandbox/image/preview_test.go
git commit -m "feat(image): add a read-only runner-image preview"
```

---

### Task 2: Read-only home-volume preview

**Files:**
- Create: `internal/sandbox/volume/preview.go`
- Test: `internal/sandbox/volume/preview_test.go`

**Interfaces:**
- Produces: `func PreviewHomeVolume(ctx context.Context, client msb.Client, k state.Key, recoveredVolume string) (volume string, create bool)` — mirrors `ResolveHomeVolume`'s reuse → recover → create decision with no writes. On the create branch it returns `("", true)`: the fresh volume's name embeds a timestamp (`HomeVolumeName`), so the preview names a volume only when it will reuse one.
- Consumes: existing `state.ReadState`, `client.GetVolume`, `confirmedRecoveredVolume`.

- [ ] **Step 1: Write the failing test**

```go
// internal/sandbox/volume/preview_test.go
func TestPreviewHomeVolumeReusesRecordedPresentVolume(t *testing.T) {
	// seed state {HomeVolume: "vol-1"}; mock GetVolume("vol-1") ok
	// assert (vol, create) == ("vol-1", false) and CreateVolume not called
}

func TestPreviewHomeVolumeReusesRecoveredVolume(t *testing.T) {
	// no state; recoveredVolume "live-1"; GetVolume("live-1") ok
	// assert ("live-1", false)
}

func TestPreviewHomeVolumeReportsCreateWhenNonePresent(t *testing.T) {
	// no state, no recovered volume
	// assert ("", true)
}

func TestPreviewHomeVolumeCorruptStateReportsCreate(t *testing.T) {
	// write a corrupt state.yaml as raw bytes; recovered empty
	// assert ("", true), no error, no write
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/volume/ -run TestPreviewHomeVolume -v`
Expected: FAIL — `PreviewHomeVolume` undefined.

- [ ] **Step 3: Implement `preview.go`**

```go
package volume

// PreviewHomeVolume resolves, read-only, the home volume a run would use and
// whether it would be created. It mirrors ResolveHomeVolume without any write.
func PreviewHomeVolume(ctx context.Context, client msb.Client, k state.Key, recoveredVolume string) (string, bool) {
	if st, err := state.ReadState(k); err == nil && st.HomeVolume != "" {
		if _, err := client.GetVolume(ctx, st.HomeVolume); err == nil {
			return st.HomeVolume, false
		}
	}
	if recovered, ok := confirmedRecoveredVolume(ctx, client, recoveredVolume); ok {
		return recovered, false
	}
	return "", true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/volume/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/volume/preview.go internal/sandbox/volume/preview_test.go
git commit -m "feat(volume): add a read-only home-volume preview"
```

---

### Task 3: `vm.PreviewSandbox` and the `agentLatestVersion` seam

**Files:**
- Create: `internal/sandbox/vm/run_preview.go`
- Modify: `internal/sandbox/vm/upgrade.go` (add `SetAgentLatestVersion` near `agentLatestVersion` at `:21`)
- Modify: `internal/sandbox/vm/run_orchestrate.go` (update the `--dry-run` gate comment at `:109-115`)
- Test: `internal/sandbox/vm/run_preview_test.go`

**Interfaces:**
- Consumes: `image.PreviewImage` (Task 1), `volume.PreviewHomeVolume` (Task 2), existing `resolveWorkspaceDir`, `resolveBindLayout`, `liveHomeVolume`, `currentAgentVersion`, `currentAgentSource`, `buildOptions`, `image.ExistingAgentVersion`.
- Produces:
  - `type Preview struct { AgentVersion string; ResolveVersionAtBuild bool; ImageTag string; BuildImage bool; BuildReasons []string; HomeVolume string; CreateHome bool }`
  - `func PreviewSandbox(ctx context.Context, opts options.RunOptions, ui termio.UI) (Preview, error)`
  - `func SetAgentLatestVersion(f func(ctx context.Context, a agent.Agent) (string, error)) func(ctx context.Context, a agent.Agent) (string, error)` — sets the seam, returns the previous value.

- [ ] **Step 1: Write the failing test**

```go
// internal/sandbox/vm/run_preview_test.go
func TestPreviewSandboxReportsBuildAndCreate(t *testing.T) {
	// docker mock whose ImageInspectFn errors (image absent), msb mock (no volume), configpaths mock
	// assert p.BuildImage true, p.BuildReasons contains "runner image not present locally"
	// assert p.CreateHome true, p.HomeVolume == ""
	// assert the msb mock's CreateVolume was NOT called
}

func TestPreviewSandboxReportsReuse(t *testing.T) {
	// seed state {HomeVolume:"vol-1", AgentVersion:"1.0.0"}; msb GetVolume ok
	// docker mock returns labels matching the rendered dockerfile-id for "1.0.0"
	// assert p.BuildImage false, p.CreateHome false, p.HomeVolume == "vol-1", p.AgentVersion == "1.0.0"
}

func TestPreviewSandboxRecoversVersionFromImageLabelReadOnly(t *testing.T) {
	// no recorded version; docker mock returns the agent-version label "1.0.0"
	// assert p.AgentVersion == "1.0.0" and p.ResolveVersionAtBuild false,
	// and that no HomeState was written (state stays absent)
}

func TestPreviewSandboxPinnedVersionWinsAndNeverChecksNetwork(t *testing.T) {
	// SetAgentLatestVersion to a func that fails the test; this is also a review-focus(4) guard
	// opts.AgentVersion == "2.0.0"
	// assert p.AgentVersion == "2.0.0"
}

func TestPreviewSandboxCorruptStateReportsCreate(t *testing.T) { // review-focus(2)
	// corrupt state.yaml + no live VM mount
	// assert p.CreateHome true, no error, state file byte-unchanged
}

func TestPreviewSandboxForcedRebuildReportsBuild(t *testing.T) { // review-focus(1)
	// opts.Rebuild true with noop docker
	// assert p.BuildImage true and BuildReasons contains "forced by --rebuild"
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/vm/ -run TestPreviewSandbox -v`
Expected: FAIL — `PreviewSandbox`/`Preview` undefined.

- [ ] **Step 3: Add the seam setter**

In `upgrade.go`, beside `agentLatestVersion`:

```go
// SetAgentLatestVersion replaces the agent-latest-version seam and returns the
// previous value. Tests use it to keep the upgrade check off the network.
func SetAgentLatestVersion(
	f func(ctx context.Context, a agent.Agent) (string, error),
) func(ctx context.Context, a agent.Agent) (string, error) {
	orig := agentLatestVersion
	agentLatestVersion = f
	return orig
}
```

- [ ] **Step 4: Implement `run_preview.go`**

```go
// Preview describes what a run/shell would do, without doing it.
type Preview struct {
	AgentVersion          string
	ResolveVersionAtBuild bool
	ImageTag              string
	BuildImage            bool
	BuildReasons          []string
	HomeVolume            string
	CreateHome            bool
}

// PreviewSandbox resolves the read-only plan for a run: the agent version, the
// image build decision, and the home volume, using only local inspection.
func PreviewSandbox(ctx context.Context, opts options.RunOptions, ui termio.UI) (Preview, error) {
	cwd, err := resolveWorkspaceDir()
	if err != nil {
		return Preview{}, err
	}
	resolveBindLayout(cwd, ui)
	a, ok := agent.Lookup(opts.Agent)
	if !ok {
		return Preview{}, fmt.Errorf("unknown agent %q", opts.Agent)
	}
	projectSlug := git.ProjectSlug()
	k := state.Key{Slug: projectSlug, Agent: a.Name()}

	version := opts.AgentVersion
	if version == "" {
		version = currentAgentVersion(k)
	}
	if version == "" {
		if recovered, ok := image.ExistingAgentVersion(ctx, k.Slug, k.Agent); ok {
			version = recovered
		}
	}

	img, err := image.PreviewImage(ctx, a, projectSlug,
		buildOptions(opts, version, opts.Rebuild, currentAgentSource(k) == agentSourceUser), ui)
	if err != nil {
		return Preview{}, err
	}

	client := msb.Get()
	homeVol, create := volume.PreviewHomeVolume(ctx, client, k, liveHomeVolume(ctx, client, k, ui))

	return Preview{
		AgentVersion: version, ResolveVersionAtBuild: img.ResolveVersionAtBuild,
		ImageTag: img.Tag, BuildImage: img.NeedsBuild, BuildReasons: img.Reasons,
		HomeVolume: homeVol, CreateHome: create,
	}, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/sandbox/vm/ -v`
Expected: PASS.

- [ ] **Step 6: Update the `--dry-run` gate comment**

In `run_orchestrate.go` at `:109-115`, replace the note that says `--dry-run` still runs image build, home-volume creation, and state writes (and links issue #228 as open) with the new contract: `--dry-run` is a read-only preview rendered by the session layer (`session.Run`/`Shell` call `vm.PreviewSandbox` and return before `PrepareSandbox`), so these in-`PrepareSandbox` gates are reached only by direct callers/tests that pass `opts.DryRun`; keep the description of what the gates suppress (`determineAgentVersion`'s upgrade check and legacy fold, `prepareHomeVolume`'s migration offer) and drop the "tracked in issue #228" line. Do not change any gate's behavior.

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/vm/run_preview.go internal/sandbox/vm/run_preview_test.go internal/sandbox/vm/upgrade.go internal/sandbox/vm/run_orchestrate.go
git commit -m "feat(vm): add a read-only sandbox preview and the agent-version seam"
```

---

### Task 4: Wire the preview into `session.Run`/`Shell`

**Files:**
- Modify: `internal/sandbox/session/run.go` (Run `:131`, Shell `:208`)
- Test: `internal/sandbox/session/run_test.go` (`TestRunDryRun` `:202`, `TestShellDryRun` `:417`; add preview tests)

**Interfaces:**
- Consumes: `vm.PreviewSandbox` / `vm.Preview` (Task 3).
- Produces: a `previewSandbox` seam var (mirrors `prepareSandbox`) and `renderPreview(ui, p, shell bool)`.

- [ ] **Step 1: Write the failing test**

Replace `TestRunDryRun`/`TestShellDryRun` and add rendering tests. Stub the seam, not `prepareSandbox`:

```go
func TestRunDryRunRendersPreview(t *testing.T) {
	orig := previewSandbox
	previewSandbox = func(context.Context, options.RunOptions, termio.UI) (sandbox.Preview, error) {
		return sandbox.Preview{
			AgentVersion: "1.0.0", ImageTag: "agents-sandbox-runner-x", BuildImage: true,
			BuildReasons: []string{"runner image not present locally"}, HomeVolume: "vol-1", CreateHome: true,
		}, nil
	}
	defer func() { previewSandbox = orig }()
	ui := &termio.Mock{}
	if err := Run(context.Background(), options.RunOptions{DryRun: true}, ui); err != nil { t.Fatal(err) }
	want := []string{
		"dry-run: agent version 1.0.0",
		"dry-run: would build runner image agents-sandbox-runner-x (runner image not present locally)",
		"dry-run: would create home volume",
		"dry-run: would start agent session",
	}
	// assert ui.InfoCalls equals want
}

func TestShellDryRunRendersPreview(t *testing.T) {
	// stub previewSandbox with a reuse preview; assert the four lines, last is
	// "dry-run: would start interactive shell session"
}

func TestRunDryRunResolveAtBuildMessage(t *testing.T) {
	// Preview{ResolveVersionAtBuild: true, ...}; assert first line
	// "dry-run: agent version resolved at build time"
}

func TestRunDryRunPropagatesPreviewError(t *testing.T) {
	// previewSandbox returns an error; assert Run returns it
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/session/ -run 'DryRun' -v`
Expected: FAIL — `previewSandbox`/rendering undefined and old messages gone.

- [ ] **Step 3: Implement**

In `run.go`, add the seam beside `prepareSandbox`:

```go
var previewSandbox = func(ctx context.Context, opts options.RunOptions, ui termio.UI) (sandbox.Preview, error) {
	return sandbox.PreviewSandbox(ctx, opts, ui)
}
```

Add the renderer (add the `strings` import to `run.go`):

```go
func renderPreview(ui termio.UI, p sandbox.Preview, shell bool) {
	if p.ResolveVersionAtBuild {
		ui.Infof("dry-run: agent version resolved at build time")
	} else {
		ui.Infof("dry-run: agent version %s", p.AgentVersion)
	}
	if p.BuildImage {
		ui.Infof("dry-run: would build runner image %s (%s)", p.ImageTag, strings.Join(p.BuildReasons, "; "))
	} else {
		ui.Infof("dry-run: would reuse runner image %s", p.ImageTag)
	}
	if p.CreateHome {
		ui.Infof("dry-run: would create home volume")
	} else {
		ui.Infof("dry-run: would reuse home volume %s", p.HomeVolume)
	}
	if shell {
		ui.Infof("dry-run: would start interactive shell session")
	} else {
		ui.Infof("dry-run: would start agent session")
	}
}
```

In `Run`, before `prepareSandbox`:

```go
if opts.DryRun {
	p, err := previewSandbox(ctx, opts, ui)
	if err != nil {
		return err
	}
	renderPreview(ui, p, false)
	return nil
}
```

In `Shell`, before `prepareSandbox`, the same with `renderPreview(ui, p, true)`. Remove the now-dead `if opts.DryRun { … }` early-returns after `prepareSandbox` in both (the `DryRunVM` ones stay).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/session/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/session/run.go internal/sandbox/session/run_test.go
git commit -m "feat(session): render the dry-run preview before preparing the sandbox"
```

---

### Task 5: Fail-fast agent-version seam default in `cmd`

**Files:**
- Modify: `cmd/agents-sandbox/main_test.go` (`TestMain` `:14`)

**Interfaces:**
- Consumes: `vm.SetAgentLatestVersion` (Task 3).

- [ ] **Step 1: Add the default**

In `TestMain`, before `m.Run()`, add:

```go
// Keep the upgrade check off the network: any test that reaches it must stub
// the seam explicitly (a panic, not an error, so a swallowed warning cannot
// hide the leak).
sandbox.SetAgentLatestVersion(func(context.Context, agent.Agent) (string, error) {
	panic("agent latest-version lookup not stubbed in this test")
})
```

Add the `agent` import.

- [ ] **Step 2: Run the package tests**

Run: `go test ./cmd/agents-sandbox/ 2>&1 | tail -20`
Expected: the ONLY failures are the two already-broken run/shell `--dry-run` end-to-end tests (`TestRunShellDryRunRun`/`TestRunShellDryRunShell`), which the plan migrates in Task 7; no test should panic with the fail-fast message (a panic means a test reached the network path and must stub `SetAgentLatestVersion` for its own case — none is expected before Task 7).

- [ ] **Step 3: Commit**

```bash
git add cmd/agents-sandbox/main_test.go
git commit -m "test(cmd): fail fast on the agent latest-version seam"
```

---

### Task 6: `volume <name> --dry-run` performs no image ensure

**Files:**
- Modify: `cmd/agents-sandbox/commands_system.go` (`buildVolumeOpsCmd` RunE, `:112-148`)
- Test: `cmd/agents-sandbox/cli_volume_test.go` (add)

**Interfaces:**
- No new signatures. `volumeOp` already tolerates a zero `image.ImageInfo` on its dry-run early-return (`volume/operations.go:216`).

- [ ] **Step 1: Write the failing test**

```go
func TestVolumeOpsDryRunDoesNotEnsureImage(t *testing.T) {
	// seed state {HomeVolume:"vol-1"}; install a docker mock whose ImageInspectFn
	// and ImageBuild record calls (or fail the test).
	// run `volume migrate --dry-run`; assert exit nil, the would-do message printed,
	// ImageInspect/ImageBuild never called, and CreateVolume never called.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/agents-sandbox/ -run TestVolumeOpsDryRunDoesNotEnsureImage -v`
Expected: FAIL — `ImageInspect` is called by the current `EnsureImage`.

- [ ] **Step 3: Implement**

In `buildVolumeOpsCmd`'s `RunE`, skip the ensure under `--dry-run`:

```go
var info image.ImageInfo
if !dryRun {
	info, err = image.EnsureImage(
		c.Context(), a, projectSlug,
		image.BuildOptions{Force: false, AgentVersion: "", UserProvided: false, Docker: docker},
		ui,
	)
	if err != nil {
		return fmt.Errorf("ensure image: %w", err)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/agents-sandbox/ -run 'Volume|volume' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/agents-sandbox/commands_system.go cmd/agents-sandbox/cli_volume_test.go
git commit -m "fix(cli): volume --dry-run no longer ensures the runner image"
```

---

### Task 7: Migrate the run/shell CLI tests to `--dry-run-vm` and pin the preview

**Files:**
- Modify: `cmd/agents-sandbox/cli_run_shell_test.go` (`TestRunShellDryRunRun` `:96`, `TestRunShellDryRunShell` `:129`)
- Modify: `cmd/agents-sandbox/cli_bare_checkout_test.go` (`TestRunCommandReadsBareRoot` `:75`)
- Test: same files (add)

**Interfaces:** none new.

- [ ] **Step 1: Migrate the prepare-path tests to `--dry-run-vm`**

- `cli_run_shell_test.go`: change `"run", "--dry-run"` → `"run", "--dry-run-vm"` and `"shell", "--dry-run"` → `"shell", "--dry-run-vm"`; assert `"dry-run: Would run agent session"` becomes `"dry-run: Would start agent session in VM"` (and the shell equivalent); delete the `"dry-run-vm: auto-enabled"` verbose assertion.
- `cli_bare_checkout_test.go`: change `cmdRun, flagDryRun` → `cmdRun, flagDryRunVM` (this keeps `EnsureImage` running so the ignored-worktree warning still fires).

- [ ] **Step 2: Run tests**

Run: `go test ./cmd/agents-sandbox/ -run 'RunShell|BareRoot' -v`
Expected: PASS.

- [ ] **Step 3: Write the new preview CLI tests**

```go
func TestRunDryRunPreviewCLI(t *testing.T) {
	// fresh project, docker mock whose ImageInspectFn errors (absent image) + mocked msb;
	// pin no version resolution with image.WithMockAgentVersionResolver(t, a func that fails the test)
	// `run --dry-run`; assert exit nil and the four InfoCalls:
	//   "dry-run: agent version resolved at build time"
	//   "dry-run: would build runner image <tag> (runner image not present locally)"
	//   "dry-run: would create home volume"
	//   "dry-run: would start agent session"
	// assert the docker mock built nothing and the msb mock created no volume.
}

func TestShellDryRunPreviewCLI(t *testing.T) {
	// same via `shell --dry-run`; last line "dry-run: would start interactive shell session"
}

func TestRunDryRunWithPinnedVersionReportsPin(t *testing.T) { // review-focus(4)
	// `run --dry-run --agent-version 9.9.9`; assert first line
	// "dry-run: agent version 9.9.9" and the agentLatestVersion seam was not reached
}

func TestRunDryRunServeOnlyIsPurePreview(t *testing.T) { // review-focus(5)
	// `run --dry-run --serve-only`; assert it returns promptly (no serve/hang)
	// and renders the four preview lines
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/agents-sandbox/ -run 'DryRun' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/agents-sandbox/cli_run_shell_test.go cmd/agents-sandbox/cli_bare_checkout_test.go
git commit -m "test(cli): use --dry-run-vm for the prepare path and pin the --dry-run preview"
```

---

### Task 8: Docs, changelog, and spec status

**Files:**
- Modify: `docs/commands.md` (`--dry-run` row `:45`)
- Modify: `.operator-shared/specs/upgrade-state-scoping.md` (`:92-95`, `:214`)
- Modify: `.operator-shared/specs/project-state-transactions.md` (`:76-78`, `:241-248`)
- Modify: `CHANGELOG.md` (`[Unreleased]`)
- Modify: `.operator-shared/specs/dry-run-preview.md` (status line)

- [ ] **Step 1: Update the user docs**

`docs/commands.md`, `--dry-run` row: describe the preview — reports the agent version, whether the runner image would build or be reused (with reasons), and whether the home volume would be created or reused, without building, creating a volume, writing state, starting a VM, or making a network request. Leave `--dry-run-vm`'s wording unchanged. Keep root-level doc link style (rendered HTML links into `docs/`).

- [ ] **Step 2: Reconcile the two specs**

- `upgrade-state-scoping.md`: narrow the "`--dry-run` is side-effect-free" clause to the upgrade check it was written for and point to `dry-run-preview.md`.
- `project-state-transactions.md`: replace the "documentation-only, tracked in #228" resolution and the finding-7 note with a pointer to `dry-run-preview.md`.

- [ ] **Step 3: Changelog and spec status**

- `CHANGELOG.md` `[Unreleased]`: `--dry-run` no longer builds the runner image, creates a home volume, or writes project state; it is a read-only preview and resolves no version over the network. `volume migrate/reset/edit --dry-run` likewise no longer ensures the image.
- `dry-run-preview.md`: set a Status line (implemented on `<branch>`).

- [ ] **Step 4: Whole-project verification**

Run: `make check`
Expected: PASS (fmt, lint, tests, docs linkcheck).
Run: `make compile-all`
Expected: PASS, with `[no tests to run]` per package (compile-only).

- [ ] **Step 5: Commit**

```bash
git add docs/commands.md CHANGELOG.md .operator-shared/specs/upgrade-state-scoping.md .operator-shared/specs/project-state-transactions.md .operator-shared/specs/dry-run-preview.md
git commit -m "docs: describe the side-effect-free --dry-run preview"
```
