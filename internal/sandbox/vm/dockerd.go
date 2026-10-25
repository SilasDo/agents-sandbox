package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	dockerdBinaryCheckCmd = "command -v dockerd >/dev/null 2>&1"
	dockerdReadyCmd       = "docker info"

	// dockerdDaemonConfigPath is dockerd's default configuration file. The
	// launcher reads it (see guestStorageDriver) so it never forces
	// storage-driver both in the file and on the command line, which dockerd
	// rejects (inoio/agents-sandbox#287).
	dockerdDaemonConfigPath = "/etc/docker/daemon.json"
	vfsStorageDriver        = "vfs"

	dockerdTeardownCmd = "pkill dockerd >/dev/null 2>&1 || true; " +
		"pkill containerd >/dev/null 2>&1 || true; " +
		"find /run /var/run -iname 'docker*.pid' -delete >/dev/null 2>&1 || true; " +
		"find /run /var/run \\( -iname '*containerd*.sock' -o -iname '*containerd*.pid' \\) -delete >/dev/null 2>&1 || true; "
	dockerdStartPrefix = "nohup dockerd -H unix:///var/run/docker.sock"
	dockerdVFSFlag     = " --storage-driver=" + vfsStorageDriver
	dockerdLogSuffix   = " > /var/log/dockerd.log 2>&1 &"

	// dockerdRestartCmd forces vfs with a flag, for images whose daemon.json
	// sets no storage driver.
	dockerdRestartCmd = dockerdTeardownCmd + dockerdStartPrefix + dockerdVFSFlag + dockerdLogSuffix
	// dockerdRestartCmdConfigVFS relies on the image's daemon.json to force vfs:
	// passing the flag too would make dockerd reject the duplicated directive.
	dockerdRestartCmdConfigVFS = dockerdTeardownCmd + dockerdStartPrefix + dockerdLogSuffix
)

// The readiness window must exceed dockerd's 15s containerd start timeout, so a
// restart has time to come up before the poll gives up. Tests shorten the vars.
const (
	dockerdReadyTimeoutDefault = 30 * time.Second
	dockerdPollIntervalDefault = time.Second
)

// dockerdStartCommand picks the dockerd start command for this VM. The
// microsandbox filesystem does not support overlayfs, so vfs must win; it is
// requested with --storage-driver=vfs unless the image's daemon.json already
// sets it, in which case the flag is dropped because dockerd refuses a
// directive set both as a flag and in the configuration file (#287). A
// daemon.json requesting a different driver is a hard error rather than a
// silent override or a flag/file conflict.
func dockerdStartCommand(ctx context.Context, sb msb.Sandbox) (string, error) {
	driver, err := guestStorageDriver(ctx, sb)
	if err != nil {
		return "", err
	}
	switch driver {
	case "":
		return dockerdRestartCmd, nil
	case vfsStorageDriver:
		return dockerdRestartCmdConfigVFS, nil
	default:
		return "", fmt.Errorf(
			"%s sets storage-driver=%q, but the microsandbox filesystem requires %q; "+
				"remove the storage-driver entry or set it to %q",
			dockerdDaemonConfigPath, driver, vfsStorageDriver, vfsStorageDriver,
		)
	}
}

// guestStorageDriver returns the storage-driver requested by the image's
// daemon.json. It returns "" when the file is absent, sets no storage-driver,
// or is not parseable as JSON, in which case dockerd reports the parse error
// itself.
func guestStorageDriver(ctx context.Context, sb msb.Sandbox) (string, error) {
	exists, err := sb.FS().Exists(ctx, dockerdDaemonConfigPath)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", dockerdDaemonConfigPath, err)
	}
	if !exists {
		return "", nil
	}
	raw, err := sb.FS().Read(ctx, dockerdDaemonConfigPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dockerdDaemonConfigPath, err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		// Unparseable: dockerd reports the parse error itself.
		return "", nil //nolint:nilerr // malformed daemon.json is left for dockerd to reject
	}
	rawDriver, ok := config["storage-driver"]
	if !ok || string(rawDriver) == "null" {
		return "", nil
	}
	var driver string
	if err := json.Unmarshal(rawDriver, &driver); err != nil {
		return "", fmt.Errorf("%s has a non-string storage-driver value", dockerdDaemonConfigPath)
	}
	return driver, nil
}

var (
	dockerdReadyTimeout = dockerdReadyTimeoutDefault
	dockerdPollInterval = dockerdPollIntervalDefault
)

// startDockerdIfPresent starts dockerd inside the VM if the docker image is in
// use, or does nothing otherwise. It is safe to call on every VM bootstrap
// because it checks for the dockerd binary and handles the case where dockerd
// is already running.
func startDockerdIfPresent(ctx context.Context, sb msb.Sandbox, ui termio.UI) error {
	out, checkErr := sb.Shell(ctx, dockerdBinaryCheckCmd, msbSdk.WithExecUser(RootUser))
	if checkErr != nil {
		return fmt.Errorf("while checking dockerd binary: %w", checkErr)
	}
	if !out.Success() {
		ui.Verbosef("/usr/bin/dockerd not present, skipping Docker startup")
		return nil
	}

	// Check if an existing dockerd is already healthy
	if infoOut, readyErr := sb.Shell(
		ctx,
		dockerdReadyCmd,
		msbSdk.WithExecUser(DefaultSandboxUser),
	); readyErr == nil &&
		infoOut.Success() {
		ui.Verbose("using already running dockerd")
		return nil
	}

	ui.Verbosef("starting dockerd with vfs storage driver")
	startCmd, err := dockerdStartCommand(ctx, sb)
	if err != nil {
		return err
	}
	// The command background-launches dockerd, so the shell always exits 0; a
	// startup failure surfaces through the readiness loop below, not the exit
	// code. Only a hard exec failure is fatal here.
	if _, restartErr := sb.Shell(ctx, startCmd, msbSdk.WithExecUser(RootUser)); restartErr != nil {
		return fmt.Errorf("start dockerd: %w (no result)", restartErr)
	}

	deadline := time.Now().Add(dockerdReadyTimeout)
	for time.Now().Before(deadline) {
		out, pollErr := sb.Shell(ctx, dockerdReadyCmd, msbSdk.WithExecUser(DefaultSandboxUser))
		if pollErr == nil && out.Success() {
			ui.Verbosef("dockerd is ready")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(dockerdPollInterval):
			if out != nil {
				ui.Verbosef("dockerd readiness: err=%v, exit=%d, stdout=%q, stderr=%q",
					pollErr, out.ExitCode(), out.Stdout(), out.Stderr())
			} else {
				ui.Verbosef("dockerd readiness: err=%v", pollErr)
			}
		}
	}
	data, err := sb.FS().ReadString(ctx, "/var/log/dockerd.log")
	if err == nil && data != "" {
		return fmt.Errorf(
			"dockerd did not become ready within %s; dockerd log:\n%s",
			dockerdReadyTimeout, data,
		)
	}
	return fmt.Errorf("dockerd did not become ready within %s (dockerd log unavailable)", dockerdReadyTimeout)
}
