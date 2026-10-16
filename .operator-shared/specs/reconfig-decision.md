---
description: Contract for the per-startup project-VM reconfiguration decision: how changed settings are detected via persisted fingerprints, which changes recreate the VM vs restart the daemon, the fingerprint-preservation invariant, and the known state-write race.
read_if: Changing decideReconfig/PlanReconfig, the env/secret/network/mount fingerprints, the reconfig prompt, or diagnosing a spurious VM rebuild or "network policy changed" prompt.
---

# Project-VM reconfiguration decision

`PrepareSandbox` calls `decideReconfig` (`internal/sandbox/vm/reconfig.go`) once per startup to choose between recreating
the project VM, restarting the agent daemon, or doing nothing. The decision is computed by `reconfig.PlanReconfig` and
applied by `reconfig.ResolveReconfig` (silent when alone, a keep/quit[/restart] prompt when other clients are attached).

## Fingerprint-backed change detection

The microsandbox SDK does not round-trip env, secrets, network, or mounts when reading an existing VM back, so those
changes are detected against fingerprints persisted in the per-`(slug, agent)` `state.yaml` (`state.HomeState`):

- `EnvState`, `SecretState`, `MountState` — content hashes. Empty applied means "no persisted state yet" and counts as
  changed **only when the desired setting is non-empty**.
- `NetworkState` — a `network.Policy.Fingerprint()` hash. Empty applied counts as changed **unconditionally**, to force
  one-time migration of VMs created before the deny-by-default network change (#86). `BuildNetworkState` applies
  `Policy.Effective()`, so an unset profile fingerprints as `none`; `Fingerprint` sorts its lines, so list order does not
  matter.

Fingerprints are written by `persistConfigHashes`, which runs only when the VM was created this run (`boot ==
vmBootCreated`). A fresh home volume's state is additionally seeded with the desired fingerprints at creation
(`volume.Manager.EnsureNewHome`), so a `HomeState` is never fingerprint-less while a VM exists (see Resolution below).

## Invariant: preserve fingerprints on every state rewrite

Every path that rewrites a project's `HomeState` must carry the existing env/secret/network/mount fingerprints forward,
because an absent fingerprint is treated as "changed". `volume.operations.volumeOp`, `volume.Manager.ApplyHomeAction`,
and `volume.Manager.RecordHomeImage` all preserve them; `volume.Manager.EnsureNewHome` seeds them on a fresh state.
Dropping them makes the next run rebuild the VM and blame a setting (e.g. the network policy) that did not change.

## Determinism

Change detection is order-independent by design: `state.SecretsContentHash`, `state.EnvContentHash`, and
`network.Policy.Fingerprint` all sort their inputs before hashing, and `reprovision.AgentConfigEqual` compares the merged
and mirror files by map lookup. The helper outputs feeding them are also kept deterministic: `reprovision.ConfigFiles.Keys`
has a single ordering authority (the merged config leads when snippets exist, the remainder sorted; callers must not
re-sort) and `envsecret.BuildSecretsFromSpecs` emits entries in sorted `EnvVar` order. Map iteration order therefore never
reaches the decision — a periodic spurious rebuild was the state-write race resolved below, not input ordering.

## Resolution (issue #183)

The spurious change came from `EnsureNewHome` writing a fingerprint-less fresh `HomeState` outside the `ensure-vm.lock`
flock, while `persistConfigHashes` ran only after VM creation. A second session could read the fingerprint-less state
after the first session created the VM but before it persisted fingerprints, and then see a bogus "network policy" (and
env/secret/mount) change — a silent rebuild when alone or a rebuild/restart prompt when another client was attached.

The invariant now held is: **a `HomeState` is never fingerprint-less while a VM exists.** The desired fingerprints are
computed once (`vm.desiredFingerprints`) and seeded as part of the fresh-home write: `volume.Manager.EnsureNewHome`
applies `state.Fingerprints` to the new `HomeState` in one atomic write, and `persistConfigHashes` persists that same
value, so the seeded and persisted sets cannot diverge. Because state resolution runs before VM creation, the written
state already matches the desired config by the time any session can observe the VM, so the empty-applied "changed" rule
can never fire spuriously. This closes the concurrent window and the crash route (a process killed before
`persistConfigHashes` now finds a seeded state).

The deleted-state route is now handled by home-volume recovery (issue #219): when `state.yaml` is deleted while the VM
and its volume survive, `ResolveHomeVolume` adopts the volume the VM still mounts and seeds the recovered state with the
desired fingerprints, so no duplicate volume is minted, the old volume is not orphaned, and `homeVolumeChange` does not
fire. When a prior state is still readable but its recorded volume is gone, that state's fingerprints are carried
forward instead. See `specs/home-volume-recovery.md`.

The trade-off: the seed write briefly asserts "applied" before the VM exists. If creation then fails the state overstates
what is baked in, which is harmless because with no VM `PlanReconfig` short-circuits to no recreate. Recreated VMs (a
genuine config change against an existing home) still refresh fingerprints via `persistConfigHashes` after creation; the
state is never fingerprint-less on that path, so the change detected there is real.

## Known gap (issue #223)

`tmpSizeChange`, `workspaceQuotaChange`, and `homeVolumeChange` compare against `cfg.Volumes[...]`, but the SDK does not
round-trip volumes on read-back (same fact behind the fingerprint approach), so these three detectors never fire — a
changed `tmp_size`/workspace quota on an existing VM is silently ignored until an unrelated recreate. `rootDiskChange`
reads `cfg.RootDisk` and is unaffected. Fix is either a fingerprint (as for network/env) or deleting the dead detectors;
tracked in #223, out of scope for #221.
