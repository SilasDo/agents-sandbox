---
description: Contract for the `fuzz` workflow (`.github/workflows/fuzz.yml`) — its triggers and the budget each passes, why it is never a PR gate and not part of `make check`/`make verify`, how the corpus accumulates between runs, how a crasher leaves the runner, and the `make fuzz` knobs it drives.
read_if: Changing `.github/workflows/fuzz.yml`, the `make fuzz` target or its `FUZZTIME`/`FUZZPKG` knobs, or deciding whether a fuzzing session belongs in CI.
---

# Fuzz workflow

## Contract

`.github/workflows/fuzz.yml` explores the four fuzz targets — the project Dockerfile (`image.RenderDockerfile`), the
`home:` manifest, `--older-than` duration parsing, and image-reference round-tripping (`naming`) — on `ubuntu-latest`.
These targets are pure Go: no KVM, no docker daemon, no network. That is why they can run on a GitHub-hosted runner at
all, unlike the `msb` smoke tests and the `integration` job.

| Trigger | Budget passed to `make fuzz` | Why |
|---|---|---|
| `push` to `main` touching one of the four package dirs | `60s` | the run is attributable to the merge that broke it; depth is not the point here |
| `schedule`, nightly at `17 3 * * *` | `15m` | where depth comes from, accumulating into the corpus |
| `workflow_dispatch`, `fuzztime` input | the input (default `15m`) | an explicit long run before a release |

The merge trigger is path-filtered to the four package directories: a merge that cannot change what these targets parse
has nothing new for them to find, which also keeps four jobs off every dependency bump. One matrix job per target
(`internal/viperconfig`, `internal/homeconfig`, `internal/sandbox/image`, `internal/sandbox/naming`), because
`go test -fuzz` refuses more than one package per run; `fail-fast: false` keeps one target's crasher from hiding the
others, and `timeout-minutes: 45` bounds a job.

## Invariants

- **Never a gate.** Fuzzing is not a PR check, not a step in `make check`/`make verify` (which mirrors PR CI), and not in
  `ci.yml`'s `release.needs`. A red run reports a crasher; it cannot block a merge or a release. What makes a finding
  durable is the committed corpus, not the run.
- **A cancelled run is a wasted run.** The job's concurrency group is `fuzz-<target>` with `cancel-in-progress: false`, so
  a new run waits for the in-flight one instead of cancelling it mid-budget. GitHub still drops a superseded *pending*
  job, which is intended: only the newest queued run matters.
- **The corpus accumulates in `$GOCACHE/fuzz`, cached on its own per target.** Go writes every input it finds there, so
  the job restores `~/.cache/go-build/fuzz` from an `actions/cache` entry keyed
  `fuzz-corpus-<target>-<run id>-<run attempt>` with a `fuzz-corpus-<target>-` restore prefix: a unique key always saves
  (GitHub caches are immutable, so a fixed key is written once and never updated), and the prefix restores the newest
  entry for that target. It must **not** rely on `setup-go`'s cache for this — that entry is keyed on `go.sum` and is
  never re-saved on an exact hit, so a run's discoveries would be thrown away, and other jobs sharing the key can save it
  first. The corpus is small: a 3s run yields tens of inputs and ~128K.
- **A crasher leaves the runner as an artifact.** Go writes a failing input to the package's `testdata/fuzz/<Target>/` and
  prints it in the log; the failing job uploads `internal/**/testdata/fuzz/**`. CI does not commit the seed (that needs a
  push token, which the workflow deliberately does not have) — a maintainer or agent commits it, and from then on it runs
  under `make test`.
- **`FUZZPKG` fails loudly.** `make fuzz FUZZPKG=<package path>` selects exactly that path and exits non-zero when nothing
  matches, so a typo cannot read as a green run over zero targets.
- A schedule runs only from the default branch's copy of the workflow, and always against the default branch: this
  workflow is inert until it merges, and it never fuzzes a PR branch.
