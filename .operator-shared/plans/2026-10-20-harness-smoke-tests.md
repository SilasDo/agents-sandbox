# Harness Smoke Tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `msb`-tagged suite that starts every registered harness in a real microsandbox VM and proves it reaches its TUI, with hard isolation from the developer's existing agents-sandbox state.

**Architecture:** A new `test/harness` package. Untagged files hold the inert core (harness table, environment allowlist, output helpers, and a pty driver) with fast unit tests that run in `make test`; one `//go:build msb` file drives the real CLI under a pty in an isolated temp home. The suite is host-only (`make test-msb`) but always compile-checked (`make compile-all`).

**Tech Stack:** Go 1.26, `creack/pty`, `os/exec`, the existing `internal/agent` registry, and the built `agents-sandbox` binary.

**Spec:** `.operator-shared/specs/harness-smoke-tests.md`

## Global Constraints

- Build tag `msb`; package path `test/harness`.
- Untagged files must never touch host state (no real config/home/docker/msb resolution). `TestMain` installs the fail-fast seam mocks and does nothing else.
- The child process env is an allowlist (see Task 1); never inherit `HOME`, `XDG_*`, `AGENTS_SANDBOX_*`, `OPENCODE_SANDBOX_*`, or `MSB_*`.
- Default VM network is `none`.
- Format/lint only via `make fmt` and `make lint` (golangci-lint); never `go fmt`/`go vet` directly. Run `golangci-lint cache clean` before linting in the worktree.
- Run `go mod tidy` after adding the dependency.
- Conventional commit messages; `make check` must pass at the end.

## Review Focus

- **Inherited environment leakage.** If the child inherits the developer's `HOME`/`XDG_*`/`AGENTS_SANDBOX_*`, the run becomes non-deterministic and can write real config. Pinned by `TestIsolatedEnvAllowlist` (Task 1).
- **Table drift.** A registered harness with no table entry would be skipped silently. Pinned by `TestHarnessSpecsCoverEveryRegisteredAgent` (Task 1).
- **`compile-all` inertness.** Any host work in `TestMain`/package init breaks `-run=^$` on CI. Pinned by the `make compile-all` step (Task 3).
- **TUI-start detection correctness.** Alt-screen detection must fire on the enter sequence and not on its absence. Pinned by `TestTUIStarted` and `TestStripANSI` (Task 1).
- **Quit that never quits.** A wrong quit key must fail the subtest, not hang it. Pinned by the escalation-fails rule and `TestPTYRoundTrip` (Task 2, Task 3).

---

### Task 1: Inert core — table, env allowlist, output helpers

**Files:**
- Create: `test/harness/harness.go`
- Create: `test/harness/main_test.go`
- Create: `test/harness/harness_test.go`

**Interfaces:**
- Produces:
  - `type harnessSpec struct { QuitKeys []byte; Network string }`
  - `var harnessSpecs map[string]harnessSpec` (keys `claude-code`, `opencode`, `opencode2`, `pi`)
  - `func isolatedEnv(root, projectDir string, inherited []string) []string`
  - `func stripANSI(b []byte) string`
  - `func tuiStarted(output []byte) bool`
  - `const daemonTimeoutMarker = "did not become healthy"`

- [ ] **Step 1: Write the failing tests**

`test/harness/harness_test.go`:

```go
package harness

import (
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestHarnessSpecsCoverEveryRegisteredAgent(t *testing.T) {
	names := agent.Names()
	if len(names) == 0 {
		t.Fatal("no agents registered")
	}
	for _, name := range names {
		if _, ok := harnessSpecs[name]; !ok {
			t.Errorf("registered harness %q has no smoke-test spec; add a quit key", name)
		}
	}
	for name := range harnessSpecs {
		if _, ok := agent.Lookup(name); !ok {
			t.Errorf("smoke-test spec %q matches no registered harness", name)
		}
	}
}

func TestHarnessSpecsQuitKeysNonEmpty(t *testing.T) {
	for name, spec := range harnessSpecs {
		if len(spec.QuitKeys) == 0 {
			t.Errorf("harness %q has empty quit keys", name)
		}
	}
}

func TestIsolatedEnvAllowlist(t *testing.T) {
	inherited := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/real/home",
		"XDG_DATA_HOME=/real/data",
		"AGENTS_SANDBOX_NETWORK_PROFILE=public",
		"OPENCODE_SANDBOX_NOTIFY=on",
		"MSB_HOME=/real/msb",
		"SECRET_TOKEN=leak",
	}
	env := isolatedEnv("/root", "/root/projects/opencode", inherited)
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	want := map[string]string{
		"PATH":            "/usr/bin:/bin",
		"HOME":            "/root/home",
		"XDG_CONFIG_HOME": "/root/config",
		"XDG_DATA_HOME":   "/root/data",
		"XDG_STATE_HOME":  "/root/state",
		"XDG_CACHE_HOME":  "/root/cache",
		"TMPDIR":          "/root/tmp",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{
		"AGENTS_SANDBOX_NETWORK_PROFILE",
		"OPENCODE_SANDBOX_NOTIFY",
		"MSB_HOME",
		"SECRET_TOKEN",
	} {
		if _, ok := got[k]; ok {
			t.Errorf("%s must not be inherited (got %q)", k, got[k])
		}
	}
}

func TestStripANSI(t *testing.T) {
	if got := stripANSI([]byte("\x1b[31mred\x1b[0m")); got != "red" {
		t.Errorf("stripANSI = %q, want %q", got, "red")
	}
}

func TestTUIStarted(t *testing.T) {
	if !tuiStarted([]byte("launcher output\n\x1b[?1049htui")) {
		t.Error("tuiStarted = false, want true for alternate-screen enter sequence")
	}
	if tuiStarted([]byte("launcher output\nno tui yet")) {
		t.Error("tuiStarted = true, want false without the sequence")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./test/harness/...`
