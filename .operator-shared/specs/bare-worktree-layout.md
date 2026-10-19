---
description: Contract for making git worktrees a contained, host-and-VM-consistent thing (issue #145): a bare checkout whose worktrees live inside it, bound as `/workspace`, plus the minimal mount rule that makes worktrees the source of truth. Covers the pattern priority order (non-bare checkout default, non-repo folder, bare-with-worktrees esoteric), the generalized bind rule (bind the smallest enclosing checkout that contains the CWD, so a worktree session always exposes its worktree), guest-CWD derivation for subdirectories, the decision to leave outside-checkout worktrees on current behavior with a termio warning, unchanged VM/state/home-volume/image identity (no scoping, no migration), the deliberately convention-only folder taxonomy (recipe + skill + AGENTS.md), and non-goals.
read_if: Implementing or reviewing issue #145 (bare checkout with worktrees inside), the `/workspace` bind-source resolution, guest-CWD derivation, or the worktree-layout recipe/skill.
---

# Contained worktrees across host and VM (issue #145)

## Problem Statement

Today worktrees are a **creature of the VM**. `--worktree <name>` asks the in-VM opencode daemon to create a worktree
under `<opencode-data>/opencode/<name>` (the opencode data dir), which lives on the **home volume**, not on the
`/workspace` bind mount. The host repository never sees them.

Two consequences motivate this work:

1. **Host and VM worktrees diverge.** A host checkout of the same repository has its own worktrees (e.g. the ones
   opencode creates on the host), and neither side can see the other. Agents have pruned worktrees that were still live
   on the other side; a worktree session cannot be inspected, continued, or cleaned up from the host.
2. **Uncommitted work is invisible and can be lost.** Because worktrees sit on the home volume, wiping/recreating the
   home volume destroys them. There is no easy host-side way to see whether a worktree still has uncommitted changes
   before deleting a home volume.

The root cause is that the worktree's *source of truth* is off the bind mount. The fix is to make worktrees live where
host and VM already agree — inside the project directory that is bind-mounted at `/workspace`.

## Goals / Non-Goals

**Goals**

- Worktrees for a project are **contained in one place** that both the host and the VM see identically (the bind mount).
- A session started in a worktree that lives **inside its checkout** (the bare layout, or a non-bare worktree nested
  under the main checkout) **always exposes that worktree** under `/workspace`, and connects to the project's shared VM.
- Dropping or recreating the home volume no longer destroys worktree state, and uncommitted work is inspectable from the
  host with ordinary git.
- **Support** a bare checkout whose worktrees live inside it, while keeping the **plain non-bare checkout the default**
  and primary pattern.

**Non-goals**

- Replacing `--worktree`. It is **kept as-is** for now; this change is an experiment in the underlying layout.
- Any `asb`/`agents-sandbox` `worktree` subcommand (create/list/remove/prune). Worktrees are managed with plain `git`
  commands; `agents-sandbox` only changes *what it binds*.
- Factoring in opencode's own daemon worktrees. They land under the opencode data dir (home volume) and are explicitly
  out of scope. Verified: `POST /experimental/worktree` creates
  `~/.local/share/opencode/worktree/<projectID>/<name>`.
- Making the bare layout the recommended/default way to use the tool.

**Anti-goals** (would make this a failure even if it "works")

- Requiring users to restructure an ordinary checkout, or silently changing behavior for plain non-repo folders and
  non-bare checkouts.
- Breaking existing VM names, home volumes, or state (no migration).
- Hiding the worktree a session is in (the bug this fixes) in any case.

## Pattern Priority Order

A project directory can be one of three shapes. Support order = priority:

1. **Plain non-bare checkout** — the default and primary pattern. Behavior must be unchanged except for the subdirectory
   rule below.
2. **Non-repo folder** — no git; bind the folder as today. Unchanged.
3. **Bare checkout with worktrees inside** — esoteric, supported.

## The Mount Rule (the only code change)

The single behavioral change in the product is *which host directory is bound at `/workspace`* and *where inside it the
agent starts*. Everything else (folder names, git config, how agents behave) is convention, not code (see below).

### Bind the smallest enclosing checkout that contains the CWD

Let:

- `cwd` = the current working directory, symlinks resolved (existing `resolveWorkspaceDir`).
- `gitCommonDir` = the resolved common git directory of `cwd`'s repository (`git rev-parse --git-common-dir`), or none
  if `cwd` is not in a repository.
- `checkoutRoot` = the directory that contains the git dir and any nested worktrees: the parent of `gitCommonDir` when
  `gitCommonDir` is named `.git`, otherwise `gitCommonDir` itself. This uniformly yields the main checkout root for a
  non-bare repo and the bare root for the `<root>/.git` bare layout.
- `worktreeRoot` = the top level of the working tree containing `cwd` (`git rev-parse --show-toplevel`), empty for a
  bare repository or a non-repo folder.

Then:

| Situation | `bindRoot` (bound at `/workspace`) |
|---|---|
| Not in a repository (non-repo folder) | `cwd` (unchanged) |
| In the main checkout, at its root or a subdirectory | `checkoutRoot` (unchanged at root; **root instead of CWD** in a subdirectory) |
| In a worktree **nested inside** the checkout (`worktreeRoot` within `checkoutRoot`) | `checkoutRoot` (so all sibling worktrees stay visible) |
| In a worktree **outside** the checkout (`worktreeRoot` not within `checkoutRoot`) | `cwd` (unchanged) **+ a `termio` warning** (unsupported; see below) |
| At the bare root itself (no working tree) | `cwd` (== `checkoutRoot`) |

For the supported cases the unifying invariant is: **`/workspace` must always expose the worktree the session is in.**
For the bare layout the bare root contains every worktree, so all sessions share one root; for a nested non-bare worktree
the main checkout contains it.

**Outside worktrees are out of scope and warned.** When the resolved worktree sits outside its checkout, binding only
the worktree leaves its `.git` pointer aimed at a shared git dir that is not mounted, so `git` cannot resolve the common
dir inside the VM. Rather than handle this case (which would require mounting the common git dir), the launcher keeps
today's binding and emits a **`termio` warning** that a worktree outside its checkout is unsupported and that git may not
work in the VM. This is not a regression: today's launcher already binds the CWD (the worktree) in this case.

The `git rev-parse` phrasing above describes the *facts*, not the implementation. The derivation reuses the existing
host-side, go-git-based repository inspection (`projectSlugAt` / `resolveGitDir` in `internal/git`); a host `git`
binary is not a runtime prerequisite and must not be introduced.

The bind *source* is a VM-creation-time property and is not part of the current reconfig change detection (only the
`/workspace` quota is compared). This change therefore takes effect on **freshly created VMs**; an already-existing VM
keeps its original bind source until it is recreated or removed. Adding bind-source-change detection to reconfig is
deliberately **not** done here (decision: scope the change to fresh VMs).

### Guest working directory

The agent must start **in the subdirectory it was launched from**, expressed relative to the bound root:

```
guestCWD = "/workspace" + <cwd relative to bindRoot>
```

At the root, `guestCWD == "/workspace"` (identical to today). In a subdirectory, the full repository remains visible
above the agent while it works in the subdirectory. For an outside worktree (binding unchanged), `bindRoot == cwd`, so
`guestCWD == "/workspace"` as today.

How `guestCWD` reaches the agent (see Background):

- **opencode / opencode2** (`run`): pass `guestCWD` as the attach target → `opencode attach … --dir <guestCWD>` /
  `opencode2 --server … <guestCWD>` (v2 takes the directory positionally; it has no `--dir`). No server change.
- **`shell`, `pi`, `claude-code`**: attach with the SDK's `WithAttachCwd(guestCWD)` (through the msb seam's
  `AttachWith`), which sets the PTY working directory for clients that ignore the attach target.
