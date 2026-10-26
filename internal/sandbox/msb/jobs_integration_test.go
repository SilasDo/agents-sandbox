//go:build integration

package msb

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// TestJobsRoundTrip exercises the detached-jobs seam against a real
// microsandbox VM: it launches a runtime-owned job, then verifies Inspect,
// ListJobs, Logs, GetJob, and Kill via the realSandbox/realJob adapters.
//
// It is excluded from the main suite via the `integration` build tag (run with
// `make test-integration`) because it requires a working KVM host and network
// access to pull the base image.
func TestJobsRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const (
		sandboxName = "agents-sandbox-jobs-integration"
		sentinel    = "jobs-sentinel"
	)

	sb, err := msbSdk.CreateSandbox(
		ctx,
		sandboxName,
		msbSdk.WithImage("alpine:3.20"),
		msbSdk.WithReplace(),
		msbSdk.WithDetached(),
	)
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if handle, handleErr := msbSdk.GetSandbox(cleanupCtx, sandboxName); handleErr == nil {
			_ = handle.Remove(cleanupCtx)
		}
	})

	adapter := realSandbox{sandbox: sb}

	job, err := adapter.ExecDetached(ctx, "sh", []string{"-c", "echo " + sentinel + "; sleep 30"})
	if err != nil {
		t.Fatalf("ExecDetached: %v", err)
	}
	if job.ID() == "" {
		t.Fatal("ExecDetached returned an empty job id")
	}
	t.Cleanup(func() { _ = job.Close() })

	if _, inspectErr := job.Inspect(ctx); inspectErr != nil {
		t.Fatalf("Inspect: %v", inspectErr)
	}

	infos, err := adapter.ListJobs(ctx, msbSdk.WithAllJobs())
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	info := jobInfoByID(infos, job.ID())
	if info == nil {
		t.Fatalf("ListJobs did not contain job %s: %+v", job.ID(), infos)
	}
	// killJobsByCommand matches JobInfo.Command as a prefix of [binary, args...],
	// so the reported shape is load-bearing: if the runtime rewrote it (resolved
	// argv[0], joined string), stale jobs would never be killed on restart.
	wantCommand := []string{"sh", "-c", "echo " + sentinel + "; sleep 30"}
	if len(info.Command) < len(wantCommand) || !slices.Equal(info.Command[:len(wantCommand)], wantCommand) {
		t.Fatalf("JobInfo.Command = %q, want prefix %q", info.Command, wantCommand)
	}

	got, getErr := adapter.GetJob(ctx, job.ID())
	if getErr != nil {
		t.Fatalf("GetJob(%s): %v", job.ID(), getErr)
	}
	t.Cleanup(func() { _ = got.Close() })
	if got.ID() != job.ID() {
		t.Fatalf("GetJob(%s) = (%v, %v), want the same job", job.ID(), got, getErr)
	}

	awaitLogs(t, ctx, job, sentinel)

	if err := job.Kill(ctx); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	awaitExit(t, ctx, job)
}

func jobInfoByID(infos []msbSdk.JobInfo, id string) *msbSdk.JobInfo {
	for i := range infos {
		if infos[i].ID == id {
			return &infos[i]
		}
	}
	return nil
}

func awaitLogs(t *testing.T, ctx context.Context, job Job, sentinel string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		entries, err := job.Logs(ctx, msbSdk.JobLogOptions{})
		if err != nil {
			t.Fatalf("Logs: %v", err)
		}
		var builder strings.Builder
		for _, entry := range entries {
			builder.Write(entry.Data)
		}
		if strings.Contains(builder.String(), sentinel) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job logs never contained %q: %q", sentinel, builder.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func awaitExit(t *testing.T, ctx context.Context, job Job) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		info, err := job.Inspect(ctx)
		if err != nil {
			t.Fatalf("Inspect after kill: %v", err)
		}
		if info != nil && info.ExitCode != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not exit after kill: %+v", info)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
