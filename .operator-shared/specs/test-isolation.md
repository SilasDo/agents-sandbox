---
description: Contract for keeping tests off real host state - the fail-fast seam mocks installed by every test package's TestMain, the host-home redirect in WithMockConfigPaths, and the seam import-chain exceptions.
read_if: Adding a test package or a TestMain, or debugging a test that reads or writes real host state (config, home, docker, msb, doctor).
---

# Test isolation

## Contract

Tests must never read or write real host state. The tool dispatches all side-effecting work through injectable seams;
under tests each seam defaults to a **fail-fast** implementation that panics on first use, so a test that reaches real
resolution fails loudly instead of silently touching the host.

Every test package installs the fail-fast defaults in `TestMain`, for every seam family its tests can import without a
test import cycle. The normal call is
`termio.InitFailFastMocks(m)` (`internal/testutil/mocks`), which installs all four seam families:

- `configpaths.InstallFailFastConfigPaths()`
- `docker.InstallFailFastGet()`
- `doctor.InstallFailFast()`
- `msb.InstallFailFastGet()`

A test that legitimately exercises a seam opts in with `WithMockConfigPaths(t)` (or the docker/msb/doctor equivalent).

The set is enforced, not hand-maintained: `TestEveryTestPackageInstallsItsAcyclicFailFastMocks`
(`internal/testutil/isolation_contract_test.go`) derives each package's expected families from the module import graph —
a family is skipped only when the family's package imports the test package, which is exactly when importing it would
cycle. A new test package, a dropped installer, or a new seam family fails the test until the mocks match the graph.

Packages in the seam import chain cannot import `internal/testutil/mocks` without a test import cycle; they install the
reachable subset directly, mirroring that body:

- `configpaths` (installs configpaths + docker; doctor and msb are unreachable and would cycle)
- `docker` (installs configpaths + docker + msb; doctor imports docker, so it would cycle)
- `msb` (installs configpaths + docker + msb; doctor imports msb, so it would cycle)
- `termio` (installs configpaths + docker + msb; doctor imports termio, so it would cycle)
- `doctor` (installs all four itself; importing `internal/testutil/mocks` would cycle through doctor)
- `runtime` (installs configpaths + docker + msb; doctor imports runtime, so `InitFailFastMocks` would cycle and doctor
  is unreachable from runtime)
- `agent` and `filewalk` install docker only: `configpaths` imports `agent`, which imports `filewalk`, so no
  configpaths-reachable family is importable. Neither package reaches a seam in its own code; if one ever gains such a
  call, the seam must be reached through an injected dependency rather than a global.

Add a `main_test.go` with such a `TestMain` to every new package that has tests; the contract test fails without one.

## Host-home redirect

`configpaths.WithMockConfigPaths(t)` roots a `mockConfigPaths` in `t.TempDir()` **and** redirects `$HOME` into
`<tmp>/home`, so a stray `os.UserHomeDir()` / `os.Getenv("HOME")` in a test resolves inside the temp tree. Tests using
it must not be `t.Parallel` (`t.Setenv` forbids it). `WithMockConfigPathsHomeError(t, err)` installs a mock whose
`UserHomeDir` fails, to exercise host-home resolution failures.

## Audit status (2026-10-16)

A full sweep of `internal/**` and `cmd/**` found the seam model holding: no production path outside the documented
`configpaths` exceptions (`mounts` `~/` sources, `docker`'s `~/.docker`) reads or writes host config/home state, and no
seam family is reached without its injectable factory. The remaining ambient paths are read-only or transient:

- `options.FirstFreeHostPort` probes a real loopback port through the `probeHostPort` var seam (added so tests inject a
  deterministic probe); production still binds/frees a transient socket. Read-only.
- `notify`'s backend runs the notifier through the `execCommand` var seam, whose `TestMain` now installs an
  `InstallFailFast` panic by default (tests opt in by overriding it); the temp WAV still goes to the real `os.TempDir`.
- `os.Getuid`/`Getgid` (image build args), `os.Getwd` (workspace source), `time.Now` (names/polling/spinner), and
  `viperconfig`'s env reads are read-only or the input itself, not leaks.

`agent` and `filewalk` keep the docker fail-fast as their only backstop (every other family cycles through
`configpaths`). Keep any future host-path need in them injected, never global.

## Why (the incident this encodes)

`TestConfigAgentPrintsMergedAndHostFiles` built the host opencode dir from `os.Getenv("HOME")` and wrote
`{"model":"host"}` into the developer's real `~/.config/opencode/opencode.jsonc`. The next launcher start read that file
as the agent's merged config, found it different from the host snippet merge, and raised the daemon-restart
"agent config changed" prompt (see `reconfig-decision.md`). `go test` must not leave the host in a state a later run
reacts to. The tree-wide fail-fast defaults and the `$HOME` redirect close this class.
