# Branch and worktree cleanup

`scripts/cleanup-merged-branches.sh` (run as `make cleanup-branches ARGS=...`) removes
merged worktrees and local/origin branches, then reports everything left, fresh entries in
green and stale ones in yellow. It is repo-maintenance tooling, not part of `make check`.

## Merge detection

A branch is removed when its tip is:

- an ancestor of `origin/main` or any `origin/release/*` (fast-forward / merge-commit);
- equal to the head commit of a merged PR (`gh pr list`) — catches squash/rebase merges;
- patch-equivalent via `git cherry` — catches rebase merges offline.

The PR check requires the branch tip to equal the PR head commit. Matching by branch name
alone is unsafe: a branch can gain commits after its PR merges, and deleting it would
discard them.

## Safety

- Prunable worktrees (a registered worktree whose directory is missing here) are never
  touched: the directory may still exist on the host while absent in the VM.
- Dirty worktrees require `--force`; long-lived refs (`main`, `release/*`) and the current
  branch/worktree are never removed.
- A worktree whose branch has no commits of its own (tip is an ancestor of `main`) is kept
  when its tip is the local default branch or newer, or when the worktree was created within
  `--fresh` (default 30m). This protects a just-created session worktree; `--force` does not
  override it.
- Run in a host session with no agents active. The script only protects its own working
  directory, so it cannot see a worktree another host or agent is using.
