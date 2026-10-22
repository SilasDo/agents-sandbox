---
description: Contract for the `fuzz` workflow (`.github/workflows/fuzz.yml`) — how it discovers targets from the sources (no hardcoded package list), its triggers and the budget each passes, why it is never a PR gate and not part of `make check`/`make verify`, how the corpus accumulates between runs, how a crasher leaves the runner, and the `make fuzz` knobs it drives.
read_if: Changing `.github/workflows/fuzz.yml`, the `make fuzz` target or its `FUZZTIME`/`FUZZPKG` knobs, adding a fuzz target and expecting CI to run it, or deciding whether a fuzzing session belongs in CI.
---

# Fuzz workflow

## Contract

`.github/workflows/fuzz.yml` explores every fuzz target discovered in the module — the persisted state files
(`state.FuzzParseState`, both the per-project and launcher-global documents), size and `--worktree` specs
(`options.FuzzParseOptions`), configured mount-target validation (`mounts.FuzzResolveMountTarget`), the retired
`updater.yaml` (`vm.FuzzParseLegacyUpdaterState`), the project Dockerfile (`image.FuzzRenderDockerfile`), the `home:`
manifest (`homeconfig.FuzzParseHomeSection`), `--older-than` durations (`viperconfig.FuzzParseHumanDuration`), and
the naming package's artifact-name and image-reference round-trips (`naming.FuzzArtifactNameRoundTrip`,
`naming.FuzzRunnerImageRefRoundTrip`) — on `ubuntu-latest`. These targets are pure Go:
no KVM, no docker daemon, no network. That is why they can run on a GitHub-hosted runner at all, unlike the `msb` smoke
tests and the `integration` job.

| Trigger | Budget passed to `make fuzz` | Why |
|---|---|---|
| `push` to `main` touching `internal/**`, `cmd/**`, or `test/**` | `60s` | the run is attributable to the merge that broke it; depth is not the point here |
| `schedule`, nightly at `17 3 * * *` | `15m` | where depth comes from, accumulating into the corpus |
| `workflow_dispatch`, `fuzztime` input | the input (default `15m`) | an explicit long run before a release |

### Target discovery

The list is derived, not hardcoded. A `discover` job lists the packages that contain a target
(`grep -rl '^func Fuzz' --include='*_test.go' internal cmd test | xargs -n1 dirname | sort -u`), turns each package
into `{name, path}` JSON, and the `fuzz` job builds its matrix from `fromJSON(needs.discover.outputs.pkgs)`. Adding a
`func Fuzz...` in a package under those trees joins the matrix with no workflow edit — including a *second* target in an
existing package, which `make fuzz` discovers and runs on its own. The merge `paths` filter names the same three trees,
because a new target can only land under one of them. CI and the Makefile therefore agree on what a target is by
construction. `name` is the package path with `/` flattened to `-` (e.g. `internal-sandbox-state`), unique across
packages, so it keys both the per-target corpus cache and the concurrency group.

One matrix job per package, because `go test -fuzz` refuses more than one package per run. Within a package `make fuzz`
enumerates each `func Fuzz...` and runs it alone with `-fuzz='^Name$'` (Go accepts exactly one target per run), continuing
past a failure so a crasher in one target does not hide the others — the same intent as the workflow's `fail-fast: false`,
which keeps one package's failure from hiding another's. `timeout-minutes: 45` bounds a job.

## Invariants

- **Never a gate.** Fuzzing is not a PR check, not a step in `make check`/`make verify` (which mirrors PR CI), and not in
  `ci.yml`'s `release.needs`. A red run reports a crasher; it cannot block a merge or a release. What makes a finding
  durable is the committed corpus, not the run.
- **Discovery can never yield an empty matrix.** The `discover` job exits non-zero when it finds no package, mirroring
  `make fuzz FUZZPKG`, so GitHub's "matrix must contain at least one value" error can never be reached; a target the
  module genuinely lost is a loud failure, not a silent disappear.
- **A cancelled run is a wasted run.** The job's concurrency group is `fuzz-<name>` with `cancel-in-progress: false`, so
  a new run waits for the in-flight one instead of cancelling it mid-budget. GitHub still drops a superseded *pending*
  job, which is intended: only the newest queued run matters.
- **The corpus accumulates in `$GOCACHE/fuzz`, cached on its own per target.** Go writes every input it finds there, so
  the job restores `~/.cache/go-build/fuzz` from an `actions/cache` entry keyed
  `fuzz-corpus-<name>-<run id>-<run attempt>` with a `fuzz-corpus-<name>-` restore prefix: a unique key always saves
  (GitHub caches are immutable, so a fixed key is written once and never updated), and the prefix restores the newest
  entry for that target. It must **not** rely on `setup-go`'s cache for this — that entry is keyed on `go.sum` and is
  never re-saved on an exact hit, so a run's discoveries would be thrown away, and other jobs sharing the key can save it
  first. The corpus is small: a 3s run yields tens of inputs and ~128K. `name` changed from the bare basename to the
  flattened path when discovery landed, so each pre-discovery target's corpus restarts once under its new key.
- **A crasher leaves the runner as an artifact.** Go writes a failing input to the package's `testdata/fuzz/<Target>/` and
  prints it in the log; the failing job uploads `internal/**/testdata/fuzz/**` (a `cmd/` or `test/` target's corpus is
  not under that glob and would need the upload path widened). CI does not commit the seed (that needs a push token,
  which the workflow deliberately does not have) — a maintainer or agent commits it, and from then on it runs under
  `make test`.
- **`FUZZPKG` fails loudly.** `make fuzz FUZZPKG=<package path>` runs that package's targets and exits non-zero when
  nothing matches, so a typo cannot read as a green run over zero targets.
- A schedule runs only from the default branch's copy of the workflow, and always against the default branch: this
  workflow is inert until it merges, and it never fuzzes a PR branch.
