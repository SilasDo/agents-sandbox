package vm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

var daemonReadyTimeout = 60 * time.Second

var daemonPollInterval = 2 * time.Second

// daemonShellFunc is the test seam for sb.Shell, matching the ensureInstalled
// pattern in doctor.go. Tests override this; production code leaves the default.
var daemonShellFunc = func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error) {
	out, err := sb.Shell(ctx, command)
	if err != nil {
		return "", -1, err
	}
	return out.Stdout(), out.ExitCode(), nil
}

// ensureDaemon guarantees the agent's serve daemon is healthy inside the VM. It
// health checks via the provider's command inside the VM; if unhealthy, it
// kills any stale daemon process, starts a fresh one bound to the hostname
// appropriate for serveOnly mode, and polls until healthy or timeout. An agent
// without a daemon provider is a no-op.
func ensureDaemon(ctx context.Context, a agent.Agent, serveOnly bool, sb msb.Sandbox, ui termio.UI) error {
	provider, ok := agent.AsDaemonProvider(a)
	if !ok {
		return nil
	}

	if probe := probeDaemonHealth(ctx, sb, provider); probe.healthy {
		ui.Verbosef("%s daemon already healthy", a.Name())
		return nil
	} else if probe.detail != "" {
		ui.Verbosef("%s daemon not healthy: %s", a.Name(), probe.detail)
	}

	ui.Verbosef("starting %s serve daemon", a.Name())
	if _, _, err := daemonShellFunc(ctx, sb, provider.DaemonKillCmd()); err != nil {
		ui.Warnf("kill stale daemon failed (continuing): %v", err)
	}
	if _, _, err := daemonShellFunc(ctx, sb, provider.DaemonStartCmd(serveOnly)); err != nil {
		return fmt.Errorf("start %s serve: %w", a.Name(), err)
	}

	deadline := time.Now().Add(daemonReadyTimeout)
	var lastDetail string
	for {
		if probe := probeDaemonHealth(ctx, sb, provider); probe.healthy {
			ui.Verbosef("%s daemon is healthy", a.Name())
			return nil
		} else {
			lastDetail = probe.detail
		}
		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(daemonPollInterval):
		}
		ui.Verbosef("Polling for daemon health: %s", lastDetail)
	}
	return fmt.Errorf("%s daemon did not become healthy within %s: %s%s",
		a.Name(), daemonReadyTimeout, lastDetail, daemonLogTail(ctx, sb, provider))
}

// daemonProbe is the outcome of a single health probe, carrying enough detail
// (exit code, parse error, or raw body) to diagnose a daemon that never becomes
// healthy.
type daemonProbe struct {
	healthy bool
	detail  string
}

// probeDaemonHealth runs the provider's health command once and classifies the
// result. A healthy probe carries no detail; an unhealthy one carries a
// human-readable explanation for verbose logging.
func probeDaemonHealth(ctx context.Context, sb msb.Sandbox, provider agent.DaemonProvider) daemonProbe {
	stdout, exitCode, err := daemonShellFunc(ctx, sb, provider.DaemonHealthCmd())
	if err != nil {
		return daemonProbe{healthy: false, detail: fmt.Sprintf("health probe failed: %v", err)}
	}
	if exitCode != 0 {
		return daemonProbe{healthy: false, detail: fmt.Sprintf("health probe exit %d: %s", exitCode, probeBody(stdout))}
	}
	healthy, err := provider.DaemonHealthParse(stdout)
	if err != nil {
		return daemonProbe{
			healthy: false,
			detail:  fmt.Sprintf("health response unparsable: %v: %s", err, probeBody(stdout)),
		}
	}
	if !healthy {
		return daemonProbe{healthy: false, detail: fmt.Sprintf("server reported unhealthy: %s", probeBody(stdout))}
	}
	return daemonProbe{healthy: true, detail: ""}
}

// probeBody renders a health probe's stdout for logging, flagging an empty body
// (e.g. no response, or a response with an empty body).
func probeBody(stdout string) string {
	if body := strings.TrimSpace(stdout); body != "" {
		return body
	}
	return "(no output)"
}

// daemonLogTail reads the daemon log's last lines to explain a health timeout.
// It is best-effort: a missing log, a failed tail, or empty output contributes
// nothing.
func daemonLogTail(ctx context.Context, sb msb.Sandbox, provider agent.DaemonProvider) string {
	path := provider.DaemonLogPath()
	if path == "" {
		return ""
	}
	stdout, exitCode, err := daemonShellFunc(ctx, sb, "tail -n 40 "+path)
	if err != nil || exitCode != 0 {
		return ""
	}
	if body := strings.TrimSpace(stdout); body != "" {
		return fmt.Sprintf("\n--- %s (last 40 lines) ---\n%s", path, body)
	}
	return ""
}

// SetDaemonShellFunc replaces the daemonShellFunc factory used by ensureDaemon
// with one that returns the provided function. The original factory is
// returned so callers can restore it after their test.
//
// Usage from an external test package:
//
//	orig := sandbox.SetDaemonShellFunc(func(ctx context.Context, sb sandbox.msb.Sandbox, command string) (string, int, error) {
//	    return "", 0, nil
//	})
//	t.Cleanup(func() { sandbox.SetDaemonShellFunc(orig) })
//
//deadcode:ignore reason="test seam replacing the daemon shell"
func SetDaemonShellFunc(
	f func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error),
) func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error) {
	orig := daemonShellFunc
	daemonShellFunc = f
	return orig
}
