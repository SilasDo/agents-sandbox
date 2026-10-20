//go:build msb

package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
)

const (
	// tuiReadyTimeout bounds how long a harness may take to reach its
	// full-screen TUI; a cold run builds and loads a runner image first.
	tuiReadyTimeout = 120 * time.Second
	// exitTimeout bounds how long a harness may take to exit once its quit
	// keys are written.
	exitTimeout = 20 * time.Second
	// prepareTimeout bounds the one-off launcher build every subtest shares.
	prepareTimeout = 30 * time.Minute

	// kvmDevice is the Linux virtualization device microsandbox needs.
	kvmDevice = "/dev/kvm"
	// launcherPackage is the command the suite compiles and drives.
	launcherPackage = "github.com/inoio/agents-sandbox/cmd/agents-sandbox"
	// networkNone is the secure default network policy for a smoke run.
	networkNone = "none"
)

// The suite builds the launcher once and shares the result across subtests.
// prepareOnce guards the build; the stored error is reported by every caller so
// a failed build fails each harness instead of being silently retried.
var (
	prepareOnce     sync.Once
	smokeRoot       string
	smokeBinary     string
	errSmokePrepare error
)

// TestHarnessSmoke starts every registered harness the way a user does
// (`agents-sandbox run --agent <name>`) inside a real microsandbox VM and
// asserts it reaches its full-screen TUI, exits 0 on its quit keys, and never
// reports a daemon timeout. It requires KVM, a docker daemon, and network; the
// `msb` build tag keeps it out of the default test run.
func TestHarnessSmoke(t *testing.T) {
	preflight(t)
	root, binary := prepare(t)
	defer cleanupSandboxes(t, root, binary)

	for _, name := range agent.Names() {
		spec, ok := harnessSpecs[name]
		if !ok {
			t.Fatalf("harness %q has no smoke-test spec", name)
		}
		network := spec.Network
		if network == "" {
			network = networkNone
		}
		t.Run(name, func(t *testing.T) {
			projectDir := filepath.Join(root, "projects", name)
			if err := os.MkdirAll(projectDir, 0o755); err != nil {
				t.Fatal(err)
			}
			ses := startUnderPTY(
				t, projectDir,
				isolatedEnv(root, projectDir, os.Environ()),
				binary, "run", "--agent", name, "--network", network,
			)
			if err := ses.waitForTUI(tuiReadyTimeout); err != nil {
				t.Fatalf("harness %s did not reach its TUI: %v\n--- output ---\n%s", name, err, stripANSI(ses.output()))
			}
			if err := ses.write(spec.QuitKeys); err != nil {
				t.Fatalf("write quit keys: %v", err)
			}
			code, err := ses.waitExit(exitTimeout)
			if err != nil {
				t.Fatalf("harness %s did not exit after quit keys %v: %v", name, spec.QuitKeys, err)
			}
			if code != 0 {
				t.Fatalf("harness %s exited %d, want 0\n--- output ---\n%s", name, code, stripANSI(ses.output()))
			}
			if strings.Contains(stripANSI(ses.output()), daemonTimeoutMarker) {
				t.Fatalf("harness %s reported a daemon timeout:\n%s", name, stripANSI(ses.output()))
			}
		})
	}
}

// preflight skips the suite when the host cannot run it: an unsupported
// platform, Linux without KVM, or an unreachable docker daemon. It never fails.
func preflight(t *testing.T) {
	t.Helper()

	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat(kvmDevice); err != nil {
			t.Skipf("harness smoke tests need %s: %v", kvmDevice, err)
		}
	case "darwin":
		// Apple Silicon virtualization needs no host device.
	default:
		t.Skipf("harness smoke tests need linux or darwin, not %s", runtime.GOOS)
	}

	// A preflight `docker info` has no cancellation to attach.
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("harness smoke tests need a reachable docker daemon: %v", err)
	}
}

// prepare builds the launcher once for the whole suite and returns the shared
// temp root and the binary path inside it.
func prepare(t *testing.T) (root, binary string) {
	t.Helper()

	prepareOnce.Do(buildSmokeBinary)
	if errSmokePrepare != nil {
		t.Fatalf("prepare harness smoke suite: %v", errSmokePrepare)
	}
	return smokeRoot, smokeBinary
}

// buildSmokeBinary creates the shared root and compiles the launcher into it.
func buildSmokeBinary() {
	root, err := os.MkdirTemp("", "harness-smoke-")
	if err != nil {
		errSmokePrepare = fmt.Errorf("create temp root: %w", err)
		return
	}
	smokeRoot = root
	smokeBinary = filepath.Join(root, "agents-sandbox")
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
		errSmokePrepare = fmt.Errorf("create temp dir: %w", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()

	build := exec.CommandContext(ctx, "go", "build", "-o", smokeBinary, launcherPackage)
	if out, err := build.CombinedOutput(); err != nil {
		errSmokePrepare = fmt.Errorf("build launcher: %w\n%s", err, out)
	}
}

// cleanupSandboxes best-effort destroys any sandbox left in the isolated home
// before removing the temp root, so a failed harness cannot leak a VM pointing
// at a deleted home. It logs failures and never fails the test.
func cleanupSandboxes(t *testing.T, root, binary string) {
	t.Helper()

	env := isolatedEnv(root, root, os.Environ())
	list := exec.Command(binary, "sandbox", "list", "-q")
	list.Env = env
	out, err := list.Output()
	if err != nil {
		t.Logf("cleanup: list sandboxes: %v", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		// --yes is required: without it the non-interactive remove blocks on
		// its confirmation prompt and fails.
		remove := exec.Command(binary, "sandbox", "remove", "--force", "--yes", name)
		remove.Env = env
		if out, err := remove.CombinedOutput(); err != nil {
			t.Logf("cleanup: remove sandbox %s: %v\n%s", name, err, out)
		}
	}
	if err := os.RemoveAll(root); err != nil {
		t.Logf("cleanup: remove temp root %s: %v", root, err)
	}
}
