---
description: Reverted design for running dockerd/the agent serve daemon as runtime-owned detached jobs. Kept as the re-merge plan: microsandbox counts a detached job as a guest exec session, so any job permanently suppresses the sandbox idle timeout; re-land once microsandbox offers an idle-exempt/service job marker. Records the idle mechanism, the evidence, and the re-merge/revert steps.
read_if: Working on VM idle shutdown, dockerd/agent-serve startup, detached jobs (ExecDetached/Job), issues #146/#147/#148, or deciding whether to re-land the detached-job runtime.
---

# Detached jobs for dockerd and the agent serve daemon (REVERTED)

## Status

**Reverted.** Issue #146 / PR #191 introduced runtime-owned detached jobs for `dockerd` and the agent `serve`
daemon. It was reverted (this branch) because it makes the VM never idle-stop: microsandbox counts a detached
job as an ordinary guest exec session, and its idle monitor treats any active exec session as "not idle", so
the configured `idle_timeout` never fires while a job runs. `dockerd`/`serve` are back on the pre-#146
`nohup … &` shell path.

- Affected / tracking: #146 (reopened), #147 (supervised service hooks), #148 (`jobs` command) — both depend on
  the reverted `internal/sandbox/msb` jobs seam.
- Re-merge condition: an upstream microsandbox idle-exempt/service marker for jobs (drafted proposal to
  `superradcompany/microsandbox`). Until then, do not re-land the job-based runtime.

## Why reverted (idle incompatibility)

- A detached job is an ordinary guest `ExecRequest` stream held by the host runner (the microsandbox jobs
  protocol "deliberately does not change guest exec messages"). agentd's exec handler inserts it into
  `state.sessions`, and the heartbeat reports
  `active_exec_sessions = state.sessions.len() + state.detached_sessions.len()`.
- microsandbox's idle monitor returns `Active` whenever `active_exec_sessions > 0` — it can only go idle at
  zero (`crates/runtime/lib/runner/heartbeat.rs`, consumed in `crates/runtime/lib/runner/vm.rs`). A quiet
  daemon still counts.
- Upstream exposes no job/service/idle-exempt field on `ExecRequest`, no per-session identity in the heartbeat,
  and no SDK exec/job option (repo-wide search for `idle_exempt`/`exclude_from_idle`/`job_session`/`no_idle` is
  empty). The launcher therefore cannot separate job sessions from user activity. Confirmed identical at
  microsandbox `v0.7.8` and `main`.
- Pre-#146, `nohup dockerd … &` was launched via `Shell`, whose exec returns immediately; no session persisted,
  so the idle timer worked.

## Re-merge plan

1. Upstream must accept a way to exclude a job's session from `active_exec_sessions` while still letting its
   output advance `activity_seq` (an opt-in `service`/`idle_exempt` flag). See the drafted microsandbox issue.
2. Then re-land: revert this revert (`git revert <this-branch's-revert-commit>` restores PR #191), or re-apply
   #146 on top, and mark the `dockerd`/`serve` launches as idle-exempt.
3. Verify: a quiet `dockerd` job must let the VM idle-stop; a busy job must keep it alive.

## Reverted design (for the re-merge)

When re-landed, the contract was: `dockerd` and the agent `serve` daemon run as microsandbox runtime-owned
detached jobs through the `internal/sandbox/msb` seam (`Sandbox.ExecDetached` + a minimal `Job` of
`ID/Inspect/Kill/Signal/Logs/Close`), replacing `nohup … &`, logfile polling, and `pkill` plumbing. No shell
fallback (jobs ABI is an optional native symbol; unsupported runtimes surface `JobError{Code:"unsupported_feature"}`).
Readiness is health-probe based (`docker info` / the agent health endpoint) with `Job.Inspect` crash detection
and a `Job.Logs` tail on failure; no live `FollowLogs`. Idempotency is health-probe based, with stale jobs
rediscovered by command match (`ListJobs(WithAllJobs)` → `GetJob` → `Kill`); no persisted job IDs. dockerd keeps
a minimal `containerd`/pid/socket residue cleanup. `agent.DaemonProvider` exposes a structured
`DaemonCommand{Binary,Args}`. Full detail is in git history (PR #191 / commit `a482e8f` and the
reverted branch, where the plan document lived).
