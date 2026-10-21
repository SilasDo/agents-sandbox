---
description: Contract for what `.github/workflows/ci.yml` runs and gates — `lint`/`test`/`race`/`vulncheck` run ungated, `make fuzz` is deliberately absent, and the costly `integration` job runs on `main`, on release tags, on manual dispatch, and on a PR a maintainer opted in with the `run-integration` label; plus the invariants that keep those conditions live (declared triggers, `pull_request.types` defaults, why integration must stay in `ci.yml`).
read_if: Changing the CI triggers or job conditions in `.github/workflows/ci.yml`, the integration job's gating, the `run-integration` opt-in label, or deciding whether a check belongs in CI.
---

# CI job gating

## Contract

`.github/workflows/ci.yml` runs `lint`, `test`, `race` (`make test-race`), and `vulncheck` (`make vulncheck`) on every
push to `main`, every `v*` tag, and every PR event — all four ungated. The
`integration` job (`make test-integration`, the `integration`-tagged docker-build suite, ~10 minutes) is gated to four
paths:

1. `github.ref == 'refs/heads/main'` — every push to `main`.
2. `startsWith(github.ref, 'refs/tags/')` — every release tag. `release` declares `needs: [lint, test, integration]`, so
   the integration job stays in this workflow; a second workflow would drop that gate.
3. `github.event_name == 'workflow_dispatch'` — a manual run.
4. `github.event_name == 'pull_request' && contains(github.event.pull_request.labels.*.name, 'run-integration')` — a
   maintainer opts a PR in by adding the label. This is the only path that exercises the suite on PR code before merge.

## Invariants

- Every `github.event_name == '<event>'` a job condition references must be declared in the workflow's `on:` block.
  Breaking this is silent, not loud: the condition is merely never true. The integration job referenced
  `workflow_dispatch` while `on:` declared no dispatch trigger, so the documented manual path did not exist.
- `on.pull_request.types` must list `labeled` **and** repeat the defaults (`opened`, `synchronize`, `reopened`):
  declaring `types:` replaces the default set rather than extending it.
- GitHub only offers dispatch for workflows whose file exists on the default branch. The `workflow_dispatch` trigger
  therefore becomes usable when it reaches `main`; `gh workflow run ci.yml --ref <branch>` can then dispatch any branch.
- The label name has no single source of truth: it appears in the job condition, in the job's comment, and in
  CONTRIBUTING's Testing section. GitHub also needs the label to exist in the repository before anyone can add it:
  `run-integration` is created there (delete with `gh label delete run-integration`).
- Applying a label needs the repository's **triage** role or higher — read alone is not enough — so anyone with write
  access qualifies and a read-only collaborator or outside contributor cannot self-serve. That is why CONTRIBUTING says
  to ask a maintainer. A label a contributor sets in their own fork does nothing: the condition reads the base
  repository's labels on the PR.

## Behavior

- `release` gates on `lint`, `test`, and `integration` only. `race` and `vulncheck` run on a tag but do not block
  publishing, so a new reachable vulnerability in a dependency cannot wedge a release. Promote them into `release.needs`
  deliberately if that trade-off changes.
- Adding the label re-runs the whole workflow, so `lint`/`test` run again for that event.
- `make fuzz` (`go test -fuzz`, `FUZZTIME` per target) is deliberately neither a CI job nor a `make check`/`make verify`
  step. A session worth running needs a time budget, and Go's fuzzing is nondeterministic, so the durable part is the
  corpus: the seed corpus and any committed crasher run under `make test`, which is where a found defect becomes a
  regression test. If it is ever scheduled, give it a real budget rather than shaving it into a per-push gate.
- The label persists on the PR, so every later `synchronize` push runs the integration job again; removing the label
  stops that.
- The gate is not a privilege boundary: `lint`/`test` already execute PR code on the same runner, so labeling adds
  compute, not trust. For fork PRs, `pull_request` (never `pull_request_target`) withholds secrets and forces a read-only
  token, and the integration job uses no secrets in any case. Independently, a first-time contributor's fork PR needs a
  maintainer's workflow approval before any job runs (repo setting `fork-pr-contributor-approval`:
  `first_time_contributors`).
