package vm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// fakeAgent implements only the core Agent interface with no capabilities, so
// the daemon/worktree paths must no-op for it.
type fakeAgent struct{}

func (fakeAgent) Name() string          { return "fake" }
func (fakeAgent) ConfigDirName() string { return "fake" }
func (fakeAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{}
}

func TestEnsureDaemonNoDaemonProvider(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellCalls: &[]string{}}
	a := &fakeAgent{}
	if err := ensureDaemon(context.Background(), a, false, sb, ui); err != nil {
		t.Fatalf("expected nil for an agent without a DaemonProvider, got %v", err)
	}
	if len(*sb.ShellCalls) != 0 {
		t.Errorf("expected no shell calls for an agent without a DaemonProvider, got %v", *sb.ShellCalls)
	}
}

// mockDaemonShell overrides daemonShellFunc for testutil. It returns queued
// (stdout, exitCode) pairs.
type mockDaemonShell struct {
	responses []mockShellResp
	calls     int
}

type mockShellResp struct {
	stdout   string
	exitCode int
	err      error
}

func (m *mockDaemonShell) run(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
	if m.calls >= len(m.responses) {
		return "", 0, nil
	}
	r := m.responses[m.calls]
	m.calls++
	return r.stdout, r.exitCode, r.err
}

func TestEnsureDaemonStartsWhenUnhealthy(t *testing.T) {
	testUI := termio.NewTestMock(t)
	mock := &mockDaemonShell{
		responses: []mockShellResp{
			// First healthcheck: unhealthy (daemon not running).
			{stdout: "", exitCode: 1},
			// Kill stale daemon (response ignored).
			{stdout: "", exitCode: 0},
			// Start command (response ignored).
			{stdout: "", exitCode: 0},
			// Poll: still starting.
			{stdout: "", exitCode: 1},
			// Poll again: healthy.
			{stdout: `{"healthy":true}`, exitCode: 0},
		},
	}
	prev := daemonShellFunc
	t.Cleanup(func() { daemonShellFunc = prev })
	daemonShellFunc = mock.run

	origPoll := daemonPollInterval
	t.Cleanup(func() { daemonPollInterval = origPoll })
	daemonPollInterval = 10 * time.Millisecond

	err := ensureDaemon(context.Background(), opencodeAgent(t), false, nil, &testUI)
	if err != nil {
		t.Fatalf("EnsureDaemon failed: %v", err)
	}
}

// TestEnsureDaemonStartsServeOnlyOnExternalInterface verifies that serve-only
// mode starts the opencode daemon bound to 0.0.0.0. Microsandbox's published-port
// forwarder dials the guest's external interface address, so a loopback-only
// (127.0.0.1) binding would make host clients get an empty reply.
func TestEnsureDaemonStartsServeOnlyOnExternalInterface(t *testing.T) {
	testUI := termio.NewTestMock(t)
	provider := opencodeProvider(t)
	var startCmd string
	healthChecks := 0
	prev := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if strings.Contains(command, "opencode serve") && !strings.Contains(command, "pkill") {
			startCmd = command
		}
		if command == provider.DaemonHealthCmd() {
			healthChecks++
			if healthChecks == 1 {
				return "", 1, nil
			}
			return `{"healthy":true}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonShellFunc(prev) })

	origPoll := daemonPollInterval
	t.Cleanup(func() { daemonPollInterval = origPoll })
	daemonPollInterval = 10 * time.Millisecond

	err := ensureDaemon(context.Background(), opencodeAgent(t), true, nil, &testUI)
	if err != nil {
		t.Fatalf("EnsureDaemon failed: %v", err)
	}
	if startCmd == "" || !strings.Contains(startCmd, "--hostname 0.0.0.0") {
		t.Errorf("serve-only daemon was not started with external-interface binding, got %q", startCmd)
	}
}

// TestEnsureDaemonProbesBeforeWaiting verifies that after starting the daemon
// the first health probe runs immediately rather than after a full poll
// interval; a long interval must not delay discovering an already-healthy
// daemon.
func TestEnsureDaemonProbesBeforeWaiting(t *testing.T) {
	testUI := termio.NewTestMock(t)
	provider := opencodeProvider(t)
	healthChecks := 0
	prev := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == provider.DaemonHealthCmd() {
			healthChecks++
			if healthChecks == 1 {
				return "", 1, nil
			}
			return `{"healthy":true}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonShellFunc(prev) })

	origTimeout, origInterval := daemonReadyTimeout, daemonPollInterval
	t.Cleanup(func() {
		daemonReadyTimeout = origTimeout
		daemonPollInterval = origInterval
	})
	daemonReadyTimeout = 2 * time.Second
	daemonPollInterval = 2 * time.Second

	start := time.Now()
	if err := ensureDaemon(context.Background(), opencodeAgent(t), false, nil, &testUI); err != nil {
		t.Fatalf("ensureDaemon: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Fatalf("ensureDaemon waited %v before its first health probe, want immediate", elapsed)
	}
}

