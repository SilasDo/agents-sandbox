# Contract: opencode2 worktree API (v2 routes, project scoping, base refs)

Date: 2026-10-19
Status: implemented on fix/opencode2-worktree-api (pending merge)

## Problem

`--worktree <name>` with the `opencode2` (opencode v2) agent has never worked: the profile
issues v1 worktree routes that v2 does not serve.

| Launcher call (today) | Result on v2 |
|---|---|
| `GET /api/project/current` (project id) | **404** |
| `GET /api/worktree/$PID` (list) | **404** |
| `POST /api/worktree/$PID` (create) | **404** |

The credential added in #234 (`-u opencode:opencode`) fixed authentication only; the routes
still 404, so the session fails before attach.

Three v2 facts make this more than a route rename:

1. **The worktree routes are project-scoped.** Both need the project id, and the project is
   identified by the *directory* the request is scoped to. The profile cannot resolve it:
   `WorktreeListCmd()`/`WorktreeCreateCmd(spec)` are built without the target directory, and the
   event that would register a project (attach) happens after `ResolveTarget` runs.
2. **The v1 create body does not fit v2's schema.** `Worktree.CreateInput` is
   `additionalProperties: false`, has no `strategy`, and `from` is an `AbsolutePath` (a directory),
   not a ref.
3. **Base refs differ.** v1 applies the base with `startCommand: "git reset --hard <base>"`; v2's
   native field is `branch`.

Verified against the pinned `opencode v2.0.32` — the version the runner image bakes and
`latestOpenCode2Version` returns (`opencode2_release_test.go`) — by driving a throwaway `opencode2
serve` with real HTTP calls, per the "Verified v2.0.32 API" section.

## Verified v2.0.32 API

- `GET /api/location?location[directory]=<dir>` → `{directory, project:{id, directory, canonical}}`.
  Resolves (and registers) the project for a directory in one request. The project is the enclosing
  git repository: a subdirectory and a linked worktree both resolve to the same `id`; `canonical` is
  the repository root, while `directory` on the project is the requested worktree directory.
- `GET /api/project` → an array of every project the server has seen (`{id, canonical, vcs?, name?,
  time:{created, updated, active}, sandboxes}`). Project ids are stable at 40 hex chars.
- `GET /api/worktree?projectID=<id>` → `[{directory, strategy?}]`. The list contains **every git
  worktree of the project, including the canonical root entry** (which carries no `strategy`). There
  is no `/api/worktree/{id}` route.
- `POST /api/worktree` with `Worktree.CreateInput {projectID (required), name?, directory?, from?,
  branch?}` → `Worktree.Info {"directory": "<absolute path>"}`. Semantics:
  - `name` + `directory`: `directory` is the **parent**; the result is `<directory>/<name>`.
  - `name` only: `$HOME/.local/share/opencode/worktree/<first 6 chars of project id>/<name>`.
  - neither: the default parent plus a randomly generated name.
  - A name that already exists is **never reused**: the server appends `-2`, `-3`, … and creates a
    new worktree. Reuse therefore stays a client-side decision (list first).
  - `from`: an absolute path of an existing worktree directory to create from; defaults to the
    project.
  - `branch`: an existing ref to base the new worktree on. An unresolvable ref fails the create
    (`fatal: invalid reference: <ref>`). Created worktrees are **detached HEAD** — v2 never leaves a
    worktree on a branch.
  - Failures return `WorktreeError {message, forceRequired?}` or `ProjectNotFoundError`.
- `Worktree.Info` and `Worktree.Directory` both carry `directory`, so the shared
  `{"directory": ...}` parser (`parseWorktreeDirectory`) already handles v2 responses.
- `/api/worktree/refresh` (POST) and `DELETE /api/worktree` exist; unused.

## Contract

