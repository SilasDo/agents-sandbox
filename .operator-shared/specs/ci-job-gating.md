---
description: Contract for what `.github/workflows/ci.yml` runs and gates — `lint`/`test` always run (the only required checks), `race`/`vulncheck`/`deadcode` run when the `changes` gate reports code and are skipped on a code-free change, the `fuzz` and `mutation` workflows are deliberately outside this gating (see `fuzz-ci.md` and `mutation-testing.md`), the costly `integration` (Docker Engine) and `integration-podman` (rootless podman) jobs run the same build suite on a code-affecting `main` push, on release tags, on manual dispatch, and on a PR a maintainer opted in with the matching `run-integration` / `run-integration-podman` label; plus the PR-only concurrency group, the `changes` code-free gate, and the invariants that keep those conditions live (declared triggers, `pull_request.types` defaults, why integration must stay in `ci.yml`).
read_if: Changing the CI triggers or job conditions in `.github/workflows/ci.yml`, the integration or integration-podman job's gating, the `run-integration`/`run-integration-podman` opt-in labels, the PR-only concurrency group, the `changes` code-free gate (integration and `race`/`vulncheck`/`deadcode`), or deciding whether a check belongs in CI.
---

# CI job gating

## Contract

`.github/workflows/ci.yml` always runs `lint` and `test` — the only two required branch checks. `race` (`make test-race`),
`vulncheck` (`make vulncheck`), and `deadcode` (`make deadcode`) are **not** required, so they `needs: [changes]` and run
on a PR, main push, tag, or dispatch only when the `changes` gate reports `code=true`; a code-free change skips them. The
`integration` job (`make test-integration`, the `integration`-tagged docker-build suite, ~10 minutes) is gated to four
paths:

1. `github.ref == 'refs/heads/main'` **and** the `changes` job reports `code=true` — a code-affecting push to `main`. A
   merge that touches only docs, the operator brain, the Homebrew formula, issue templates, or `.gitignore` sets
   `code=false` and skips both integration jobs.
2. `startsWith(github.ref, 'refs/tags/')` — every release tag. `release` declares
   `needs: [lint, test, integration, integration-podman]`, so both integration jobs stay in this workflow; a second
   workflow would drop that gate.
3. `github.event_name == 'workflow_dispatch'` — a manual run.
4. `github.event_name == 'pull_request' && contains(github.event.pull_request.labels.*.name, 'run-integration')` — a
   maintainer opts a PR in by adding the label. This is the only path that exercises the suite on PR code before merge.

`integration-podman` runs the same suite against a rootless podman endpoint (the step starts `podman system service` and
points `DOCKER_HOST` at its socket) with the same four gates and its own `run-integration-podman` label. It exists to
hold podman to the claim that it is a tested Docker-compatible engine, so a podman-only failure has its own job and label
and never blocks a PR a maintainer ran Docker Engine for.

### Concurrency and the `changes` gate

The workflow sets `concurrency.group` to `ci-<ref>` for pull requests and a per-run id otherwise, with
`cancel-in-progress: true` only for pull requests: a re-pushed PR cancels its superseded run (its `lint`/`test` required
checks re-report on the newer commit), while a main push, tag, or dispatch is never cancelled or serialized.

The `changes` job runs on every event, cheaply, and reports whether the push changed anything that can affect the runner
image or the tests — the complement of `mutation.yml`'s `paths-ignore` list (docs, `.operator-shared/`, `Formula/`,
`.github/ISSUE_TEMPLATE/`, `**.md`, `.gitignore`). It diffs the event's base against `github.sha`: a push uses the
payload's `before` tip, a pull request (whose payload has no `before`) the base branch's merge base, so a PR is judged on
its own changeset. `race`, `vulncheck`, and `deadcode` `needs: [changes]` and, on a PR or main push, run only when
`code=true`; tags and dispatch always run them. Both integration jobs `needs: [changes]` and, on a `refs/heads/main`
push, additionally require `needs.changes.outputs.code == 'true'`. Tags, dispatch, and the PR opt-in labels ignore the
gate. CodeQL keeps its own equivalent `paths-ignore` in `.github/workflows/codeql.yml`; see `codeql-scanning.md`.

