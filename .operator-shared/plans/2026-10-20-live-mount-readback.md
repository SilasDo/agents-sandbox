# Live Mount Read-back Implementation Plan

**Status:** implemented on `docs/223-reconfig-dead-detectors` (pending merge), commits `e43380e`..`92e59a0`.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the dead `cfg.Volumes` readers by reading a live VM's mounts from the persisted config JSON, so `tmp_size`/`workspace-quota`/home-volume changes are detected, home-volume recovery works, and `sandbox inspect` reports mounts.

**Architecture:** One seam addition — `msb.SandboxHandle.ConfigMounts()`, a thin wrapper over the exported `SandboxHandle.ConfigJSON()` plus a new pure parser `msb.ParseConfigMounts` — and three call sites that consume it (`decideReconfig`, `liveHomeVolume`, `InspectSandbox`). `reconfig.PlanReconfig` keeps its signature and its `cfg.Volumes`-based comparison; `decideReconfig` fills `cfg.Volumes` from the seam before planning, so the three detectors (and their existing unit tests) need no change.

**Tech Stack:** Go 1.26, `github.com/inoio/agents-sandbox`, microsandbox Go SDK pinned at `v0.7.8` (no SDK change), `testify`-free stdlib tests with the repo's `msb` mocks.

**Spec:** `.operator-shared/specs/live-mount-readback.md`

## Global Constraints

- Module root is the repo root; module path `github.com/inoio/agents-sandbox`.
- No new dependencies. Do not modify or vendor the microsandbox SDK; the fix works around its `SandboxConfig.UnmarshalJSON` (`sdk/go@v0.7.8`, `options.go`) dropping `mounts`.
- `internal/sandbox/msb` must not import `internal/sandbox/vm` (import direction: `vm` → `msb`).
- Do not change `reconfig.PlanReconfig`'s signature or its meaning: it compares `cfg.Volumes` and the caller is responsible for that map reflecting the live VM.
- Run `make fmt` and `make lint` after each task; `make lint` is `golangci-lint`, never `go vet`. In a worktree run `golangci-lint cache clean` first (see `AGENTS.md`).
- Tests must pass without KVM or a docker daemon. `make test` for the default set; `make compile-all` before the final commit.
- Commit messages follow the repo's conventional style (`fix(scope): …`, `test(scope): …`, `docs: …`).

## Review Focus

- A persisted config with **no `mounts` key** (a runtime that does not record mounts, or a foreign sandbox) must produce no error and no mounts — never a spurious recreate or a failed run.
- An **unrecognized mount `type`** from a newer runtime must be skipped while the recognized mounts are still returned, so one new variant cannot disable detection of `/tmp`, `/workspace`, and `/home/dev`.
- **Invalid `config_json`** must not crash `decideReconfig` or `liveHomeVolume`; they log and continue with no mounts, reproducing pre-#223 behaviour rather than aborting the startup.
- **Duplicate `guest` paths** in `mounts` must resolve deterministically (last entry wins) so a repeated mount cannot make decision output order-dependent (`specs/reconfig-decision.md` determinism).
- **`Owned`/`DiskImage` mounts** must map without colliding with the three managed guest paths, and an `Owned` `storage.kind` of `"directory"`/`"disk"` (upstream snake_case) must not be conflated with the SDK's `VolumeKind` value `"dir"`.

---

### Task 1: Parse mounts out of a persisted sandbox config JSON

**Files:**
- Create: `internal/sandbox/msb/configmounts.go`
- Test: `internal/sandbox/msb/configmounts_test.go`

**Interfaces:**
- Consumes: `msbSdk "github.com/superradcompany/microsandbox/sdk/go"` (`MountConfig`, `Mount.Bind/Named/Tmpfs/Owned/Disk`, `MountOptions`, `TmpfsOptions`, `OwnedVolumeOptions`, `DiskOptions`, `VolumeKindDir`, `VolumeKindDisk`).
- Produces: `func ParseConfigMounts(configJSON string) (map[string]msbSdk.MountConfig, error)` — mounts keyed by guest path, `nil` when the JSON has no `mounts` key.

