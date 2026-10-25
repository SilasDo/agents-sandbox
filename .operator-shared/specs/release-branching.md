---
description: Contract for the per-minor release branch `release/<major>.<minor>` — cut at the release commit, the branch the line's patch releases are tagged from, and where fixes for a released line land while `main` keeps new work.
read_if: Deciding where a release tag or a fix lands, cutting a patch release for a released line, backporting a fix, or changing the release branch name or flow.
---

# Release branching

## Contract

- `main` is the development line: new features and non-urgent fixes land there.
- Every **minor** (and **major**) release cuts a long-lived release branch
  `release/<major>.<minor>` (for example `release/0.6`) at the commit the release is tagged on.
- **Patch releases for a line are cut from that line's release branch.** The fix merges into
  `release/<major>.<minor>` and the tag `vX.Y.(Z+1)` lands on that branch's tip.
- A fix that also applies to `main` is forward-ported there. `main` is never reset to, or held at,
  a release branch.
- At a minor release the tag is reachable from both lines (the branch is created at the same
  commit); at a patch release it is on the release branch only.

## Cutting a release

The mechanics live in the release runbook (`AGENTS.md` points at it): the person cutting the
release creates the branch and the tag — by pushing from the host or, when the maintainer asks,
through `gh` — and then monitors CI through the published GitHub Release, the Homebrew bump PR,
and the docs deploy.
