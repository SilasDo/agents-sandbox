---
description: Contract for the prefix-free artifact naming strategy: sandboxes, home volumes and microsandbox image references drop the `agents-sandbox-`/`agents-sandbox/runner-` prefix while Docker images keep it, new artifacts follow the new scheme and existing ones stay discoverable (write-new, read-both), and artifact discovery relies on the private runtime home plus labels rather than a name prefix.
read_if: Changing `internal/sandbox/naming`, the VM/home-volume/runner-image name or reference constructors or parsers, artifact discovery (list/remove/prune/reference scans), the sandbox or volume labels, the Docker-vs-msb image reference split (`ImageInfo`), or diagnosing a legacy artifact that stopped being found, reimported, or rebuilt after the naming change.
---

# Prefix-free artifact naming

## Intent

Names and references of the artifacts agents-sandbox creates in the microsandbox runtime must **not** carry the
vendor prefix:

| Artifact | Before | After |
|---|---|---|
| project VM | `agents-sandbox-vm-<slug>-<agent>` | `<slug>-<agent>` |
| task sandbox | `agents-sandbox-task-<slug>-<agent>-<nanos>` | `<slug>-<agent>-<nanos>` |
| home volume | `agents-sandbox-home-<slug>-<agent>-<ts>` | `<slug>-<agent>-<ts>` |
| microsandbox runner image ref | `agents-sandbox/runner-<slug>:<agent>-latest` | `<slug>:<agent>-latest` |
| Docker runner image | `agents-sandbox/runner-<slug>:<agent>-latest` | **unchanged** |

Docker keeps the prefix so our runner images are distinguishable from user images in the system Docker daemon. This
is a naming change inside a runtime we own; it needs no runtime replacement, and it is not a user-facing rename (the
names are internal keys, not CLI dialect).

`<slug>` is `<folder≤20>-<14-char base36>` (`internal/git`), lowercase and alphanumeric-starting, so every target
name still satisfies the runtime's name rules (sandbox ≤128 bytes / alnum-start / `[A-Za-z0-9._-]`; volume same
charset) and the derived guest hostname (≤64 bytes). The shorter names are a side benefit: `<slug>-<agent>` is ~47
bytes, so msb no longer truncates and hash-suffixes the guest hostname, and `projectVMName`'s 128-byte truncation
stops biting.

## The Docker/msb reference split

`image.ImageInfo.Tag` today is simultaneously the Docker tag and the microsandbox reference. The two must split:

- `ImageInfo.DockerTag` — `agents-sandbox/runner-<slug>:<agent>-latest`; used by `buildDecision`, `buildFromPlan`,
  `docker.ImageInspect`, `docker.ImageSave`, `readImageInfoFromDocker`.
- `ImageInfo.Ref` — the resolved microsandbox reference (new or legacy, see below); used by `ImageGet`,
  `ImageInspect`, `ImageLoad`, `ImageRemove`, and `WithImage`.

The Docker tag is exactly `agents-sandbox/runner-` + the new msb ref, so the split is mechanical. Because the Docker
tag is unchanged, this never triggers a rebuild — at most a one-time `ImageSave`/`ImageLoad` into msb under the new
ref.

## Write-new, read-both

New artifacts are created under the new scheme; lookups accept either scheme, so existing artifacts keep working and
are neither renamed, reimported nor rebuilt. `naming` owns both constructors per family (new and `Legacy…`) and its
parsers accept both shapes.

- **Home volume — recorded, therefore free.** The name is persisted in `HomeState.HomeVolume`, so an existing project
  resolves its volume by the recorded name and never rotates it. Only the list/parse paths must accept the legacy
  shape so the volume still appears and is still classified. A legacy-named volume stays legacy until the project
  next rotates its volume.
- **msb image — resolve by content.** The ref is derived from `(slug, agent)`, so resolution is: if the legacy ref
  exists **and** `cachedImageMatches` (its recorded digest equals the Docker image), use it — no reimport; otherwise
  use the new ref and `EnsureLoaded` loads there. The digest check is what makes "not reimported" exact: the legacy
  ref is reused only when it already holds the current content. Changed content reimports under the new ref, silently
  migrating that project.
- **Sandbox — lookup fallback.** The name is derived, not stored: try `GetSandbox(newName)`, then
  `GetSandbox(legacyName)`, and create under the new name only when neither exists. A reused legacy VM keeps its
  legacy name until it is next recreated (reconfig, or a fresh session after it is gone), which is not a rebuild
  caused by the rename.

`state.ImageReferences` and `vm/adopt.go`, which recompute the legacy runner-image ref for ownership/adoption, must
consider both refs.

## Ownership vs classification

Ownership **is not** a per-object property anymore. The private runtime home (`configpaths.MSBHome()`) is ours alone
and the external-runtime escape hatch was removed (`runtime-home-decoupling.md`), so every object in it was written
by us. The prefix-based "is this ours?" filters in `vm/list.go`, `volume/list.go`, `image/list.go` and
`pruning/images.go` are dropped, and `image/remove.go`'s `refuseNonRunnerRefs` becomes a "does this ref resolve to a
cached image" check rather than a prefix check.

Classification **is** still needed, because sandboxes have two kinds:

- A project VM and a transient task sandbox must be distinguished for pruning (project VMs are kept by age, task
  sandboxes are always reclaimable) and for listing (task sandboxes must not appear in the sandbox list). The signal
  is a label: project VMs already carry `org.agents-sandbox.project`/`.image`; task sandboxes get an
  `org.agents-sandbox.task` label at their creation sites. Handle-level labels are read via `Config().Labels`.
- Volumes have one kind (home volumes); images have one kind (runner images). Neither needs a marker.

Two name-shape checks survive for a different reason: `runtimemigration/legacy.go` scans the **legacy
`~/.microsandbox`**, which a user's own msb shared, so `IsHomeVolume`/`IsLauncherSandbox` are the only way to avoid
copying foreign data there. Those predicates stay (and accept both shapes).

## Non-goals

- No Docker image rename; the Docker prefix stays.
- No rename/rewrite of existing artifacts — legacy objects are read, never migrated in place.
- No change to the slug (`internal/git`) or to the state schema/directory keys.
- No single "which scheme is live" config switch; both schemes coexist indefinitely for reading.

## Acceptance

- New runs create `<slug>-<agent>`, `<slug>-<agent>-<ts>` and `<slug>:<agent>-latest`; the Docker tag keeps
  `agents-sandbox/runner-`.
- A project with a legacy home volume, legacy msb image ref and legacy project VM: the volume is still listed and
  resolved, the image is reused (no reimport) while its content matches, and the VM is reused (no rebuild); once the
  VM is recreated it comes back under the new name.
- `volume list` / `sandbox list` / `image list` show both schemes; task sandboxes never appear in `sandbox list`.
- `pruning` reclaims legacy-named task sandboxes and surplus refs, and keeps a live project VM's legacy ref.
- `make check` green; `CHANGELOG.md` Unreleased line; `docs/how-it-works.md`, `docs/sandboxes.md` and
  `docs/runner-image.md` updated for the new names/references.