- [x] **Step 1: Write the failing test**

Create `internal/sandbox/msb/configmounts_test.go` in `package msb`:

```go
package msb

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Real persisted shape: SandboxConfig flattens SandboxSpec, whose mounts are an
// internally tagged array (microsandbox-types domain.rs).
const persistedConfigWithMounts = `{
  "name": "agents-sandbox-vm-proj-opencode",
  "memory_mib": 16384,
  "mounts": [
    {"type":"Named","name":"agents-sandbox-home-proj-opencode-20261016T101010","guest":"/home/dev","options":{"readonly":false}},
    {"type":"Bind","host":"/host/proj","guest":"/workspace","quota_mib":16384,"options":{"host_permissions":"mirror"}},
    {"type":"Tmpfs","guest":"/tmp","size_mib":2048,"options":{}},
    {"type":"Owned","guest":"/data","storage":{"kind":"directory","quota_mib":512},"options":{}},
    {"type":"DiskImage","host":"/images/seed.img","guest":"/disk","format":"raw","fstype":"ext4","options":{"readonly":true}}
  ]
}`

func TestParseConfigMountsMapsEveryVariant(t *testing.T) {
	got, err := ParseConfigMounts(persistedConfigWithMounts)
	if err != nil {
		t.Fatalf("ParseConfigMounts() error = %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("ParseConfigMounts() returned %d mounts, want 5: %+v", len(got), got)
	}

	home := got["/home/dev"]
	if home.Kind() != msbSdk.MountKindNamed || home.Named != "agents-sandbox-home-proj-opencode-20261016T101010" {
		t.Errorf("home mount = %+v, want named agents-sandbox-home-proj-opencode-20261016T101010", home)
	}

	workspace := got["/workspace"]
	if workspace.Kind() != msbSdk.MountKindBind || workspace.Bind != "/host/proj" || workspace.QuotaMiB != 16384 {
		t.Errorf("workspace mount = %+v, want bind /host/proj quota 16384", workspace)
	}

	tmp := got["/tmp"]
	if tmp.Kind() != msbSdk.MountKindTmpfs || tmp.SizeMiB != 2048 {
		t.Errorf("tmp mount = %+v, want tmpfs size 2048", tmp)
	}

	if owned := got["/data"]; owned.Kind() != msbSdk.MountKindOwned || owned.Owned != string(msbSdk.VolumeKindDir) {
		t.Errorf("owned mount = %+v, want owned kind dir", owned)
	}

	disk := got["/disk"]
	if disk.Kind() != msbSdk.MountKindDisk || disk.Disk != "/images/seed.img" || !disk.Readonly {
		t.Errorf("disk mount = %+v, want disk /images/seed.img read-only", disk)
	}
}

func TestParseConfigMountsWithoutMountsKeyIsNotAnError(t *testing.T) {
	got, err := ParseConfigMounts(`{"name":"vm","memory_mib":4096}`)
	if err != nil {
		t.Fatalf("ParseConfigMounts() error = %v, want nil for a config without mounts", err)
	}
	if got != nil {
		t.Errorf("ParseConfigMounts() = %+v, want nil", got)
	}
}

func TestParseConfigMountsEmptyArrayIsAnEmptyMap(t *testing.T) {
	got, err := ParseConfigMounts(`{"mounts":[]}`)
	if err != nil {
		t.Fatalf("ParseConfigMounts() error = %v", err)
	}
	if len(got) != 0 || got == nil {
		t.Errorf("ParseConfigMounts() = %+v, want a non-nil empty map", got)
	}
}

func TestParseConfigMountsSkipsUnknownType(t *testing.T) {
	raw := `{"mounts":[
	  {"type":"Tmpfs","guest":"/tmp","size_mib":2048},
	  {"type":"SomethingNew","guest":"/future"}
	]}`
	got, err := ParseConfigMounts(raw)
	if err != nil {
		t.Fatalf("ParseConfigMounts() error = %v", err)
	}
	if len(got) != 1 || got["/tmp"].SizeMiB != 2048 {
		t.Errorf("ParseConfigMounts() = %+v, want only the recognized /tmp mount", got)
	}
}

func TestParseConfigMountsLastDuplicateGuestWins(t *testing.T) {
	raw := `{"mounts":[
	  {"type":"Tmpfs","guest":"/tmp","size_mib":2048},
	  {"type":"Tmpfs","guest":"/tmp","size_mib":4096}
	]}`
	got, err := ParseConfigMounts(raw)
	if err != nil {
		t.Fatalf("ParseConfigMounts() error = %v", err)
	}
	if got["/tmp"].SizeMiB != 4096 {
		t.Errorf("ParseConfigMounts()[/tmp] = %+v, want the last entry (4096)", got["/tmp"])
	}
}

func TestParseConfigMountsRejectsMalformedJSON(t *testing.T) {
	if _, err := ParseConfigMounts("not json"); err == nil {
		t.Error("ParseConfigMounts() error = nil, want an error for malformed JSON")
	}
}

func TestParseConfigMountsRejectsMountWithoutGuest(t *testing.T) {
	if _, err := ParseConfigMounts(`{"mounts":[{"type":"Tmpfs","size_mib":1}]}`); err == nil {
		t.Error("ParseConfigMounts() error = nil, want an error for a mount without a guest path")
	}
}
```