- **The `agent.WorktreeProvider` seam receives the target directory.** The launch directory
  (`vm.ResolveTarget`'s `defaultDir`, i.e. the guest CWD) is passed to
  `WorktreeListCmd(target string)` and set on `agent.WorktreeSpec.Target` for
  `WorktreeCreateCmd(spec)` (the field exists today but is never populated). v1 ignores it: its
  routes are global and the daemon resolves the project itself. No other provider capability
  changes shape.
- **v2 resolves the project through `/api/location`**, not by scanning `/api/project`: the command
  seeds `location[directory]=<target>` (shell-quoted), and reads `.project.id`. An empty id or a
  non-2xx response must fail the command rather than send a request with an empty `projectID`.
  `canonical` must likewise resolve to a non-empty value or the command aborts: with a missing
  `canonical` the list's canonical-root filter would compare entries against a `null` sentinel and
  fail open, keeping the main checkout in the list.
- **v2 lists with `GET /api/worktree?projectID=<id>`**, and the emitted list **excludes the
  canonical root entry**: `jq -c --arg canonical "$CANON" '[.[]|select(.directory != $canonical)]'`
  (wrapped in an array so the output stays one JSON list). Otherwise a worktree name equal
  to the repository directory name would silently "reuse" the main checkout and attach the session
  there instead of creating a worktree.
- **v2 creates with `POST /api/worktree`** and the body `{"projectID": …, "name": …}`, plus
  `"branch": <base>` when a base is given. Deliberately absent: `strategy`, `directory`, `from`,
  `startCommand`.
  - No `directory`: the launcher keeps v1's convention of letting the daemon choose the worktree
    location, which lands under the opencode data dir on the home volume
    (`$HOME/.local/share/opencode/worktree/<short project id>/<name>`). Choosing a location is not
    part of this fix.
  - No `from`: `--worktree` has no stacked-worktree concept, and `from` is a directory, not a ref.
- **Worktree reporting is user-visible.** On both create and reuse the resolved worktree directory is
  reported at info level (default-visible). When the daemon auto-suffixes a name — the created
  directory's base name differing from the requested slug — a warning names the directory actually
  used. An agent whose daemon creates worktrees detached (opencode2) appends a detached-HEAD note via
  the optional `DetachedWorktreeReporter` capability, naming the `git switch -c <branch>` recovery;
  v1, which keeps a branch, adds no note.
- **Base handling: the base is validated *before* the create, against the target directory, for both
  providers** (`git rev-parse --verify <base>^{commit}`, no fetch — the existing
  `validateWorktreeBase`, just moved ahead of the create and pointed at the source directory). The
  post-create validation is dropped. This is what produces v2's only realistic create failure (an
  unresolvable ref) as the existing friendly error instead of a bare `curl` exit code. On reuse the
  base is still ignored with the existing warning.
  - v1 keeps `startCommand: "git reset --hard <base>"` in its create body; v2 uses `branch`.
- **All v2 calls keep the baked credential** (`-u opencode:opencode`, `opencode2CurlAuth`).
- **Reuse matching is unchanged**: the shared base-name match over the list, which is exact because
  `--worktree` names are slugs (no separators; see below).

## Deviations from v1 (must be reflected in docs, not hidden)

- **Checkout state.** v1 leaves the worktree on its own branch with the base reset into it; v2 leaves
  a **detached HEAD** at the base ref (or at the source tip without a base). `docs/branch-sessions.md`
  currently states the v1 behavior unconditionally ("The worktree stays on its own `opencode/<name>`
  branch") and must be qualified.
- **Name validation is already stricter than the issue's repro suggests.** `ResolveWorktreeSpec`
  rejects anything that is not a slug, so `--worktree feature/x` never reaches the daemon; the real
  repro command is `--worktree feature-x`.

## Non-goals

- No worktree location control: the launcher sends no `directory`, so worktrees stay on the home
  volume. Nesting them inside the checkout remains `bare-worktree-layout.md`'s convention-only story,
  and `--worktree` there stays "kept as-is".
- No `from`/stacked worktrees, no `--worktree` CLI surface change (no new flag, no `:base` change).
- No daemon-side worktree removal or `refresh` wiring, and no worktree listing command in the CLI.
- No caching of project ids or worktree lists: resolution happens per run, only when `--worktree` is
  set (`ResolveTarget` still returns early for an empty name).
- No change to v1's routes, bodies, or command shapes beyond the new target parameter it ignores.

## Verification

- Command-string unit tests for `opencode2Profile` (resolution prelude present, list filtered by
  `.project.canonical`, create body contains `projectID`/`name`/`branch` and no `strategy`), and for
  `opencodeProfile` (routes and body unchanged; target ignored).
- `vm.ResolveTarget` tests: the target reaches both provider calls, the base is validated before the
  create, and an unresolvable base aborts without creating.
- Manual end-to-end (user-side; the dev VM cannot start microsandbox VMs):
  `agents-sandbox run --agent opencode2 --worktree feature-x` creates, attaches, and a second run
  reuses the same directory; `-w feature-x:main` checks out at `main` (detached).
