---
description: Contract for reading a live project VM's mounts. The microsandbox Go SDK's `SandboxConfig.UnmarshalJSON` drops the persisted `mounts`, so `cfg.Volumes` is always empty on a read-back; the launcher instead decodes the persisted config JSON through `msb.SandboxHandle.ConfigMounts()`. Every consumer (the reconfig tmp-size/workspace-quota/home-volume detectors, home-volume recovery, and `sandbox inspect`) reads mounts from that source and degrades rather than failing when it is unavailable.
read_if: Reading or changing how the launcher learns a live VM's mounts, or diagnosing an empty `cfg.Volumes` / `mounts (0)` / silently ignored `tmp_size` or `workspace-quota` change (`internal/sandbox/reconfig/plan.go`, `internal/sandbox/vm/inspect.go`, `liveHomeVolume`).
---

# Live VM mounts come from the persisted config JSON

## The gap

`SandboxHandle.Config()` decodes the sandbox record's `config_json` column through the Go SDK's
`SandboxConfig.UnmarshalJSON` (`sdk/go@v0.7.8`, `options.go`), which populates a `persistedSandboxConfig` that has
**no mounts field**. `cfg.Volumes` is therefore nil for any config read back from a live VM. Fields that do round-trip
(`cfg.Image`, `cfg.RootDisk`, `cfg.Labels`, resources) are unaffected, which is why `rootDiskChange` works and the
`cfg.Volumes` readers do not.

The data is present upstream. `config_json` holds a serialized `SandboxConfig` whose `#[serde(flatten)]`ed
`SandboxSpec` carries `mounts: Vec<VolumeMount>` (`microsandbox-types`, `domain.rs`), serialized as an internally
tagged array — one entry per mount, keyed in the launcher by its `guest` path:

```json
"mounts": [
  {"type":"Named","name":"agents-sandbox-home-<slug>-<agent>-<ts>","guest":"/home/dev","options":{...}},
  {"type":"Bind","host":"<host repo>","guest":"/workspace","quota_mib":16384,"options":{...}},
  {"type":"Tmpfs","guest":"/tmp","size_mib":2048,"options":{...}}
]
```

This is exactly the shape `buildMounts` (`internal/sandbox/vm/vm_lifecycle.go`) creates, so the round-trip is
lossless for everything the launcher sets.

## Contract

- The single source of truth for a live VM's mounts is `msb.SandboxHandle.ConfigMounts()`, backed by
  `msb.ParseConfigMounts(handle.ConfigJSON())`. `cfg.Volumes` must not be treated as populated on a read-back; a
  consumer that needs mounts applies `ConfigMounts()` first (this is the one place the SDK gap is worked around).
- `ParseConfigMounts` keys by guest path and maps the five upstream variants onto the SDK's `MountConfig` through its
  `Mount` factories: `Bind` → `quota_mib` + `options.readonly`, `Tmpfs` → `size_mib`, `Named` → `name`, `Owned` and
  `DiskImage` for completeness (`DiskImage`'s `format` is lowercased, because upstream serializes `DiskImageFormat` as
  the bare variant name — `"Raw"` — while `DiskOptions.Format` is the lowercase hint). An entry whose `type` is
  unrecognized (a newer runtime) is skipped rather than failing the whole read; a `guest`-less entry, invalid JSON, or
  an empty `config_json` is an error.
- Consumers and what they read:
  - `reconfig.PlanReconfig` via `decideReconfig` — `tmpSizeChange` (`size_mib` at `/tmp`), `workspaceQuotaChange`
    (`quota_mib` at `/workspace`), `homeVolumeChange` (the `name` of the `Named` mount at `/home/dev`).
  - `vm.liveHomeVolume` — the same `Named` mount, for home-volume recovery (`specs/home-volume-recovery.md`, #219).
  - `vm.InspectSandbox` — the mount list rendered by `sandbox inspect`
    (`specs/runtime-management-commands.md`, Slice 3).
- `reconfig.PlanReconfig` via `decideReconfig` and `vm.liveHomeVolume` **degrade, never fail**: a lookup or parse
  failure is logged at verbose level and the run proceeds with no mounts, reproducing the previous behaviour instead
  of breaking a run. `vm.InspectSandbox` reports the failure like its `Config()` read does, so `sandbox inspect`
  never silently shows zero mounts for a VM that has them.
- A missing `mounts` key is not an error — a config persisted by a runtime that does not record mounts is valid, and
  must not read as "everything changed". It yields an empty (non-nil) map.

## Deliberate non-goal

The `MountState` fingerprint for the user's extra host bind mounts (`opts.Mounts`) stays. It covers a different set
(the user-configured binds, fingerprinted with source and readonly) than the managed mounts read here, and the two do
not overlap. Replacing it with a live comparison is possible now that mounts are readable, but is not part of this
change.

## Upstream

`SandboxConfig.UnmarshalJSON` dropping `mounts` is a Go SDK defect, not an intentional contract: `SandboxConfig`
already exposes `Volumes` and `WithMounts`, so the reverse direction (create) works while the read-back direction
silently loses the same data. Fixing it upstream would let `ConfigMounts` collapse into `Config()`. Until then the
launcher keeps its own decode.
