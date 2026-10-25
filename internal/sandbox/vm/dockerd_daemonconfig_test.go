package vm

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// daemonJSONSandbox returns a MockSandbox whose guest has the given
// /etc/docker/daemon.json contents (omitted when content is empty).
func daemonJSONSandbox(content string) *msb.MockSandbox {
	files := map[string][]byte{}
	if content != "" {
		files[dockerdDaemonConfigPath] = []byte(content)
	}
	return &msb.MockSandbox{FSValue_: msb.NewTestFS(files, nil)}
}

func TestDockerdStartCommandDefaultsToVFSFlag(t *testing.T) {
	cmd, err := dockerdStartCommand(context.Background(), daemonJSONSandbox(""))
	if err != nil {
		t.Fatalf("dockerdStartCommand: %v", err)
	}
	if !strings.Contains(cmd, dockerdVFSFlag) {
		t.Errorf("expected the vfs flag when daemon.json is absent; got:\n%s", cmd)
	}
}

func TestDockerdStartCommandKeepsFlagForUnrelatedKeys(t *testing.T) {
	cmd, err := dockerdStartCommand(
		context.Background(),
		daemonJSONSandbox(`{"registry-mirrors":["https://mirror.example"]}`),
	)
	if err != nil {
		t.Fatalf("dockerdStartCommand: %v", err)
	}
	if !strings.Contains(cmd, dockerdVFSFlag) {
		t.Errorf("expected the vfs flag when daemon.json sets no storage driver; got:\n%s", cmd)
	}
}

// TestDockerdStartCommandDropsFlagWhenDaemonJSONSetsVFS guards #287: when the
// image already forces vfs in daemon.json, passing --storage-driver=vfs as well
// makes dockerd refuse to start ("specified both as a flag and in the
// configuration file").
func TestDockerdStartCommandDropsFlagWhenDaemonJSONSetsVFS(t *testing.T) {
	cmd, err := dockerdStartCommand(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":"vfs"}`),
	)
	if err != nil {
		t.Fatalf("dockerdStartCommand: %v", err)
	}
	if strings.Contains(cmd, "--storage-driver") {
		t.Errorf("must not pass the flag when daemon.json already sets vfs; got:\n%s", cmd)
	}
	if !strings.Contains(cmd, dockerdStartPrefix) {
		t.Errorf("expected dockerd to still be started; got:\n%s", cmd)
	}
}

func TestDockerdStartCommandFailsOnOtherStorageDriver(t *testing.T) {
	_, err := dockerdStartCommand(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":"overlay2"}`),
	)
	if err == nil {
		t.Fatal("expected an error when daemon.json requests a non-vfs driver")
	}
	if !strings.Contains(err.Error(), "overlay2") || !strings.Contains(err.Error(), vfsStorageDriver) {
		t.Errorf("error should name the offending and required drivers, got: %v", err)
	}
}

func TestDockerdStartCommandMalformedDaemonJSONFallsBackToFlag(t *testing.T) {
	cmd, err := dockerdStartCommand(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":`),
	)
	if err != nil {
		t.Fatalf("dockerdStartCommand: %v", err)
	}
	if !strings.Contains(cmd, dockerdVFSFlag) {
		t.Errorf("expected the vfs flag when daemon.json cannot be parsed; got:\n%s", cmd)
	}
}

func TestDockerdStartCommandNullStorageDriverFallsBackToFlag(t *testing.T) {
	cmd, err := dockerdStartCommand(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":null}`),
	)
	if err != nil {
		t.Fatalf("dockerdStartCommand: %v", err)
	}
	if !strings.Contains(cmd, dockerdVFSFlag) {
		t.Errorf("expected the vfs flag when storage-driver is null; got:\n%s", cmd)
	}
}

func TestDockerdStartCommandNonStringStorageDriverFails(t *testing.T) {
	_, err := dockerdStartCommand(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":42}`),
	)
	if err == nil {
		t.Fatal("expected an error for a non-string storage-driver value")
	}
}

func TestDockerdStartCommandReadErrorFails(t *testing.T) {
	fs := msb.NewTestFS(map[string][]byte{dockerdDaemonConfigPath: []byte(`{}`)}, nil)
	fs.ReadErr = errors.New("read boom")
	sb := &msb.MockSandbox{FSValue_: fs}
	if _, err := dockerdStartCommand(context.Background(), sb); err == nil {
		t.Fatal("expected an error when daemon.json cannot be read")
	}
}

// dockerdStartSandbox drives startDockerdIfPresent down its start path (the
// pre-restart readiness probe fails, the post-restart probe succeeds) and
// records every shell command.
type dockerdStartSandbox struct {
	msb.Sandbox

	calls      []string
	readyCalls int
}

func (s *dockerdStartSandbox) Shell(
	_ context.Context,
	command string,
	_ ...msbSdk.ExecOption,
) (msb.ShellResult, error) {
	s.calls = append(s.calls, command)
	switch command {
	case dockerdBinaryCheckCmd:
		return msb.NewTestResult(true, 0, "", "", nil), nil
	case dockerdReadyCmd:
		s.readyCalls++
		return msb.NewTestResult(s.readyCalls > 1, 0, "", "", nil), nil
	}
	return msb.NewTestResult(true, 0, "", "", nil), nil
}

func newDockerdStartSandbox(content string) *dockerdStartSandbox {
	files := map[string][]byte{}
	if content != "" {
		files[dockerdDaemonConfigPath] = []byte(content)
	}
	return &dockerdStartSandbox{
		Sandbox: msb.NewMockSandbox(msb.SandboxOpts{FSValue: msb.NewTestFS(files, nil)}),
	}
}

func TestStartDockerdUsesConfigDrivenCommandWhenDaemonJSONSetsVFS(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := newDockerdStartSandbox(`{"storage-driver":"vfs"}`)

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent: %v", err)
	}
	if !contains(joinStrings(sb.calls), dockerdRestartCmdConfigVFS) {
		t.Errorf("expected the config-driven start command, got calls:\n%s", joinStrings(sb.calls))
	}
	if contains(joinStrings(sb.calls), dockerdRestartCmd) {
		t.Errorf("must not pass --storage-driver when daemon.json sets vfs, got calls:\n%s", joinStrings(sb.calls))
	}
}

func TestStartDockerdFailsOnOtherStorageDriver(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := newDockerdStartSandbox(`{"storage-driver":"overlay2"}`)

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected startDockerdIfPresent to fail for a non-vfs driver")
	}
	if !contains(err.Error(), "overlay2") {
		t.Errorf("error should name the offending driver, got: %v", err)
	}
	if contains(joinStrings(sb.calls), dockerdRestartCmd) ||
		contains(joinStrings(sb.calls), dockerdRestartCmdConfigVFS) {
		t.Errorf("dockerd must not be started when the driver conflicts, got calls:\n%s", joinStrings(sb.calls))
	}
}
