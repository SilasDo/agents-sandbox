package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

const (
	// ptyPollInterval is how often waitForTUI re-inspects the captured output.
	ptyPollInterval = 50 * time.Millisecond
	// ptyDrainGrace bounds how long waitForTUI waits for the master read loop
	// to flush the output a process wrote just before exiting.
	ptyDrainGrace = 500 * time.Millisecond
	// ptyOutputTailLimit caps the output an error message quotes.
	ptyOutputTailLimit = 500
)

// ptySession drives one child process attached to a pseudo-terminal. The
// harness smoke test uses it to start the launcher the way a user does and to
// watch its output for the full-screen TUI.
type ptySession struct {
	cmd    *exec.Cmd
	master *os.File

	// cancel stops the child; close uses it to kill a session the test left
	// running.
	cancel context.CancelFunc

	mu     sync.Mutex
	buffer bytes.Buffer

	// waitDone is closed once cmd.Wait has returned and waitErr is final.
	waitDone chan struct{}
	waitErr  error

	// readDone is closed once the master read loop stopped (EOF or error).
	readDone chan struct{}

	closeOnce sync.Once
}

// startUnderPTY starts argv attached to a new pseudo-terminal and returns a
// session for driving it. An empty dir runs the child in the test's working
// directory; a nil env inherits the parent environment. Cleanup closes the
// session, so callers may defer close safely.
func startUnderPTY(t *testing.T, dir string, env []string, argv ...string) *ptySession {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	//nolint:gosec // G204: the caller names the binary to drive; no untrusted input reaches it.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
	}
	// Cancel the whole process group rather than just the direct child: a
	// harness can leave grandchildren holding the pty.
	cmd.Cancel = func() error { return killProcessGroup(cmd) }

	// pty.Start wires the child's stdio to the pty's slave side, puts it in a
	// new session (Setsid) and makes the pty its controlling terminal, which
	// is what lets the child's harness render full-screen. The new session
	// also makes the child a process-group leader, so its pid doubles as its
	// pgid and close can signal the whole group. Adding Setpgid on top would
	// fail with EPERM: setpgid rejects a session leader.
	master, err := pty.Start(cmd)
	if err != nil {
		cancel()
		t.Fatalf("start %v under pty: %v", argv, err)
	}

	ses := &ptySession{
		cmd:       cmd,
		master:    master,
		cancel:    cancel,
		mu:        sync.Mutex{},
		buffer:    bytes.Buffer{},
		waitDone:  make(chan struct{}),
		waitErr:   nil,
		readDone:  make(chan struct{}),
		closeOnce: sync.Once{},
	}
	go ses.readOutput()
	go ses.waitProcess()
	t.Cleanup(ses.close)
	return ses
}

// readOutput captures everything the child writes until the pty reports EOF or
// an error, which happens once every slave descriptor is closed.
func (s *ptySession) readOutput() {
	defer close(s.readDone)
	buf := make([]byte, 4096)
	for {
		n, err := s.master.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.buffer.Write(buf[:n])
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// waitProcess reaps the child exactly once; waitDone publishes the result.
func (s *ptySession) waitProcess() {
	s.waitErr = s.cmd.Wait()
	close(s.waitDone)
}

// output returns a copy of everything captured so far.
func (s *ptySession) output() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.buffer.Bytes())
}

// write sends data to the child's terminal.
func (s *ptySession) write(data []byte) error {
	_, err := s.master.Write(data)
	return err
}

// waitForTUI blocks until the child's output shows a full-screen TUI or the
// child exits, whichever comes first. The error names what was seen instead.
func (s *ptySession) waitForTUI(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if tuiStarted(s.output()) {
			return nil
		}
		select {
		case <-s.waitDone:
			s.drainOutput()
			if tuiStarted(s.output()) {
				return nil
			}
			return fmt.Errorf(
				"no full-screen TUI before the process exited (%s); output: %s",
				s.exitStatus(), s.outputTail())
		default:
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf(
				"no full-screen TUI within %s (process still running); output: %s",
				timeout, s.outputTail())
		}
		select {
		case <-time.After(ptyPollInterval):
		case <-s.waitDone:
		}
	}
}

// waitExit blocks until the child exits and returns its exit code. A process
// that exited successfully yields 0; one killed by a signal, -1. Only a wait
// that does not complete within the timeout (or fails outright) is an error.
func (s *ptySession) waitExit(timeout time.Duration) (int, error) {
	select {
	case <-s.waitDone:
	case <-time.After(timeout):
		return 0, fmt.Errorf("process did not exit within %s", timeout)
	}
	if s.waitErr != nil {
		if _, ok := errors.AsType[*exec.ExitError](s.waitErr); !ok {
			return 0, fmt.Errorf("wait for process: %w", s.waitErr)
		}
	}
	return exitCode(s.waitErr), nil
}

// close kills the child's process group if it is still running and closes the
// pty master. It is idempotent and safe to call from a cleanup.
func (s *ptySession) close() {
	s.closeOnce.Do(func() {
		// Only signal while the child is unreaped: once cmd.Wait has returned
		// its pid could belong to an unrelated process.
		select {
		case <-s.waitDone:
		default:
			s.cancel()
		}
		_ = s.master.Close()
	})
}

// killProcessGroup SIGKILLs the command's process group. The child runs in its
// own session, so its pid is also its pgid and the group covers any
// grandchildren. It reports os.ErrProcessDone, the "no needless error" signal
// exec.Cmd.Wait understands: the process is finished either way, and reporting
// a cancellation error would hide its real exit status.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// drainOutput waits for the read loop to flush output written just before exit.
func (s *ptySession) drainOutput() {
	select {
	case <-s.readDone:
	case <-time.After(ptyDrainGrace):
	}
}

// exitStatus names the child's exit for error messages.
func (s *ptySession) exitStatus() string {
	if s.waitErr == nil {
		return "exit code 0"
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](s.waitErr); ok {
		return fmt.Sprintf("exit code %d", exitErr.ExitCode())
	}
	return s.waitErr.Error()
}

// outputTail renders the tail of the captured output, without escape
// sequences, for an error message.
func (s *ptySession) outputTail() string {
	text := stripANSI(s.output())
	if len(text) > ptyOutputTailLimit {
		text = "…" + text[len(text)-ptyOutputTailLimit:]
	}
	return fmt.Sprintf("%q", text)
}

// exitCode maps the error from cmd.Wait to a process exit code.
func exitCode(waitErr error) int {
	if waitErr == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
		return exitErr.ExitCode()
	}
	return -1
}
