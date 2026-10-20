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
	// full-screen TUI. The image prebuild pays the one-off microsandbox
	// runtime install, so this only has to absorb the VM start; the budget
	// stays generous for a cold host.
	tuiReadyTimeout = 180 * time.Second
	// exitTimeout bounds how long a harness may take to exit once its quit
	// keys are written.
	exitTimeout = 20 * time.Second
	// prepareTimeout bounds the one-off launcher build and the per-harness
	// runner-image prebuild every subtest shares.
	prepareTimeout = 30 * time.Minute
	// probeTimeout bounds a preflight or cleanup subprocess so a wedged docker
	// socket cannot hang the suite.
	probeTimeout = 30 * time.Second

	// kvmDevice is the Linux virtualization device microsandbox needs.
	kvmDevice = "/dev/kvm"
	// launcherPackage is the command the suite compiles and drives.
	launcherPackage = "github.com/inoio/agents-sandbox/cmd/agents-sandbox"
	// networkNone is the secure default network policy for a smoke run.
	networkNone = "none"
)

// The suite creates one shared root, builds the launcher into it once, and
// prebuilds every harness image once. Each phase is guarded by its own
// sync.Once; the stored error is reported by every caller so a failed phase
// fails each harness instead of being silently retried.
var (
	prepareOnce     sync.Once
	prebuildOnce    sync.Once
	smokeRoot       string
	smokeBinary     string
	errSmokePrepare error
	errPrebuild     error
)

// TestHarnessSmoke starts every registered harness the way a user does
// (`agents-sandbox run --agent <name>`) inside a real microsandbox VM and
// asserts it reaches its full-screen TUI, exits 0 on its quit keys, and never
// reports a daemon timeout. It requires KVM, a docker daemon, and network; the
// `msb` build tag keeps it out of the default test run.
//
// The checks run in order so an environment that cannot run the suite skips
// before paying for it: the platform/`/dev/kvm` preflight first (no build), then
// the shared root and binary, then the isolated-env docker preflight, then the
// image prebuild (the first docker-dependent step), then the harnesses.
func TestHarnessSmoke(t *testing.T) {
	preflight(t)
	root, binary := prepare(t)
	preflightDocker(t, root)
	prebuildImages(t, root)
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
			ses := startUnderPTY(
				t, projectDir(root, name),
				isolatedEnv(root, os.Environ()),
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
			if sawDaemonTimeout(ses.output()) {
				t.Fatalf("harness %s reported a daemon timeout:\n%s", name, stripANSI(ses.output()))
			}
		})
	}
}

// preflight skips the suite when the host cannot run it at all: an unsupported
// platform or Linux without KVM. It never fails, needs no root, and runs before
// anything is built, so a host that cannot run the suite pays nothing.
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
}

// preflightDocker skips the suite when no docker daemon is reachable. The probe
// runs under the same isolated environment the child gets, so it can never
// certify a daemon the child cannot reach — a docker context the child does not
// inherit, or a HOME the child does not share, would otherwise pass here and
// fail every harness. It never fails.
func preflightDocker(t *testing.T, root string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	info := exec.CommandContext(ctx, "docker", "info")
	info.Env = isolatedEnv(root, os.Environ())
	if out, err := info.CombinedOutput(); err != nil {
		t.Skipf("harness smoke tests need a reachable docker daemon: %v\n%s", err, out)
	}
}

// prepare creates the shared temp root and compiles the launcher into it once
// for the whole suite, and returns both.
func prepare(t *testing.T) (root, binary string) {
	t.Helper()

	prepareOnce.Do(createRootAndBuildBinary)
	if errSmokePrepare != nil {
		t.Fatalf("prepare harness smoke suite: %v", errSmokePrepare)
	}
	return smokeRoot, smokeBinary
}

// projectDir is the throwaway project directory a harness runs in. It fixes the
// project slug the runner image is keyed by, so the prebuild's image matches
// the one the subtest's `run` loads.
func projectDir(root, name string) string {
	return filepath.Join(root, "projects", name)
}

// createRootAndBuildBinary creates the shared root and compiles the launcher
// into it.
func createRootAndBuildBinary() {
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

// prebuildImages builds every registered harness's runner image once, so each
// harness's TUI budget only has to absorb its VM start rather than a cold image
// build. It is the first docker-dependent step, so it runs only after
// preflightDocker has confirmed the child can reach a daemon.
func prebuildImages(t *testing.T, root string) {
	t.Helper()

	prebuildOnce.Do(func() { errPrebuild = buildHarnessImages(root) })
	if errPrebuild != nil {
		t.Fatalf("prebuild harness images: %v", errPrebuild)
	}
}

// buildHarnessImages builds each registered harness's runner image in the same
// throwaway project directory its subtest will run from, so the project slug
// and agent identity match. The isolated home pays for the microsandbox runtime
// install here, outside any harness's TUI budget.
func buildHarnessImages(root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()

	env := isolatedEnv(root, os.Environ())
	for _, name := range agent.Names() {
		dir := projectDir(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create project dir for %s: %w", name, err)
		}
		build := exec.CommandContext(ctx, smokeBinary, "build", "--agent", name)
		build.Dir = dir
		build.Env = env
		if out, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("prebuild image for %s: %w\n%s", name, err, out)
		}
	}
	return nil
}

// cleanupSandboxes best-effort destroys any sandbox left in the isolated home
// before removing the temp root, so a failed harness cannot leak a VM pointing
// at a deleted home. It logs failures and never fails the test.
func cleanupSandboxes(t *testing.T, root, binary string) {
	t.Helper()

	list, cancelList := boundedLauncher(root, binary, "sandbox", "list", "-q")
	out, err := list.Output()
	cancelList()
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
		remove, cancelRemove := boundedLauncher(root, binary, "sandbox", "remove", "--force", "--yes", name)
		removeOut, removeErr := remove.CombinedOutput()
		cancelRemove()
		if removeErr != nil {
			t.Logf("cleanup: remove sandbox %s: %v\n%s", name, removeErr, removeOut)
		}
	}
	if err := os.RemoveAll(root); err != nil {
		t.Logf("cleanup: remove temp root %s: %v", root, err)
	}
}

// boundedLauncher builds a launcher subcommand under the isolated environment
// with a bounded timeout, so a wedged socket cannot hang teardown. The caller
// must invoke the returned cancel once the command completes.
func boundedLauncher(root, binary string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = isolatedEnv(root, os.Environ())
	return cmd, cancel
}