## Invariants

- Every `github.event_name == '<event>'` a job condition references must be declared in the workflow's `on:` block.
  Breaking this is silent, not loud: the condition is merely never true. The integration job referenced
  `workflow_dispatch` while `on:` declared no dispatch trigger, so the documented manual path did not exist.
- `on.pull_request.types` must list `labeled` **and** repeat the defaults (`opened`, `synchronize`, `reopened`):
  declaring `types:` replaces the default set rather than extending it.
- Both `ci.yml` and `fuzz.yml` declare a workflow-level `permissions: contents: read`; the `publish` job raises its own (`contents:
  write`, `pull-requests: write`) to publish the Release and open the Homebrew bump PR. `mutation.yml` does the same
  (`contents: read` workflow-level, `pull-requests: write` on its job) for its sticky survivor comment. CodeQL's
  `actions/missing-workflow-permissions` query reports one alert **per job** with no effective limitation, so removing the
  workflow-level block reopens one alert per job rather than one per workflow.
- GitHub only offers dispatch for workflows whose file exists on the default branch. The `workflow_dispatch` trigger
  therefore becomes usable when it reaches `main`; `gh workflow run ci.yml --ref <branch>` can then dispatch any branch.
- The label names have no single source of truth: each appears in its job condition, in the job's comment, and in
  CONTRIBUTING's Testing section. GitHub also needs a label to exist in the repository before anyone can add it:
  `run-integration` and `run-integration-podman` are created there (delete with `gh label delete <name>`). A
  `run-mutation` label was created for an earlier opt-in design and is now unused — the mutation sweep runs on every PR.
- Applying a label needs the repository's **triage** role or higher — read alone is not enough — so anyone with write
  access qualifies and a read-only collaborator or outside contributor cannot self-serve. That is why CONTRIBUTING says
  to ask a maintainer. A label a contributor sets in their own fork does nothing: the condition reads the base
  repository's labels on the PR.
- The repository's `protect main branch` ruleset requires only `lint` and `test`. `race`, `vulncheck`, `deadcode`, and both
  integration jobs are optional, which is what makes the `changes` gate safe: a skipped job cannot leave a required check
  pending.

## Behavior

- `release` gates on `lint`, `test`, `integration`, and `integration-podman`. Podman is a supported engine, so both the
  reference engine (Docker Engine) and the alternative engine (rootless podman) must pass their build suite before a
  release publishes — the alternative-engine claim is verified before shipping, not after. `race` and `vulncheck` run on
  a tag but do not block publishing, so a new reachable vulnerability in a dependency cannot wedge a release. Promote
  them into `release.needs` deliberately if that trade-off changes.
- Adding either label re-runs the whole workflow, so `lint`/`test` run again for that event.
- `make fuzz` runs from its own workflow, not from `ci.yml`: it is neither a PR gate nor part of `make verify`. See
  `fuzz-ci.md` for its triggers, budgets, and corpus handling.
- `make mutation` likewise runs from its own `mutation.yml`, on every code-changing PR (docs are filtered out; and
  manual dispatch), and is never a required check or a `release.needs` member. See `specs/mutation-testing.md`.
- The label persists on the PR, so every later `synchronize` push runs the integration job again; removing the label
  stops that.
- The gate is not a privilege boundary: `lint`/`test` already execute PR code on the same runner, so labeling adds
  compute, not trust. For fork PRs, `pull_request` (never `pull_request_target`) withholds secrets and forces a read-only
  token, and the integration job uses no secrets in any case. Independently, a first-time contributor's fork PR needs a
  maintainer's workflow approval before any job runs (repo setting `fork-pr-contributor-approval`:
  `first_time_contributors`).