// fakeDaemonProvider is a minimal DaemonProvider driving the health and log-tail
// helpers directly. Both real profiles always return a non-empty log path, so
// the empty-path guard in daemonLogTail needs a controllable double.
type fakeDaemonProvider struct {
	healthCmd string
	logPath   string
	parse     func(string) (bool, error)
}

func (fakeDaemonProvider) DaemonStartCmd(bool) string { return "" }
func (fakeDaemonProvider) DaemonKillCmd() string      { return "" }
func (f fakeDaemonProvider) DaemonHealthCmd() string  { return f.healthCmd }
func (f fakeDaemonProvider) DaemonHealthParse(stdout string) (bool, error) {
	return f.parse(stdout)
}
func (f fakeDaemonProvider) DaemonLogPath() string { return f.logPath }
func (fakeDaemonProvider) DaemonAuthHint() string  { return "" }

// TestProbeDaemonHealthReportsUnhealthyBody covers a parseable health response
// that reports the daemon as unhealthy: the raw body (non-empty here) is kept as
// the detail rather than the empty-body placeholder.
func TestProbeDaemonHealthReportsUnhealthyBody(t *testing.T) {
	prev := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return `{"healthy":false}`, 0, nil
	})
	t.Cleanup(func() { SetDaemonShellFunc(prev) })

	probe := probeDaemonHealth(context.Background(), nil, fakeDaemonProvider{
		healthCmd: "health",
		parse:     func(string) (bool, error) { return false, nil },
	})
	if probe.healthy {
		t.Fatal("probe reported healthy for a server-reported-unhealthy response")
	}
	if want := `server reported unhealthy: {"healthy":false}`; probe.detail != want {
		t.Errorf("detail = %q, want %q", probe.detail, want)
	}
}

// TestDaemonLogTail exercises the best-effort tail directly: an empty path skips
// the shell, and a failed, non-zero, or empty tail contributes nothing.
func TestDaemonLogTail(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		resp  mockShellResp
		calls int
		want  string
	}{
		{name: "empty path skips shell", path: "", calls: 0, want: ""},
		{
			name:  "shell error",
			path:  "/tmp/serve.log",
			resp:  mockShellResp{stdout: "x\n", err: errors.New("boom")},
			calls: 1,
			want:  "",
		},
		{
			name:  "non-zero exit",
			path:  "/tmp/serve.log",
			resp:  mockShellResp{stdout: "x\n", exitCode: 1},
			calls: 1,
			want:  "",
		},
		{name: "empty output", path: "/tmp/serve.log", resp: mockShellResp{stdout: "  \n"}, calls: 1, want: ""},
		{
			name:  "content",
			path:  "/tmp/serve.log",
			resp:  mockShellResp{stdout: "boom\n"},
			calls: 1,
			want:  "\n--- /tmp/serve.log (last 40 lines) ---\nboom",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			prev := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
				calls++
				if want := "tail -n 40 " + tc.path; command != want {
					t.Errorf("tail command = %q, want %q", command, want)
				}
				return tc.resp.stdout, tc.resp.exitCode, tc.resp.err
			})
			t.Cleanup(func() { SetDaemonShellFunc(prev) })

			got := daemonLogTail(context.Background(), nil, fakeDaemonProvider{logPath: tc.path})
			if got != tc.want {
				t.Errorf("daemonLogTail() = %q, want %q", got, tc.want)
			}
			if calls != tc.calls {
				t.Errorf("shell calls = %d, want %d", calls, tc.calls)
			}
		})
	}
}