Expected: FAIL — undefined `harnessSpecs`, `isolatedEnv`, `stripANSI`, `tuiStarted`.

- [ ] **Step 3: Implement `test/harness/harness.go`**

Package doc plus the table and helpers. `isolatedEnv` builds a fresh slice from an allowlist (`PATH`, `TERM` defaulting to `xterm-256color`, `LANG`, `LC_ALL`, `LC_CTYPE`, `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` and their lowercase forms) read out of `inherited`, then appends the fixed `HOME`/`XDG_*`/`TMPDIR` values rooted at `root`; nothing else is copied. `stripANSI` removes `ESC [ … final-byte` sequences. `tuiStarted` reports whether the accumulated output contains the alternate-screen enter sequence `\x1b[?1049h`. Every entry in `harnessSpecs` uses `QuitKeys: []byte{0x03}`.

Signatures exactly as in **Interfaces** above.

- [ ] **Step 4: Implement `test/harness/main_test.go`**

```go
package harness

import (
	"testing"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

// TestMain installs the fail-fast seam mocks and nothing else: under
// `compile-all`'s -run=^$, `m.Run()` returns immediately, so the package stays
// inert without a VM.
func TestMain(m *testing.M) {
	mocks.InitFailFastMocks(m)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./test/harness/...`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

```bash
golangci-lint cache clean && make lint
git add test/harness/harness.go test/harness/main_test.go test/harness/harness_test.go
git commit -m "test(harness): add inert core for harness smoke tests"
```

---

### Task 2: Pty driver

**Files:**
- Create: `test/harness/pty.go`
- Create: `test/harness/pty_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `tuiStarted`, `stripANSI` (Task 1).
- Produces:
  - `type ptySession struct { /* unexported: cmd, file, buffer */ }`
  - `func startUnderPTY(t *testing.T, dir string, env []string, argv ...string) *ptySession`
  - `func (s *ptySession) waitForTUI(timeout time.Duration) error`
  - `func (s *ptySession) write(data []byte) error`
  - `func (s *ptySession) waitExit(timeout time.Duration) (int, error)`
  - `func (s *ptySession) output() []byte`
  - `func (s *ptySession) close()`

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/creack/pty@latest && go mod tidy`
Expected: `github.com/creack/pty` appears in `go.mod`.

- [ ] **Step 2: Write the failing test**

`test/harness/pty_test.go`:

```go
package harness

import (
	"testing"
	"time"
)

func TestPTYRoundTrip(t *testing.T) {
	// Enter the alternate screen, then wait for one byte before exiting with 0.
	ses := startUnderPTY(t, "", nil, "/bin/sh", "-c", `printf '\033[?1049h'; read x; exit 0`)
	defer ses.close()

	if err := ses.waitForTUI(5 * time.Second); err != nil {
		t.Fatalf("waitForTUI: %v", err)
	}
	if err := ses.write([]byte("\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, err := ses.waitExit(5 * time.Second)
	if err != nil {
		t.Fatalf("waitExit: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./test/harness/... -run TestPTYRoundTrip`
Expected: FAIL — undefined `startUnderPTY`.

- [ ] **Step 4: Implement `test/harness/pty.go`**