- **`--worktree`** keeps overriding the target with the daemon-created directory (unchanged).

## VM / State Identity and the Naming Scheme (unchanged)

VMs, home volumes, and persisted state are all keyed by `state.Key{Slug, Agent}`, where the slug is
`<folder>-<14-char hash>` derived from the origin URL (`internal/git`, `internal/sandbox/state`,
`internal/sandbox/volume`, `internal/sandbox/naming`).

**This change does not alter identity.** Every worktree of a project already resolves to the same origin-derived slug,
so all worktrees — the bare layout's subdirectories, a nested non-bare worktree, the main checkout — continue to share
**one VM, one home volume, one state dir, one runner image**. That is exactly the "one origin, one project VM" model the
feature relies on. Consequences:

- VM/volume/state/image names are **byte-identical** to today. No slug scoping, no parser changes, no migration.
- An outside worktree is not given a separate VM: it keeps today's behavior (it shares the origin VM and binds its CWD),
  and the launcher warns (see the mount rule).

Because identity is unchanged, `git.ProjectSlug()` needs no `bindRoot` parameter. Its existing multiple call sites
(`PrepareSandbox`, `session.Run`/`Shell`) keep deriving the same origin slug, so no VM-name/lease-key divergence is
introduced. The only new host-side derivation is `bindRoot`/`guestCWD`, which is separate from the slug.