- [x] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/msb/ -run TestParseConfigMounts -v`
Expected: FAIL — `undefined: ParseConfigMounts`.

- [x] **Step 3: Implement `ParseConfigMounts` in `internal/sandbox/msb/configmounts.go`**

Decode into a private mirror of the persisted entries, then map each recognized `type` onto the SDK's `Mount` factories:

```go
func ParseConfigMounts(configJSON string) (map[string]msbSdk.MountConfig, error)
```

- `""` or a config without a `mounts` key → `nil, nil`. A present-but-empty array → `{}, nil` (non-nil).
- Invalid JSON → wrapped error. Any entry with an empty `guest` → error naming the `type`.
- Unrecognized `type` → skip the entry (forward compatibility), keep going.
- `Bind` → `Mount.Bind(host, MountOptions{Readonly, Noexec, Nosuid, Nodev, QuotaMiB: quota_mib})`.
- `Named` → `Mount.Named(name, MountOptions{…})`.
- `Tmpfs` → `Mount.Tmpfs(TmpfsOptions{SizeMiB: size_mib, Readonly, Noexec, Nosuid, Nodev})`.
- `Owned` → `Mount.Owned(OwnedVolumeOptions{Kind: kind, SizeMiB: capacity_mib, QuotaMiB: quota_mib})` where `storage.kind`
  `"directory"` maps to `msbSdk.VolumeKindDir` and `"disk"` to `msbSdk.VolumeKindDisk` (upstream spells the directory kind
  `"directory"`, the SDK constant is `"dir"` — do not pass it through unchanged).
- `DiskImage` → `Mount.Disk(host, DiskOptions{Format: format, Fstype: fstype, Readonly, Noexec, Nosuid, Nodev})`.
- `readonly`/`noexec`/`nosuid`/`nodev` come from the entry's `options` object.

- [x] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sandbox/msb/ -run TestParseConfigMounts -v`
Expected: PASS (7 tests).

- [x] **Step 5: Commit**

```bash
git add internal/sandbox/msb/configmounts.go internal/sandbox/msb/configmounts_test.go
git commit -m "fix(msb): parse a live VM's mounts from the persisted config JSON"
```

---

### Task 2: Expose the live mounts on the SandboxHandle seam

**Files:**
- Modify: `internal/sandbox/msb/msb.go` (the `SandboxHandle` interface ~line 79, and `realSandboxHandle.Config` ~line 606)
- Modify: `internal/sandbox/msb/testmock.go` (`MockSandboxHandle`, ~line 425)
- Test: `internal/sandbox/msb/msb_coverage_test.go` (append; it already imports `errors` and the SDK)