`startUnderPTY` uses `pty.Start(exec.Command(argv[0], argv[1:]...))` after setting `cmd.Dir` and `cmd.Env`, then starts a goroutine that reads the master into a mutex-guarded buffer and closes a `done` channel on read error/EOF. `waitForTUI` polls `tuiStarted(s.output())` every 50ms until `timeout` or the process exits. `waitExit` waits on the process with a timer, returning the exit code (`0` on `*exec.ExitError` with code 0, the code otherwise). `write` writes to the master. `close` kills the process group (`syscall.Kill(-pgid, SIGKILL)`) if still running and closes the master; it is idempotent. `t.Cleanup(ses.close)` is registered inside `startUnderPTY` too.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./test/harness/... -run TestPTYRoundTrip`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

```bash
golangci-lint cache clean && make lint
git add go.mod go.sum test/harness/pty.go test/harness/pty_test.go
git commit -m "test(harness): add pty driver for driving a harness TUI"
```

---

### Task 3: The `msb`-tagged smoke test and Makefile targets

**Files:**
- Create: `test/harness/smoke_msb_test.go`
- Modify: `Makefile` (`test-msb` target; `compile-all` tags and comment)

**Interfaces:**
- Consumes: `harnessSpecs`, `isolatedEnv`, `stripANSI`, `daemonTimeoutMarker` (Task 1); `startUnderPTY` and its methods (Task 2); `agent.Names()`.
- Produces: `func TestHarnessSmoke(t *testing.T)`.

- [ ] **Step 1: Write the smoke test**

`test/harness/smoke_msb_test.go` (`//go:build msb`). Structure:

```go
//go:build msb

package harness

const (
	tuiReadyTimeout = 120 * time.Second
	exitTimeout     = 20 * time.Second
	prepareTimeout  = 30 * time.Minute
)

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
			network = "none"
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
```

Implement the helpers in the same file:

- `preflight(t *testing.T)`: `runtime.GOOS` `linux` requires `/dev/kvm` (`os.Stat`), `darwin` is allowed, anything else skips; then `exec.Command("docker", "info").Run()` must succeed; each failure is `t.Skipf`.
- `prepare(t *testing.T) (root, binary string)`: guarded by a package-level `sync.Once`; `os.MkdirTemp("", "harness-smoke-")`, `os.MkdirAll(filepath.Join(root, "tmp"), 0o755)`, then `go build -o <root>/agents-sandbox github.com/inoio/agents-sandbox/cmd/agents-sandbox` with a `prepareTimeout`. Errors are stored for all callers and reported per test.
- `cleanupSandboxes(t *testing.T, root, binary string)`: with `isolatedEnv(root, root, os.Environ())`, run `<binary> sandbox list -q`, then `<binary> sandbox remove --force --yes <name>` for each non-empty line (`--yes` is required or the non-interactive remove fails on its confirmation); then `os.RemoveAll(root)`. Best-effort: log, never fail.

- [ ] **Step 2: Verify it compiles (no VM)**

Run: `make compile-all`
Expected: exits 0; `test/harness` is compiled with `-tags 'integration msb'` and no test runs.

- [ ] **Step 3: Update the Makefile**

Add, next to `test-integration`:

```make
# Harness smoke tests: start every registered harness to its TUI in a real
# microsandbox VM. Host-only (needs /dev/kvm, a docker daemon, and network);
# excluded from test/check via the `msb` build tag.
test-msb:
	CGO_ENABLED=1 go test -tags msb -timeout 60m -count=1 ./test/harness/...
```

Change `compile-all`'s tag list and comment to include `msb`:

```make
# Compile every test binary including build-tagged files (`//go:build
# integration`, `//go:build msb`) so a regression that only breaks tagged code
# is caught without a docker daemon or VM. `-run=^$` matches no test.
compile-all:
	CGO_ENABLED=1 go test -tags 'integration msb' -run='^$$' ./...
```

- [ ] **Step 4: Lint and commit**

```bash
golangci-lint cache clean && make lint && make compile-all
git add test/harness/smoke_msb_test.go Makefile
git commit -m "test(harness): add msb-tagged harness smoke test and make targets"
```

Note: the real acceptance run (`make test-msb`) needs a KVM host; it is run manually by the user, not in CI.

---

### Task 4: Docs, changelog, and brain

**Files:**
- Modify: `CHANGELOG.md` (`[Unreleased]`)
- Modify: `AGENTS.md` (Development commands)
- Modify: `.operator-shared/catalog.md` (spec + plan entries)
- Modify: `.operator-shared/index/index.md` (`test/` entry)

- [ ] **Step 1: Changelog**

Add one line under `[Unreleased]` describing the new `msb`-tagged harness smoke tests and `make test-msb`.

- [ ] **Step 2: AGENTS.md**

Add `make test-msb` to the Development commands list with a one-line note that it is host-only (needs KVM) and tagged `msb`.

- [ ] **Step 3: Brain catalog and index**

Add a `harness-smoke-tests.md` entry to the Shared Partition Catalog `specs/` section (Description + Read If, mirroring the spec's frontmatter) and the plan entry under `plans/`. Add a `test/` section to `.operator-shared/index/index.md` documenting `test/harness/`.

- [ ] **Step 4: Final format, check, commit**

```bash
make fmt && make check
git add CHANGELOG.md AGENTS.md .operator-shared/catalog.md .operator-shared/index/index.md
git commit -m "docs(harness): document harness smoke tests and targets"
```
