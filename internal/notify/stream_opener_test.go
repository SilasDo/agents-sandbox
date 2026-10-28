package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// stubStreams implements StreamOpener and nothing else: no msb.Sandbox is
// behind it, which is the point of the seam. The two operations the watcher
// uses are enough to drive it.
type stubStreams struct {
	handle msb.StreamHandle
}

func (s stubStreams) ShellStream(context.Context, string) (msb.StreamHandle, error) {
	return s.handle, nil
}

func (s stubStreams) Shell(context.Context, string) (msb.ShellResult, error) {
	return nil, errors.New("stubStreams.Shell not scripted")
}

func TestSandboxStreamOpenerDelegatesToSandbox(t *testing.T) {
	handle := &streamStub{}
	seed := msb.NewTestResult(true, 0, `{"data":[]}`, "", nil)
	commands := &[]string{}
	sb := &msb.MockSandbox{
		StreamHandle_:   handle,
		ShellStreamCmds: commands,
		ShellOut:        map[string]msb.ShellResult{"seed-cmd": seed},
		ShellCalls:      &[]string{},
	}
	streams := SandboxStreamOpener{Sandbox: sb}

	got, err := streams.ShellStream(context.Background(), "stream-cmd")
	if err != nil || got != handle {
		t.Fatalf("ShellStream() = %v, %v, want the sandbox handle", got, err)
	}
	result, err := streams.Shell(context.Background(), "seed-cmd")
	if err != nil || result != seed {
		t.Fatalf("Shell() = %v, %v, want the sandbox result", result, err)
	}
	if len(*commands) != 1 || (*commands)[0] != "stream-cmd" {
		t.Errorf("ShellStream command = %v, want stream-cmd", *commands)
	}
	if len(*sb.ShellCalls) != 1 || (*sb.ShellCalls)[0] != "seed-cmd" {
		t.Errorf("Shell command = %v, want seed-cmd", *sb.ShellCalls)
	}
}

func TestSandboxStreamOpenerPropagatesErrors(t *testing.T) {
	boom := errors.New("boom")
	sb := &msb.MockSandbox{
		ShellErr:      boom,
		ShellCalls:    &[]string{},
		ShellStreamFn: func(string) (msb.StreamHandle, error) { return nil, boom },
	}
	streams := SandboxStreamOpener{Sandbox: sb}

	if _, err := streams.ShellStream(context.Background(), "stream-cmd"); !errors.Is(err, boom) {
		t.Errorf("ShellStream() error = %v, want %v", err, boom)
	}
	if _, err := streams.Shell(context.Background(), "seed-cmd"); !errors.Is(err, boom) {
		t.Errorf("Shell() error = %v, want %v", err, boom)
	}
}