func TestEnsureDaemonFailsAfterTimeout(t *testing.T) {
	testUI := termio.NewTestMock(t)
	mock := &mockDaemonShell{
		responses: []mockShellResp{
			// Always unhealthy.
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 0},
			{stdout: "", exitCode: 0},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
		},
	}
	prev := daemonShellFunc
	t.Cleanup(func() { daemonShellFunc = prev })
	daemonShellFunc = mock.run

	origTimeout, origInterval := daemonReadyTimeout, daemonPollInterval
	t.Cleanup(func() {
		daemonReadyTimeout = origTimeout
		daemonPollInterval = origInterval
	})
	daemonReadyTimeout = 10 * time.Millisecond
	daemonPollInterval = 1 * time.Millisecond

	err := ensureDaemon(context.Background(), opencodeAgent(t), false, nil, &testUI)
	if err == nil {
		t.Fatal("expected error after timeout")
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

// TestEnsureDaemonPollingReportsProbeDetail verifies the verbose poll line
// carries the health probe's outcome (exit code / body), so a daemon that never
// becomes healthy is diagnosable without guessing.
func TestEnsureDaemonPollingReportsProbeDetail(t *testing.T) {
	testUI := termio.NewTestMock(t)
	provider := opencodeProvider(t)
	prev := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == provider.DaemonHealthCmd() {
			return "", 22, nil // non-zero probe exit, e.g. connection refused (7)
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonShellFunc(prev) })

	origTimeout, origInterval := daemonReadyTimeout, daemonPollInterval
	t.Cleanup(func() { daemonReadyTimeout, daemonPollInterval = origTimeout, origInterval })
	daemonReadyTimeout = 5 * time.Millisecond
	daemonPollInterval = time.Millisecond

	err := ensureDaemon(context.Background(), opencodeAgent(t), false, nil, &testUI)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "health probe exit 22") {
		t.Errorf("timeout error missing probe detail: %v", err)
	}
	found := false
	for _, call := range testUI.VerboseCalls {
		if strings.Contains(call, "Polling for daemon health: health probe exit 22") {
			found = true
		}
	}
	if !found {
		t.Errorf("verbose polling missing probe context: %v", testUI.VerboseCalls)
	}
}

// TestEnsureDaemonTimeoutIncludesServeLog verifies that a health timeout tails
// the daemon's log (DaemonLogPath) so the reason the daemon stayed down (a bind
// failure, a crash) is surfaced.
func TestEnsureDaemonTimeoutIncludesServeLog(t *testing.T) {
	testUI := termio.NewTestMock(t)
	provider := opencodeProvider(t)
	tailCmd := "tail -n 40 " + provider.DaemonLogPath()
	prev := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		switch {
		case command == provider.DaemonHealthCmd():
			return "", 7, nil // connection refused: nothing listening
		case command == tailCmd:
			return "error: cannot bind to 127.0.0.1:4096", 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonShellFunc(prev) })

	origTimeout, origInterval := daemonReadyTimeout, daemonPollInterval
	t.Cleanup(func() { daemonReadyTimeout, daemonPollInterval = origTimeout, origInterval })
	daemonReadyTimeout = 5 * time.Millisecond
	daemonPollInterval = time.Millisecond

	err := ensureDaemon(context.Background(), opencodeAgent(t), false, nil, &testUI)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "error: cannot bind to 127.0.0.1:4096") {
		t.Errorf("timeout error missing serve log tail: %v", err)
	}
}