## Conventions (deliberately not code)

The folder taxonomy is **a contract that inhabiting agents and humans follow**, not something `agents-sandbox` bakes
into code. It is delivered as (1) a user-facing recipe page under `docs/` and (2) a **1:1 copy** as a skill under
`.agents/skills/` (the skill is the machine-readable half of the same contract). The recipe and the skill keep
identical bodies.

### Layout

Inside the bound bare root:

- `.git/` — the bare git dir. Never a worktree.
- `main/` — canonical checkout of the default branch, always main; the reference point and the usual `--worktree` base.
- `IDE/` — the human's worktree; the one place branch-switching is expected (uppercase so it stands out).
- one flat directory per branch, **name derived from the branch** with `/` flattened to `--`:
  `feat/foo-does-bar` → `feat--foo-does-bar`. Agent and occasional human worktrees share this namespace.

### Required git configuration

- `worktree.useRelativePaths true` (git ≥ 2.48) so each worktree's `.git` pointer file survives the host→VM path
  remap. Set it in the bare repo's config.

### Rules and reserved names

- `main`, `IDE`, `.git` are reserved; a branch that would map to a reserved name is rejected/renamed by convention.
- Flattening is **lossy**: branches `feat/foo` and a literal `feat--foo` collide to the same directory. The convention
  documents this; users avoid literal `--` branch names.

## Background: the opencode server directory model

Verified against `opencode 1.19.0` (throwaway server + `sst/opencode` source); this is why no server relocation is
needed.

- `opencode serve` (started by the launcher with no directory argument, inheriting the exec CWD `/workspace`) has **no
  server-side directory flag**. Its startup CWD is only a **fallback**.
- The server resolves a directory **per request**:
  `directory = query("directory") || header("x-opencode-directory") || process.cwd()`, then scopes the instance to it.
- The **client** sets that directory: `attach --dir X` (v1) / `opencode2 … X` (v2, positional) does a local `process.chdir(X)`
  (passing `X` through when it does not exist locally) and sends it to the server. So the attach target is the mechanism; the
  session's working directory is client-controlled, not server-predetermined.
- `Instance.provide(directory)` resolves the git **project** by the git common dir independently of the session
  directory: a nested directory resolves the same project as the repo root, and a linked worktree resolves the same
  projectID as the main repo (worktree path preserved). This is exactly what the bare-with-worktrees layout needs.
- opencode2 uses the same directory concept via a positional directory + `--server`; the shared limitation is a local
  `chdir`, which is fine here because the directory exists inside the VM.

## `--worktree` (kept)

`--worktree` is not removed. It continues to create/reuse a daemon-side worktree under the opencode data dir and to
override the attach target. This spec changes only the bind source and default guest CWD; the `--worktree` interaction
is out of scope beyond not regressing it.

## Acceptance / Verification

- Non-repo folder and non-bare checkout at the root: **byte-identical** behavior and names to today.
- Non-bare checkout launched from a subdirectory: `/workspace` is the checkout root; the agent starts in the
  subdirectory; host/VM see the same files.
- Nested non-bare worktree and bare-with-worktrees: `/workspace` is the enclosing root; all sibling worktrees are
  visible; the session reuses the project's shared VM; the agent starts in the launched worktree.
- Outside non-bare worktree: binding is **unchanged** (the worktree/CWD) and a `termio` warning is emitted; the session
  still shares the project VM.
- Naming: VM/volume/state/image names are **unchanged** for every case (no migration); the name parsers are untouched.
- The recipe page and the skill are present and identical in content; docs link check passes.

## Open Decisions (for the plan)

- Exact plumbing of `guestCWD` through `options`/`Session`/`runAttach` and whether the non-opencode agents take
  `WithAttachCwd` unconditionally or only when a subdirectory is in play.
- Whether the bare layout should additionally be recognized when the bare git dir is *not* named `.git` (pure bare
  root), or only for the documented `<root>/.git` layout.
- Whether `.agents/skills/` (untracked, local) or the tracked tree should hold the skill copy for distribution.
- The exact wording of the outside-worktree `termio` warning.

Decided (see the mount rule): outside worktrees keep current behavior and are warned rather than given their own VM or
extra mounts; the change applies to fresh VMs (no reconfig bind-source detection); identity/naming is unchanged.
