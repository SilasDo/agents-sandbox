package harness

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func TestPTYWaitExitReportsNonZeroCode(t *testing.T) {
	ses := startUnderPTY(t, "", nil, "/bin/sh", "-c", "exit 3")
	defer ses.close()

	code, err := ses.waitExit(5 * time.Second)
	if err != nil {
		t.Fatalf("waitExit: %v", err)
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
}

func TestPTYWaitExitTimesOutWhileProcessRuns(t *testing.T) {
	ses := startUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	defer ses.close()

	if _, err := ses.waitExit(200 * time.Millisecond); err == nil {
		t.Fatal("waitExit = nil error, want a timeout error")
	}
}

func TestPTYWaitForTUIFailsWhenProcessExitsWithoutTUI(t *testing.T) {
	ses := startUnderPTY(t, "", nil, "/bin/sh", "-c", "echo launcher output; exit 0")
	defer ses.close()

	err := ses.waitForTUI(5 * time.Second)
	if err == nil {
		t.Fatal("waitForTUI = nil, want an error for a process that exited without a TUI")
	}
	if !strings.Contains(err.Error(), "no full-screen TUI") || !strings.Contains(err.Error(), "launcher output") {
		t.Errorf("waitForTUI error %q, want it to name the missing TUI and the output seen", err)
	}
}

func TestPTYWaitForTUITimesOutWhileProcessRuns(t *testing.T) {
	ses := startUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	defer ses.close()

	err := ses.waitForTUI(200 * time.Millisecond)
	if err == nil {
		t.Fatal("waitForTUI = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "no full-screen TUI within") {
		t.Errorf("waitForTUI error %q, want it to name the timeout", err)
	}
}

// TestPTYCloseKillsProcessGroup pins that close kills a still-running child
// (and its process group) and tolerates a second call.
func TestPTYCloseKillsProcessGroup(t *testing.T) {
	ses := startUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	ses.close()
	ses.close()

	code, err := ses.waitExit(5 * time.Second)
	if err != nil {
		t.Fatalf("waitExit after close: %v", err)
	}
	if code != -1 {
		t.Errorf("exit code = %d, want -1 for a process killed by a signal", code)
	}
}

// TestPTYCloseAfterExit pins that close takes the reaped path once the child
// has already been waited for (no group signal, still idempotent).
func TestPTYCloseAfterExit(t *testing.T) {
	ses := startUnderPTY(t, "", nil, "/bin/sh", "-c", "exit 0")
	if code, err := ses.waitExit(5 * time.Second); err != nil {
		t.Fatalf("waitExit: %v", err)
	} else if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	ses.close()
	ses.close()
}

// TestExitErrorCode pins the shared *exec.ExitError unwrapping used by the exit
// status, wait, and tail helpers.
func TestExitErrorCode(t *testing.T) {
	ses := startUnderPTY(t, "", nil, "/bin/sh", "-c", "exit 7")
	defer ses.close()
	if _, err := ses.waitExit(5 * time.Second); err != nil {
		t.Fatalf("waitExit: %v", err)
	}

	code, ok := exitErrorCode(ses.waitErr)
	if !ok || code != 7 {
		t.Errorf("exitErrorCode(exit 7 wait) = (%d, %t), want (7, true)", code, ok)
	}
	if _, ok := exitErrorCode(nil); ok {
		t.Error("exitErrorCode(nil) reported an exit error, want false")
	}
}

// TestPTYOutputTailCutsOnRuneBoundary pins that outputTail never splits a
// multi-byte rune at the byte-offset cut used to trim the tail.
func TestPTYOutputTailCutsOnRuneBoundary(t *testing.T) {
	ses := &ptySession{}
	// Three-byte runes sized so the cut lands one byte into a rune: a naive
	// byte slice would start mid-rune and yield invalid UTF-8.
	ses.buffer.WriteString(strings.Repeat("€", ptyOutputTailLimit/3+1))

	quoted := ses.outputTail()
	text, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatalf("Unquote(%s): %v", quoted, err)
	}
	if !utf8.ValidString(text) {
		t.Errorf("outputTail = %q, want valid UTF-8", text)
	}
}

// TestPTYHonoursWorkingDirectoryAndEnv pins that a non-empty dir and a
// non-nil env reach the child (and that the env replaces the inherited one).
func TestPTYHonoursWorkingDirectoryAndEnv(t *testing.T) {
	dir := t.TempDir()
	env := []string{"PATH=/usr/bin:/bin", "HARNESS_PTY_TEST=marker"}
	// Unset PWD so the shell's pwd builtin asks the kernel for the real
	// working directory instead of echoing the inherited one.
	ses := startUnderPTY(t, dir, env, "/bin/sh", "-c",
		`unset PWD; printf '\033[?1049h'; read x; pwd; echo "$HARNESS_PTY_TEST"; exit 0`)
	defer ses.close()

	if err := ses.waitForTUI(5 * time.Second); err != nil {
		t.Fatalf("waitForTUI: %v", err)
	}
	if err := ses.write([]byte("\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ses.waitExit(5 * time.Second); err != nil {
		t.Fatalf("waitExit: %v", err)
	}

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	for _, want := range []string{resolved, "marker"} {
		deadline := time.Now().Add(time.Second)
		for !strings.Contains(string(ses.output()), want) {
			if time.Now().After(deadline) {
				t.Fatalf("output %q does not contain %q", ses.output(), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
