---
description: Contract for splitting project identity in two (issue #144): a repo-scoped slug (origin-derived) driving the per-slug user config dir and the sandbox grouping label, and a checkout-scoped slug (resolved git common dir) driving state, VM name, home volume, locks/claims, and the runner image tag. Two clones of one remote get separate VMs/volumes; a checkout and its worktrees share one. Includes the automatic, noticed migration that adopts the legacy origin-keyed identity (destroy the legacy VM, then move the state dir), the prune home-volume reference guard, the side-effect-free dry-run, and the outside-worktree fail-fast.
read_if: Implementing or reviewing issue #144 (two checkouts sharing one VM), the repo-vs-checkout slug derivation, the runner-image tag identity, the `config paths` command, the checkout-identity migration and its notice, the prune reference guard, or the outside-worktree fail-fast.
---

# Per-checkout project identity (issue #144)

Status: design approved; not implemented.
Date: 2026-10-19.

## Problem

`git.ProjectSlug()` hashes the **origin URL**, so every clone of one remote collapses to the same
`<repo>-<hash>` slug. That single slug keys the state dir, the VM name, the home volume, locks/claims,
the runner image tag, and the per-slug user config. Two separate checkouts therefore resolve to one
`state.Key{Slug, Agent}`: the second `agents-sandbox` finds the first checkout's VM, attaches to it,
and inherits its `/workspace` bind (the mount is fixed at VM creation). Files written in the second
checkout land in the first.

The origin-derived slug is also the *only* slug, so the config namespace, which should be shared by
all clones of a remote, is inseparable from the stateful artifacts, which should not be.

`bare-worktree-layout.md` (#145) deliberately kept "one origin, one VM" for worktrees. That premise
is wrong for independent clones and is corrected here; the worktree behavior it introduced (bind the
smallest enclosing checkout, guest CWD derivation) is preserved.

## Decision: two slugs, one derivation each

Project identity splits into two `<folder>-<14-char base36 hash>` slugs, each governing the artifacts
whose correct unit it matches.

- **repo slug** — derived from the origin remote URL. Exactly today's `ProjectSlug()` value.
- **checkout slug** — derived from the resolved git **common dir** (the shared `.git` of a checkout and
  all its worktrees). New value.

The name parsers (`naming.ParseVMName`, `ParseHomeVolumeName`, `ParseImageTag`) are untouched: both
slugs keep the `<folder>-<hash>` shape they already parse.

## Derivation

- **repo slug** = `sanitize(origin repo last path segment) + "-" + HashID(origin URL)`; falls back to
  the checkout folder/basename when there is no origin, and to the CWD path when not in a repo. This is
  the current `projectSlugAt` behavior, unchanged.
- **checkout slug** = `sanitize(origin repo last path segment, or checkout basename when no origin) +
  "-" + HashID(resolved git common dir path)`. The folder name still comes from the origin so two
  clones read `foo-<hashA>`, `foo-<hashB>`. The hash input is the symlink-resolved common dir, so all
  worktrees of one checkout (nested, or the bare `<root>/.git` layout) share a checkout slug, and two
  clones differ. The derivation resolves symlinks **internally**, so every entry point (run, stop, kill,
  volume ops, build) hashes the same path — otherwise `/tmp` vs `/private/tmp` on macOS would make `run`
  and `stop` compute different checkout slugs.
- **non-repo folder**: both slugs are `<folder>-<hash(cwd)>` (today's fallback); behavior unchanged.
- **subdirectory of a checkout**: the checkout slug is the checkout root's, matching the `/workspace`
  bind root from #145.
- **outside worktree**: fails fast (below) before any slug-sensitive work.

`ResolveWorkspaceLayout` already computes `CheckoutRoot` from `resolveGitDir`; the checkout slug reuses
that derivation (go-git / plain-file inspection, no host `git` binary).

## Which artifact uses which slug

| Artifact | Identity |
|---|---|
| Per-slug user config dir `$XDG_CONFIG_HOME/agents-sandbox/<slug>/` | **repo** |
| Sandbox grouping label `org.agents-sandbox.project` | **repo** |
| State dir `$XDG_STATE_HOME/agents-sandbox/<slug>/…` (state, `claims/`, `<agent>/clients/`) | **checkout** |
| VM name `agents-sandbox-vm-<slug>-<agent>` | **checkout** |
| Home volume `agents-sandbox-home-<slug>-<agent>-<ts>` | **checkout** |
| Locks under `<state>/locks/` keyed by project | **checkout** |
| Runner image `agents-sandbox/runner-<slug>:<agent>-latest` | **checkout** |
| Notify dedup claims (via `state.AcquireClaim`) | **checkout** |

The `org.agents-sandbox.project` label keeps the **repo** slug so `list --label project=<repo>` groups
every clone's sandbox; the sandbox **name** carries the checkout slug and is the exact identifier.
Nothing keys ownership off this label (prune parses names), so the change is safe.

## Runner image

The runner image is built from checkout-local inputs (the project Dockerfile and project config in
that checkout), so a checkout-scoped tag is the correct identity: two clones on different commits can
hold different images without fighting over one tag.

"Reuse across clones" becomes best-effort, not a shared tag: two clones with identical inputs each
compute the same content identity, so the second build is a **cache hit** against the shared BuildKit
cache, and the microsandbox content store should hold one blob behind two tags (containerd's store is
content-addressed; verify msb keys by digest). This trades the guaranteed single build of a shared tag
for correctness, and it means an identical pair costs one extra cached build rather than zero.

Consequence for upgrades: the msb seam exposes no image tag/retag operation (`ImageLoad`,
`ImageRemove`, `ImageInspect`, `ImageList`, `ImagePrune`, `ImageGet` only), so a migrated project
cannot re-point its existing image at the new tag — it rebuilds. Cache-assisted, but it needs the
builder working and, on a cold cache or offline, network for the base/agent layers.

## Config

Config stays on the **repo slug**, so its path is byte-identical to today and is shared by every clone
of a remote. No config migration.

The only real gap is discoverability: nothing today prints the slug or the per-slug dir. Add
`agents-sandbox config paths` (plural) printing every config layer in precedence order, including the
concrete per-slug user dir and the resolved repo slug, and stating that repo-wide knobs belong in the
committed project config (`.agents-sandbox/config.yaml`) or the generic user config. A `config edit`
helper to create/open the per-slug `config.yaml` is optional. The ordered-multi-dir resolver machinery
from `opencode2-config-fork.md` is available but not required, since nothing renames.

## Outside worktrees: fail fast

Replace #145's warn-and-bind-CWD behavior for a worktree outside its checkout with a **fail-fast error**
before any VM/volume/image work, with a UX message naming the worktree and its checkout. This is
required for correctness under checkout identity: an outside worktree and its main clone share the
checkout slug, so they would resolve to one VM whose `/workspace` bind can only be one of the two —
reproducing the #144 collision. Refusing is deterministic.

The error is raised in the **shared slug resolution**, not in #145's `resolveBindLayout`, so every
cwd-derived identity path refuses — not only `run`/`shell`, but `stop`, `kill`, the `volume …` ops,
and `build`, which today call `git.ProjectSlug()` directly and would otherwise silently target the main
clone's VM from an outside worktree.

## Migration (automatic, with a notice)

**Trigger**: in a repo, a legacy origin-keyed **state file** exists at `<state>/<repoSlug>/<agent>/state.yaml`
and no checkout-keyed **state file** exists at `<state>/<checkoutSlug>/<agent>/state.yaml`. The predicate
is state-**file** absence, not directory absence, so a stray or aborted run that creates the empty
checkout directory cannot permanently skip migration. Re-evaluated on each run until it fires.

**Actions**

1. **Destroy the legacy VM first.** Derive `projectVMName({Slug: repoSlug, Agent})` and destroy it
   identity-safely if it exists (best-effort; a gone name is fine). This keeps the old VM from running
   against the shared home volume and from becoming a `ToPrune[<repoSlug>]` entry (see Prune safety).
2. **Then move** `<state>/<repoSlug>/` to `<state>/<checkoutSlug>/` (project state, `claims/`,
   `<agent>/clients/`).
3. Home volume: unchanged. Its name is stored in the state's `home_volume`, so the preserved volume is
   re-resolved by name with no rename.
4. VM: recreated once under the new name over the preserved home volume.
5. Runner image: rebuilt under the new checkout tag (cache-assisted, see above).

Destroy-first is crash-safe: a crash between the destroy and the move leaves the legacy state in place,
so the next run re-adopts and simply recreates the VM. Move-first would leave an untracked legacy VM
that prune could then reclaim alongside the volume.

**`--dry-run` is side-effect-free.** It performs no destroy and no move, resolves read-only against the
legacy location, and prints the migration notice as a "would" (with the destination paths). It does not
present post-migration artifacts as if the migration had run.

**Notice**: emitted once, and **explicit about everything that moved, with absolute paths** — the state
directory (old → new), the destroyed legacy VM name, the preserved home volume name, the old → new VM
name, and the old → new image tag. The move is the idempotence record: the legacy state file is gone
afterward, so the notice does not re-fire. When several clones exist, whichever runs first adopts the
legacy identity; the notice says so and the others start fresh.

## Prune safety

A migrated project keeps a home volume whose name embeds the **legacy** repo slug (`home_volume` rides
along), while the legacy slug is now stale. Because `pruneHomeVolumes` removes any volume whose
name-parsed key is in `ToPrune`, and the just-upgraded legacy VM can land in `ToPrune[<repoSlug>]`, a
`prune` run *before* the first post-upgrade `run` would otherwise delete the preserved volume and then
`state.RemoveHomeState(<repoSlug>)` the very state adoption needs — losing auth and state.

Fix: `pruneHomeVolumes` must **fail closed on any volume name referenced by an existing state file**
(via `state.HomeVolumeReferences()`, as `volume remove` already does), so prune never deletes a volume a
state file points at. Images already have their own removal guard. Prune cannot trigger adoption
(recovery needs the checkout context, which prune is cwd-agnostic about and which state files do not
record), so the reference guard is the whole fix. Reclaiming the legacy **VM** by prune is accepted:
its absence does not block adoption, which recreates it over the preserved volume.

## Backwards compatibility

- Per-slug user config: unchanged path, shared across clones, no migration.
- Home volume and its auth/data: preserved; only the VM is recreated.
- State: preserved by the directory move (including fingerprints and upgrade/provenance fields).
- Image: rebuilt once under the new tag (cache-assisted); cannot be re-tagged.
- Non-repo folders: unchanged.
- Nested/bare worktrees: unchanged sharing (one VM per checkout).
- Outside worktrees: newly refused (was: warned and bound CWD).

## Non-goals

- Changing the #145 mount rule or guest-CWD derivation beyond the outside-worktree refusal.
- An `asb worktree` command or any change to `--worktree` / daemon worktrees.
- A git-dir identity marker: identity is path-derived (see limitations).

## Known limitations

- **Moving a checkout changes checkout identity.** The common dir path is the hash input, so a moved
  or renamed checkout gets a new checkout slug, a fresh VM/volume/image, and does not find the state it
  adopted under the old path. (Today a move keeps the origin slug.) A git-dir identity marker would fix
  this and was deliberately deferred.
- **A first-post-upgrade run needs the builder.** The image tag changes, so a cold-cache or offline
  first run rebuilds.
- Two clones with divergent Dockerfiles each build their own image (by design).

## Open decisions (for the plan)

- The exact function split: keep `ProjectSlug()` for the checkout slug (least churn) and add
  `RepoSlug()`, or rename both explicitly. Every call site must be categorized (config resolver → repo;
  `PrepareSandbox`/session/`vm_control`/volume commands/image build → checkout).
- Whether the checkout slug and `ResolveWorkspaceLayout` unify into one git-inspection pass.
- Where the legacy-adoption step lives and how it is reached from every `state.Key` entry point
  (run/shell, `volume …`, `vm_control`, prune) without an import cycle.
- Whether adoption records a `state.Migration` registry entry or relies on the directory move alone.
- The `config paths` output shape and flags.
- The exact notice wording/format (a `termio` notice vs a block).
