package vm

import (
	"context"
	"fmt"
	"strings"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// detachedLogTail is the number of retained log bytes requested for a failed
// detached job.
const detachedLogTail = 100

// jobExited reports whether a detached job's process has finished, returning
// its exit code. A nil exit code means the process is still running or its
// result is not yet known.
func jobExited(info *msbSdk.JobInfo) (int, bool) {
	if info == nil || info.ExitCode == nil {
		return 0, false
	}
	return *info.ExitCode, true
}

// jobTailLogs returns the retained log tail of a detached job for diagnostics,
// or an empty string when the logs are unavailable.
func jobTailLogs(ctx context.Context, job msb.Job) string {
	var options msbSdk.JobLogOptions
	options.Tail = new(uint)
	*options.Tail = detachedLogTail
	entries, err := job.Logs(ctx, options)
	if err != nil {
		return ""
	}
	var builder strings.Builder
	for _, entry := range entries {
		builder.Write(entry.Data)
	}
	return builder.String()
}

// commandMatches reports whether a job's recorded command starts with
// binary+args. Jobs carry no labels, so a detached process is rediscovered by
// its command line.
func commandMatches(command []string, binary string, args []string) bool {
	want := append([]string{binary}, args...)
	if len(command) < len(want) {
		return false
	}
	for i, expected := range want {
		if command[i] != expected {
			return false
		}
	}
	return true
}

// killJobsByCommand kills every job whose command matches binary+args. It is
// used to clear a stale dockerd/daemon before starting a fresh one; failures
// are reported as verbose diagnostics and never fatal.
func killJobsByCommand(
	ctx context.Context,
	sb msb.Sandbox,
	binary string,
	args []string,
	ui termio.UI,
) {
	infos, err := sb.ListJobs(ctx, msbSdk.WithAllJobs())
	if err != nil {
		ui.Verbosef("listing jobs to stop %s failed: %v", binary, err)
		return
	}
	for _, info := range infos {
		if !commandMatches(info.Command, binary, args) {
			continue
		}
		job, getErr := sb.GetJob(ctx, info.ID)
		if getErr != nil {
			ui.Verbosef("resolving job %s failed: %v", info.ID, getErr)
			continue
		}
		// Kill terminates the process; Close (handle-only) releases the native
		// handle regardless of whether the kill succeeded.
		killErr := job.Kill(ctx)
		if closeErr := job.Close(); closeErr != nil {
			ui.Verbosef("closing job %s handle failed: %v", info.ID, closeErr)
		}
		if killErr != nil {
			ui.Verbosef("killing job %s failed: %v", info.ID, killErr)
			continue
		}
		ui.Verbosef("killed stale job %s", info.ID)
	}
}

// awaitJobReady polls probe until it reports healthy. It fails fast when the
// job's process exits (a structured failure) and times out with the probe's
// last diagnostic detail and the job's log tail when probe never succeeds.
// onUnhealthy, when non-nil, is called with each unhealthy probe's detail for
// progress logging.
func awaitJobReady(
	ctx context.Context,
	job msb.Job,
	label string,
	probe func(context.Context) (healthy bool, detail string),
	onUnhealthy func(detail string),
	timeout, pollInterval time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	var lastDetail string
	for {
		if code, exited := jobExited(inspectJob(ctx, job)); exited {
			return fmt.Errorf(
				"%s exited before becoming ready (exit %d):\n%s",
				label,
				code,
				jobTailLogs(ctx, job),
			)
		}
		healthy, detail := probe(ctx)
		if healthy {
			return nil
		}
		lastDetail = detail
		if onUnhealthy != nil {
			onUnhealthy(detail)
		}
		if !time.Now().Before(deadline) {
			message := fmt.Sprintf("%s did not become ready within %s", label, timeout)
			if lastDetail != "" {
				message += ": " + lastDetail
			}
			return fmt.Errorf("%s:\n%s", message, jobTailLogs(ctx, job))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// inspectJob reads a job's info, returning nil when inspection fails so the
// readiness loop keeps polling rather than treating a transient error as death.
func inspectJob(ctx context.Context, job msb.Job) *msbSdk.JobInfo {
	info, err := job.Inspect(ctx)
	if err != nil {
		return nil
	}
	return info
}

// probeHealthy runs a health command inside the sandbox as the default user and
// reports whether it succeeded. When it fails it returns a diagnostic detail
// (the shell error, exit code, or output) so a readiness timeout is
// diagnosable instead of opaque.
func probeHealthy(ctx context.Context, sb msb.Sandbox, command string) (bool, string) {
	out, err := sb.Shell(ctx, command, msbSdk.WithExecUser(DefaultSandboxUser))
	if err != nil {
		return false, fmt.Sprintf("probe failed: %v", err)
	}
	if out == nil {
		return false, "probe returned no result"
	}
	if !out.Success() {
		body := strings.TrimSpace(out.Stderr())
		if body == "" {
			body = strings.TrimSpace(out.Stdout())
		}
		if body == "" {
			body = "(no output)"
		}
		return false, fmt.Sprintf("probe exit %d: %s", out.ExitCode(), body)
	}
	return true, ""
}