**Interfaces:**
- Consumes: `ParseConfigMounts` from Task 1.
- Produces: `SandboxHandle.ConfigMounts() (map[string]msbSdk.MountConfig, error)` on the seam; `MockSandboxHandle` gains `Mounts map[string]msbSdk.MountConfig` and `MountsErr error`.

- [x] **Step 1: Write the failing test**

Append to `internal/sandbox/msb/msb_coverage_test.go`:

```go
func TestMockSandboxHandleConfigMounts(t *testing.T) {
	want := map[string]msbSdk.MountConfig{
		"/tmp": msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{SizeMiB: 2048}),
	}
	m := &MockSandboxHandle{Mounts: want}
	got, err := m.ConfigMounts()
	if err != nil {
		t.Fatalf("ConfigMounts() error = %v", err)
	}
	if got["/tmp"].SizeMiB != 2048 {
		t.Errorf("ConfigMounts() = %+v, want the seeded mounts", got)
	}

	failing := &MockSandboxHandle{MountsErr: errors.New("mounts boom")}
	if _, err := failing.ConfigMounts(); err == nil {
		t.Error("ConfigMounts() error = nil, want the configured MountsErr")
	}
}
```

- [x] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/msb/ -run TestMockSandboxHandleConfigMounts -v`
Expected: FAIL — `m.ConfigMounts undefined` / `unknown field Mounts`.

- [x] **Step 3: Implement the seam method**

In `SandboxHandle`, add next to `Config()`:

```go
	// ConfigMounts returns the mounts the persisted config records, keyed by
	// guest path. Config() cannot supply them: the SDK's
	// SandboxConfig.UnmarshalJSON drops the persisted mounts, so
	// cfg.Volumes is always empty on a read-back (specs/live-mount-readback.md).
	ConfigMounts() (map[string]msbSdk.MountConfig, error)
```

In `realSandboxHandle`:

```go
func (w realSandboxHandle) ConfigMounts() (map[string]msbSdk.MountConfig, error) {
	return ParseConfigMounts(w.handle.ConfigJSON())
}
```

In `testmock.go`, add the two fields to `MockSandboxHandle` and return them (mirroring `Config`/`ConfigErr`):

```go
func (m *MockSandboxHandle) ConfigMounts() (map[string]msbSdk.MountConfig, error) {
	if m.MountsErr != nil {
		return nil, m.MountsErr
	}
	return m.Mounts, nil
}
```

Add a code comment on the mock method noting the divergence it makes explicit: the real handle derives mounts from the
persisted JSON while the mock is seeded directly, because the wiring line cannot be exercised without the FFI runtime —
the parser it calls is covered by Task 1.

- [x] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sandbox/msb/ -v`
Expected: PASS.

- [x] **Step 5: Verify the seam change did not break the mocks' users**

Run: `go build ./... && go test ./internal/sandbox/... -run XXX`
Expected: builds clean (any type asserting `msb.SandboxHandle` must now implement `ConfigMounts`; the mocks in
`internal/sandbox/volume` and `internal/sandbox/vm` are the `MockSandboxHandle` type, so no other implementation exists).

- [x] **Step 6: Commit**

```bash
git add internal/sandbox/msb/msb.go internal/sandbox/msb/testmock.go internal/sandbox/msb/msb_coverage_test.go
git commit -m "feat(msb): expose ConfigMounts on the SandboxHandle seam"
```

---

### Task 3: Recreate the VM on a tmp-size / workspace-quota / home-volume change

**Files:**
- Modify: `internal/sandbox/vm/reconfig.go` (`decideReconfig`, ~lines 145-183)
- Test: `internal/sandbox/vm/reconfig_livemounts_test.go` (create)

