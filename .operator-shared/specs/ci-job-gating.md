---
description: Contract for gating the costly integration job in `.github/workflows/ci.yml` — it runs on `main`, on release tags, on manual dispatch, and on a PR a maintainer opted in with the `run-integration` label; plus the invariants that keep those conditions live (declared triggers, `pull_request.types` defaults, why integration must stay in `ci.yml`).
read_if: Changing the CI triggers or job conditions in `.github/workflows/ci.yml`, the integration job's gating, or the `run-integration` opt-in label.
---

# CI job gating

## Contract

`.github/workflows/ci.yml` runs `lint` and `test` on every push to `main`, every `v*` tag, and every PR event. The
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

## Behavior

- Adding the label re-runs the whole workflow, so `lint`/`test` run again for that event.
- The label persists on the PR, so every later `synchronize` push runs the integration job again; removing the label
  stops that.
- The gate works for fork PRs. The job uses no secrets, and `pull_request` (never `pull_request_target`) keeps the token
  read-only, so a labeled fork PR only spends runner minutes.
