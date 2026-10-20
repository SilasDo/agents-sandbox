---
description: Contract for the `msb`-tagged harness smoke tests that start every registered harness to its TUI in a real microsandbox VM, and the isolation that keeps them off the developer's existing agents-sandbox state.
read_if: Adding or changing the harness smoke tests, the `msb` build tag or `make test-msb`, the harness quit-key table, or when a harness's TUI startup or quit behavior changes.
---

# Harness smoke tests

## Problem

Harness support can break without any test noticing. `opencode2` did: its `DaemonProvider.DaemonHealthCmd`
sent no credentials, v2's `serve` answers every `/api/*` route with `401` when unauthenticated, and
`ensureDaemon` (`internal/sandbox/vm/daemon.go`) polled to its `daemonReadyTimeout` (60s) before failing. The
daemon was healthy; the launcher could not see it, and the user got "… did not become healthy within 60s"
instead of a TUI. See `opencode2-server-auth.md` for the fix.

No existing test could catch it:

- Every `ensureDaemon` test stubs the shell seam (`daemonShellFunc` plus fake `DaemonProvider`s), so they only
  assert the profile matches the fake the test installed — never the shipped harness binary.
- The `integration` tests build real images and run `docker run … --version` probes
  (`internal/sandbox/image/render_integration_test.go`); they never start a daemon or attach a client.
- Nothing drives the launcher end to end.

The same gap means a newly registered harness has no test that it starts at all.

## Contract

**Tag and location.** The suite lives in package `test/harness/` behind the build tag `msb` (a tag names the
runtime it requires, as `integration` names a real docker daemon). `make test-msb` runs it; it is not part of
`make check`, `make test`, or the default CI. `make compile-all` compiles it (`-tags 'integration msb'`) so a
tagged-only compile regression is caught on every PR.

**Scope of the assertion.** For every name in `agent.Names()`, the suite starts the harness the way a user does
(`agents-sandbox run --agent <name>` under a pseudo-terminal) and asserts:

1. the attach client reached its full-screen TUI — the terminal entered the alternate screen (`ESC [ ? 1049 h`)
   while the process was still alive, within a timeout; a first-run/login screen counts;
2. the process then exited `0` after the harness's quit bytes were written to the terminal; escalation
   (`SIGTERM`/`SIGKILL`) is cleanup only and fails the subtest if reached;
3. the output never contains the daemon-timeout marker (`did not become healthy`).

It proves "the harness reached its TUI", not "the client session is functional": a healthy daemon whose client
renders a connection-error screen still passes. Daemon health itself is covered because the launcher blocks in
`ensureDaemon` before any TUI, so a broken daemon fails the TUI-wait or assertion 3.

**Harness table completeness.** A single table maps harness name to its quit bytes and an optional network
override (`Ctrl-C`, `\x03`, by default). A fast untagged unit test asserts the table's key set equals
`agent.Names()`, so a newly registered harness fails `make test` until it is added — a harness cannot be added
without smoke coverage.

**Isolation (hard requirement).** The child never reads or writes the developer's existing agents-sandbox
configuration or state. Each run creates one temp root, shared by all subtests (which run sequentially), and:

- the child environment is built from an **allowlist**, dropping inherited `HOME`, `XDG_*`,
  `AGENTS_SANDBOX_*`, `OPENCODE_SANDBOX_*`, and `MSB_*`; it sets `HOME=<root>/home`,
  `XDG_CONFIG_HOME=<root>/config`, `XDG_DATA_HOME=<root>/data`, `XDG_STATE_HOME=<root>/state`,
  `XDG_CACHE_HOME=<root>/cache`, and `TMPDIR=<root>/tmp`;
- the working directory is a temp project dir, so project config and the derived slug are throwaway;
- consequently no real `~/.config/agents-sandbox`, `~/.local/{share,state}/agents-sandbox`, or
  `~/.cache/agents-sandbox` path is read or written, and the child starts from deterministic defaults.

The isolated data home forces microsandbox to install its private runtime and build/load each harness's runner
image on first run; that cost is paid once per run. The shared host docker daemon builds the runner images, as
the existing `integration` tests already do. The suite's own process installs the fail-fast seam mocks in
`TestMain`; `TestMain` does nothing else, so `compile-all`'s `-run=^$` stays a genuine no-op (the shared root,
binary build, and preflight live behind a `sync.Once` in the test body).

**Network.** The child runs with `--network none` (the secure default), so no harness egress is exercised. The
override in the harness table exists only if a harness turns out to need egress to render.

**Preflight.** Skip, never fail, when the environment cannot run the suite: `/dev/kvm` missing on Linux, a
non-Linux/non-darwin platform, or an unreachable docker daemon.

**Teardown.** A clean quit lets the launcher stop its VM. A deferred cleanup best-effort destroys any sandbox
left in the isolated home before the temp root is removed, so a failed subtest cannot leak a VM pointing at a
deleted home.

## Non-goals

- TUI content or behaviour beyond reaching the full-screen TUI and quitting on its documented key.
- Credentials, authenticated sessions, or harness network egress.
- Replacing the mocked `internal/sandbox/vm` tests.
- Agent version resolution, the upgrade prompt, or the image build.
- Running on every PR (needs KVM, docker, network — host-only today) and a dedicated CI job.

## Risks and open items

- The alt-screen marker is the "TUI started" signal; a harness that renders without it needs a fallback.
- Quit bytes are per harness and need empirical confirmation on a host; the table is the single adjustment
  point.
- Four harnesses mean four runner-image builds on a cold home; the host-only placement absorbs that.
