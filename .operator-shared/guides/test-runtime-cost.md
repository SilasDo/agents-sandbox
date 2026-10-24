---
description: Where `go test ./...` wall time actually goes (test-binary linking, not test bodies), why every test binary embeds the 36 MB microsandbox native bundle, and the per-package timeout seams that decide failure latency.
read_if: Trying to speed up `go test`/`make test`, investigating a slow or hanging test run, adding a polling/wait seam whose production budget a test could accidentally consume, or changing a test package's `TestMain`.
---

# Test runtime cost

## The run is build-bound, not test-bound

`go test ./...` spends most of its wall time building and starting test binaries, not running test bodies.

Measured on 2026-10-24 (16-core VM, warm `GOCACHE`, this repo's `GOFLAGS=-count=1`):

- Full run: ~12 s wall, ~55 s user + ~31 s sys.
- Compile-and-start only (`go test -count=1 -run '^$' ./...`): ~10 s wall, ~49 s user + ~24 s sys.

The gap is ~1-2 s. Optimizing test bodies alone can therefore move wall time only marginally; the lever with
real headroom is the build/link step.

## Why linking is paid on every run

`go test` in **run** mode links each test binary into the ephemeral work dir, so the link command's `-o` is a
random `$WORK/b0NN/<pkg>.test` path on every invocation. That path is part of the action's cache key, so the
cached link is never reused:

- Repeated `go test -c -o <same path> ./internal/agent` — second run links zero times (cache hit).
- Repeated `go test -c -o <different path> ...` (or any run-mode `go test`) — links every time, with an
  identical `-buildid`.

So every `go test ./...` re-links all ~39 test binaries; there is no way to make run mode reuse the link.

## Why each link is expensive: the embedded native bundle

The microsandbox Go SDK embeds a native shared library into `internal/bundle`
(`libmicrosandbox_go_ffi-linux-amd64.so`, ~36 MB; analogous dylibs/dlls per platform) via `//go:embed`. Any test
binary that imports the `msb` package — hence `ffi` and `bundle` — carries that payload, so every test binary is
~44-55 MB regardless of how small the package under test is (`internal/humanize`, a leaf package, is ~47 MB).

Test binaries import `msb` through the isolation contract, not because their production code needs it: the
wrapper `testutil/mocks.InitFailFastMocks` imports `configpaths`, `docker`, `doctor`, `msb`, and `notify`, and
`test-isolation.md` requires every test package to install the full acyclic seam-family set. A test binary that
never calls native code still links the bundle.

Two data points bound the payoff of avoiding the bundle: a leaf test binary whose `TestMain` installs only
`docker.InstallFailFastGet()` directly (as `internal/git` and `internal/agent` do) is ~13-14 MB and links in
~1.3-1.7 s; one that installs the full wrapper is ~47 MB and links in ~2.4 s.

## Test-body hotspots (secondary)

Per-package wall from a `go test -json` profile: `cmd/agents-sandbox` ~3.3 s (273 tests, all serial);
`internal/git` ~2.4 s (many tests spawn the real `git` for `worktree add`/`clone`/`submodule add`);
`test/harness` ~0.7 s of deliberate pty timeout waits. `internal/sandbox/msb` was ~2.4 s, almost entirely
`TestRealMsbClientEnsureInstalled` running the real SDK installer over the network; that test now injects the
`ensureRuntime` seam instead, and the package runs in ~0.1 s.

There is no `t.Parallel()` in the repo. It is not absent by oversight: `configpaths.WithMockConfigPaths` uses
`t.Setenv`, and the `InstallFailFast*`/`Get` seams are process-global, so within-package parallelism is
precluded by the same design that makes the fail-fast defaults work.

## Timeout seams and failure latency

Tests that poll production budgets can hide a hang: with a missed mock the run waits the production timeout
before failing. The catchable package vars and their production values:

- `vm`: `daemonReadyTimeout` 60 s / `daemonPollInterval` 2 s; `dockerdReadyTimeout` 30 s /
  `dockerdPollInterval` 1 s; `sandboxLifecyclePollInterval` 100 ms.
- `session`: `waitQuiescentPollInterval` 2 s.
- `termio`: `spinnerInterval` 100 ms.
- `options`: `SandboxStopTimeout` / `DefaultVMIdleTimeout` 30 s (consts).
- `notify`: `backoffStart` 1 s / `backoffMax` 30 s / `longLived` 30 s; HTTP clients in `agent`/`upgrade` use
  10 s (and a 5 min download).

`vm`'s and `session`'s `TestMain`s now cap `daemonReadyTimeout`/`daemonPollInterval`,
`dockerdReadyTimeout`/`dockerdPollInterval`, `sandboxLifecyclePollInterval`, and `waitQuiescentPollInterval`,
so a mock that never answers fails in ~2 s or ~10 ms instead of the production budget. A test that needs the
production window (or a deliberately expired one) saves and restores the var itself. The dockerd production
default is a named const (`dockerdReadyTimeoutDefault`) so `TestDockerdReadyTimeoutAllowsFailOver` keeps
asserting the default, not the test-shortened var.

`make test`/`coverage`/`coverage-junit` pass `-timeout $(TEST_TIMEOUT)` (default 5m), so a wedged binary panics
with a stack dump instead of blocking for Go's default 10 minutes per package.
`test-integration`/`test-msb` already pass `-timeout 60m`.

## Levers, with cost

- **Cap every polling seam in its package `TestMain`** (done for `vm` and `session`). Cheap, directly bounds
  failure latency. A test that needs the long path overrides the var locally.
- **Add `-timeout` to `make test`/`make coverage-junit`** (done; `TEST_TIMEOUT`, default 5m). Cheap; bounds a
  wedged binary. Must exceed the slowest single package on the slowest runner.
- **Keep the network out of unit tests** (`TestRealMsbClientEnsureInstalled`, done). The installer path stays
  covered by `TestRealClientEnsureInstalledPassesPrivateHome`, which injects the seam.
- **Stop embedding the native bundle in test binaries that cannot reach it.** Largest build-time lever; it
  means changing what `test-isolation.md` requires each package to install. See
  `plans/2026-10-24-test-build-cost-reduction.md`.