**Interfaces:**
- Consumes: `SandboxHandle.ConfigMounts()` (Task 2); `reconfig.PlanReconfig` unchanged.
- Produces: nothing new; `decideReconfig` fills `curCfg.Volumes` before planning.

- [x] **Step 1: Write the failing test**

Create `internal/sandbox/vm/reconfig_livemounts_test.go` in `package vm`, modelled on the existing
`TestDecideReconfigHomeQuit` harness (`internal/sandbox/vm/reconfig_quit_test.go`):

```go
// TestDecideReconfigDetectsTmpSizeChangeFromLiveMounts covers issue #223: the
// VM's mounts come from ConfigMounts, because Config() leaves cfg.Volumes empty,
// so a changed tmp size must still recreate the VM.
func TestDecideReconfigDetectsTmpSizeChangeFromLiveMounts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sh := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{Image: "img:tag", CPUs: 4, MemoryMiB: 4096},
		Mounts: map[string]msbSdk.MountConfig{
			mounts.TmpMountPath:       msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{SizeMiB: 2048}),
			mounts.WorkspaceMountPath: msbSdk.Mount.Bind("/host/repo", msbSdk.MountOptions{QuotaMiB: 16384}),
			mounts.VMHomeDir:          msbSdk.Mount.Named("vol", msbSdk.MountOptions{}),
		},
	}
	sh.ConnectSb = &msb.MockSandbox{}
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) { return sh, nil }
	msb.WithMsbMock(t, mock)

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	recreate, _, _, _, err := decideReconfig(
		context.Background(),
		mock,
		volume.NewManager(&ui),
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{TmpSize: "4G"},
		image.ImageInfo{Tag: "img:tag", Digest: "sha256:new"},
		"vol",
		state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:new"},
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if !recreate {
		t.Error("expected a recreate for a tmp size change read from the live VM's mounts")
	}
}
```

Note: `options.RunOptions{TmpSize: "4G"}` against a live `/tmp` of 2048 MiB is the change; the image digest matches and
the home volume matches, so nothing else can trigger the recreate. Add a sibling subtest (or a second test function)
that pins the inverse — a VM whose mounts match the options (`TmpSize: "2G"`, `WorkspaceQuota: "16G"`, home `"vol"`)
yields `recreate == false`, so the fix cannot reintroduce a rebuild-every-run regression.

Also add `TestDecideReconfigMountsReadFailureDegrades`, reusing the same harness with
`sh.MountsErr = errors.New("mounts boom")` and an `options.RunOptions{}` that matches the (now unreadable) VM: the call
must return no error and `recreate == false` — a mount read failure degrades, it does not abort the startup (Review
Focus).

- [x] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/vm/ -run TestDecideReconfigDetectsTmpSizeChangeFromLiveMounts -v`
Expected: FAIL — `recreate` is false, because `cfg.Volumes` is empty and `tmpSizeChange` returns no change.

- [x] **Step 3: Implement**

In `decideReconfig`, inside the `if handle != nil` block, after `curCfg, cfgErr = handle.Config()`, apply the live
mounts. Add this helper to `internal/sandbox/vm/reconfig.go`:

```go
// applyLiveMounts fills cfg.Volumes with the mounts the persisted config
// records, which the SDK's Config() drops (specs/live-mount-readback.md). A read
// failure is reported at verbose level and leaves cfg.Volumes untouched, so the
// decision degrades to the pre-#223 behaviour instead of aborting the run.
func applyLiveMounts(cfg *msbSdk.SandboxConfig, handle msb.SandboxHandle, ui termio.UI) {
	if cfg == nil {
		return
	}
	liveMounts, err := handle.ConfigMounts()
	if err != nil {
		ui.Verbosef("reading VM mounts failed: %v (continuing)", err)
		return
	}
	cfg.Volumes = liveMounts
}
```

and call it from `decideReconfig`:

```go
		curCfg, cfgErr = handle.Config()
		if cfgErr != nil {
			ui.Verbosef("reading existing VM config failed: %v (continuing)", cfgErr)
		}
		applyLiveMounts(curCfg, handle, ui)
