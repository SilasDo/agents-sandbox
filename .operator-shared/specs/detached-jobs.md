---
description: Contract for running dockerd and the agent serve daemon as microsandbox runtime-owned detached jobs (ExecDetached/Job) instead of shell-backgrounded nohup processes; health-probe readiness with Job.Inspect crash detection and Job.Logs on failure, no shell fallback, kill-by-command rediscovery, and the v0.7.11 idle fix that made re-landing safe.
read_if: Working on the VM's dockerd/agent-serve startup, readiness, or restart in internal/sandbox/vm, the msb detached-job seam, the agent DaemonProvider daemon command, or the VM idle timeout.
---

# Detached jobs for dockerd and the agent serve daemon

## Contract

- `dockerd` and the agent `serve` daemon are launched as microsandbox **runtime-owned detached jobs** through the
  `internal/sandbox/msb` seam (`Sandbox.ExecDetached` plus a minimal `Job` of `ID/Inspect/Kill/Signal/Logs/Close`). The
  runtime owns the process: it survives client disconnect and is inspectable/killable by any later client.
- **No shell fallback.** The previous `nohup … &` backgrounding, `/var/log/dockerd.log` (and agent `*-serve.log`)
  redirects, `pkill -f '… serve'`, and `pkill dockerd` plumbing are removed. The jobs ABI is an optional native symbol
  (`has_msb_jobs`); an unsupported runtime surfaces the SDK's `JobError{Code:"unsupported_feature"}`, and the launcher
  trusts the pinned SDK plus the force-reinstalled private runtime rather than degrading to a shell path.
- **Readiness is health-probe based**, not `Job.Wait`: `docker info` for dockerd, the agent health endpoint for the
  daemon. Each poll iteration consults `Job.Inspect` and fails fast (structured failure naming the exit code) when the
  process has exited (`JobInfo.ExitCode != nil`). On timeout it embeds a `Job.Logs` tail (last 100 bytes) and the last
  health-probe detail instead of a logfile read.
- **No live log streaming.** `Job.FollowLogs` is deliberately unused; verbose behaviour is unchanged (the daemon's
  per-poll detail is still logged) and job logs are read only on failure/timeout.
- **Idempotency is health-probe based.** A healthy dockerd/daemon is left running; before (re)starting, any live job
  matching the command is killed first. Jobs carry no labels, so rediscovery matches `JobInfo.Command` against the
  expected binary+args prefix (`ListJobs(WithAllJobs)` → `GetJob(id)` → `Kill`).
- **No persisted job state.** Jobs die with the VM, so a stopped/restarted VM re-launches both on the next boot. There
  are no job IDs in `state.yaml` and no cross-boot rediscovery.
- **Rollout from a pre-jobs runtime.** A VM still running from before this change holds `nohup`-started processes the
  job matcher cannot see; a config-change restart against such a live VM cannot kill them, so the fresh job fails to bind
  (`:4096`/the socket) and surfaces a structured failure or timeout. This is accepted rather than mitigated with legacy
  `pkill` cleanup (the no-fallback rule): VMs are ephemeral, and a stop/start relaunches both as jobs.
- **dockerd teardown keeps residue cleanup.** Killing the dockerd job does not necessarily reap its `containerd` child,
  so a minimal shell cleanup (`pkill containerd` plus `/run`,`/var/run` pid/socket deletion) still runs before a
  restart. The job kill replaces `pkill dockerd`.
- **dockerd's storage driver.** The vfs request is built as arguments (`dockerdArgs`) and passed to `ExecDetached`, not
  as a shell string. It keeps the #287 rule: request `--storage-driver=vfs` unless the image's `daemon.json` already
  sets vfs (then the flag is dropped), and fail when it sets any other driver.
- **Agent command shape.** `agent.DaemonProvider` exposes a structured `DaemonCommand{Binary, Args}` for the detached
  launch (health command/parse unchanged); it no longer exposes shell start/kill strings or a log path. Serve-only binds
  `0.0.0.0` (loopback `127.0.0.1` otherwise) so the host's published-port forwarder reaches the guest.
- **Guest tools.** The daemon agents declare only `curl` (health/session/worktree/notify); `nohup`/`pkill` are no longer
  daemon runtime prerequisites, since the job owns start/stop.
- `restartDaemons` (config change) forces a restart via `ensureDaemon(…, force=true)` even when the current daemon is
  healthy, because a config change must be picked up by a fresh process.

## Idle timeout (why re-landing is safe)

- **Reverted at microsandbox v0.7.8** because a detached job was counted in the aggregate the heartbeat exposed
  (`active_exec_sessions = sessions + detached_sessions`) and the idle monitor treated `active_exec_sessions > 0` as
  never-idle, so a quiet dockerd/serve job permanently suppressed `idle_timeout`.
- **Fixed in microsandbox v0.7.11 (#1923, "allow detached jobs to idle", fixes #1866).** The idle monitor no longer
  short-circuits on `active_exec_sessions > 0`; attached and detached exec sessions are counted separately host-side
  (`RelayIdleState`). A detached job's *existence* does not reset the idle timer, while new job output still does. The
  launcher therefore needs no idle-exempt marker or flag: it just launches the jobs.
- Verify on a KVM host: a quiet dockerd job lets the VM idle-stop; a busy job keeps it alive.

## Verification

- Unit tests drive the seam through the msb mocks (`MockSandbox`/`MockJob`) and assert the kill-by-command, crash, and
  timeout branches.
- `TestJobsRoundTrip` (`internal/sandbox/msb/jobs_integration_test.go`, `integration` tag) exercises the real adapters
  against a live VM. Live dockerd/daemon and the idle behaviour above are manual: a KVM host with a runner image
  containing dockerd and opencode.