```

- [x] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sandbox/vm/ -run TestDecideReconfig -v`
Expected: PASS.

- [x] **Step 5: Run the affected packages**

Run: `go test ./internal/sandbox/vm/ ./internal/sandbox/reconfig/ -count=1`
Expected: PASS — the `reconfig` package tests build `cfg.Volumes` directly and must be unaffected (they pin
`PlanReconfig`'s contract).

- [x] **Step 6: Commit**

```bash
git add internal/sandbox/vm/reconfig.go internal/sandbox/vm/reconfig_livemounts_test.go
git commit -m "fix(vm): apply live VM mounts before the reconfig decision"
```

---

### Task 4: Recover the live home volume through the seam

**Files:**
- Modify: `internal/sandbox/vm/run_orchestrate.go` (`liveHomeVolume`, ~line 370)
- Test: `internal/sandbox/vm/run_orchestrate_test.go` (`TestLiveHomeVolumeReturnsMountedVolume`, `TestPrepareHomeVolumeRecoversLostStateFromLiveVM`)

**Interfaces:**
- Consumes: `SandboxHandle.ConfigMounts()` (Task 2).
- Produces: nothing new; `liveHomeVolume` reads the `Named` mount at `mounts.VMHomeDir`.

- [x] **Step 1: Update the two tests to seed mounts through the seam (they now fail by construction)**

In `TestLiveHomeVolumeReturnsMountedVolume` and `TestPrepareHomeVolumeRecoversLostStateFromLiveVM`, the
`MockSandboxHandle` literal currently sets `Cfg: &msbSdk.SandboxConfig{Volumes: …}`. Replace that with the mock's new
`Mounts` field, keeping `Cfg` only where the test needs other config fields:

```go
			return &msb.MockSandboxHandle{
				Cfg: &msbSdk.SandboxConfig{Image: "img:tag"},
				Mounts: map[string]msbSdk.MountConfig{
					mounts.VMHomeDir: msbSdk.Mount.Named("home-vol", msbSdk.MountOptions{}),
				},
			}, nil
```

(For the recovery test use the existing `recovered` name instead of `home-vol`.)

- [x] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/vm/ -run 'TestLiveHomeVolume|TestPrepareHomeVolumeRecoversLostState' -v`
Expected: FAIL — `liveHomeVolume` still reads `cfg.Volumes` and returns `""`.

- [x] **Step 3: Implement**

Replace the tail of `liveHomeVolume` (`internal/sandbox/vm/run_orchestrate.go`):

```go
	liveMounts, err := handle.ConfigMounts()
	if err != nil {
		ui.Verbosef("reading VM mounts for home-volume recovery failed: %v (continuing)", err)
		return ""
	}
	return liveMounts[mounts.VMHomeDir].Named
```

Keep the surrounding handle lookup and `cfg == nil` guards; drop the now-unused `cfg, err := handle.Config()` read and
the `cfg == nil` branch if nothing else needs the config. Update the function comment: the mount comes from the
persisted config's `Named` mount at `/home/dev`, not `cfg.Volumes` (`specs/live-mount-readback.md`).

- [x] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/vm/ -run 'TestLiveHomeVolume|TestPrepareHomeVolumeRecoversLostState' -v`
Expected: PASS. `TestLiveHomeVolumeNoVM` and the "config cannot be read" case must still pass unchanged.

- [x] **Step 5: Commit**

```bash
git add internal/sandbox/vm/run_orchestrate.go internal/sandbox/vm/run_orchestrate_test.go
git commit -m "fix(vm): read the live home volume from the persisted mounts"
```

---

### Task 5: Report mounts in `sandbox inspect`

**Files:**
- Modify: `internal/sandbox/vm/inspect.go` (`InspectSandbox`, ~lines 35-70)
- Test: `internal/sandbox/vm/inspect_test.go`, `cmd/agents-sandbox/cli_inspect_test.go` (two sandbox-inspect cases)

**Interfaces:**
- Consumes: `SandboxHandle.ConfigMounts()` (Task 2).
- Produces: nothing new; `SandboxDetail.Mounts` is now populated for a live VM.

- [x] **Step 1: Update the tests to seed mounts through the seam**

- `internal/sandbox/vm/inspect_test.go`: move the two-mount literal from `cfg.Volumes` to `MockSandboxHandle.Mounts`
  (keep the `Cfg` fields the assertions read: image, memory/cpu limits, labels).
- `cmd/agents-sandbox/cli_inspect_test.go`: same move for the two `sandbox inspect` mocks (~lines 296 and 344). The
  expected human output (`mounts (2):` …) and JSON assertions stay as they are — they are the regression proof.

- [x] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/vm/ -run TestInspectSandbox -v && go test ./cmd/agents-sandbox/ -run TestSandboxInspect -v`
Expected: FAIL — mounts are empty, so the `len(got.Mounts) != 2` / `mounts (2):` assertions fail.

- [x] **Step 3: Implement**

In `InspectSandbox`, replace the `mounts = mountDetails(cfg.Volumes)` read (inside the `if cfg != nil` branch) with a
seam read placed after the config read, and surface its error like the config read does:

```go
	liveMounts, err := handle.ConfigMounts()
	if err != nil {
		return SandboxDetail{}, fmt.Errorf("inspect sandbox %q: read mounts: %w", name, err)
	}
	mounts := mountDetails(liveMounts)
```

`mountDetails` keeps its signature; delete the now-redundant `var mounts []MountDetail` declaration so the variable is
assigned once.

- [x] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/vm/ -run TestInspectSandbox -v && go test ./cmd/agents-sandbox/ -run TestSandboxInspect -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/sandbox/vm/inspect.go internal/sandbox/vm/inspect_test.go cmd/agents-sandbox/cli_inspect_test.go
git commit -m "fix(vm): report live mounts in sandbox inspect"
```

---

### Task 6: CHANGELOG and docs

**Files:**
- Modify: `CHANGELOG.md` (`[Unreleased]` → Fixed)

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

- [x] **Step 1: Add the changelog line**

Under `## [Unreleased]`, in the `### Fixed` list, add a line naming the user-visible effects and the issue:

```markdown
- Detect `tmp_size`, `workspace-quota`, and home-volume changes on an existing project VM again: the live VM's mounts
  are read from the persisted config instead of an always-empty `cfg.Volumes`, so the change triggers the documented
  VM recreation. This also restores home-volume recovery after lost state and makes `sandbox inspect` list mounts
  ([#223](https://github.com/inoio/agents-sandbox/issues/223)).
```

- [x] **Step 2: Check the existing docs still read true**

`docs/commands.md` and `docs/configuration/launcher.md` already state that a `--workspace-quota` (and `tmp-size`)
change triggers recreation, and `docs/` documents `sandbox inspect` output. These are correct only after this change —
re-read them and change nothing unless a statement is now inaccurate.

- [x] **Step 3: Run the full check**

Run: `make check`
Expected: fmt, lint, and test targets pass. Then run `make compile-all` and expect compile-only output
(`[no tests to run]` per package).

- [x] **Step 4: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): record the live-mount read-back fix"
```

---

## Notes for the executor

- The brain specs are already updated by this plan's author: `.operator-shared/specs/live-mount-readback.md` (new),
  `.operator-shared/specs/reconfig-decision.md` (gap section → resolution), and
  `.operator-shared/specs/home-volume-recovery.md` (mount source). Do not restate them in code comments beyond the
  one-line pointer to `specs/live-mount-readback.md`.
- Upstream: `SandboxConfig.UnmarshalJSON` dropping `mounts` is a Go SDK defect worth an issue at
  `superradcompany/microsandbox` (reference `sdk/go@v0.7.8` `options.go` vs `microsandbox-types` `domain.rs`,
  `SandboxSpec.mounts`). Not a blocker for this plan and not part of it.
