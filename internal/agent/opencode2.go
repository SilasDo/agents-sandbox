package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// opencode2Name is the canonical registry name of the opencode 2 agent.
const opencode2Name = "opencode2"

// opencode2Port is the forwarded guest port the opencode2 serve daemon binds,
// matching the sandbox's fixed host/guest port mapping.
const opencode2Port = "4096"

// opencode2ServerUsername and opencode2ServerPassword are the fixed basic-auth
// credential the opencode2 daemon, the health probe, and the in-VM client share.
// v2's serve enforces HTTP basic auth on every /api/* route and, when
// OPENCODE_SERVER_PASSWORD is unset, generates a random password the launcher
// cannot know — which left the daemon permanently unhealthy. v2 hardcodes the
// basic-auth username to "opencode" (OPENCODE_SERVER_USERNAME no longer exists),
// so the values are equal; only the password is baked via
// OPENCODE_SERVER_PASSWORD, the username being implied.
const (
	opencode2ServerUsername = "opencode"
	opencode2ServerPassword = "opencode"
)

// opencode2CurlAuth is the curl basic-auth flag for the fixed opencode2 server
// credential, used by the health probe and the worktree API calls.
const opencode2CurlAuth = "-u " + opencode2ServerUsername + ":" + opencode2ServerPassword

// opencode2Profile is the opencode 2 (beta) coding agent. It embeds
// opencodeConfig for the guest config path, auth, and host drop-in it shares
// with v1 (v2 reads the same ~/.config/opencode files and auth.json), overrides
// the launcher config dir to its own namespace, and overrides the daemon,
// worktree, attach, and version behavior for the v2 server API.
type opencode2Profile struct {
	opencodeConfig
}

func (opencode2Profile) Name() string { return opencode2Name }

// ConfigDirName scopes opencode2's own launcher config dir; the guest config
// path and host drop-in still come from the embedded opencodeConfig.
func (opencode2Profile) ConfigDirName() string { return opencode2Name }

// ConfigDirAliases names the legacy shared config dir opencode2 used before it
// gained its own namespace.
func (opencode2Profile) ConfigDirAliases() []string { return []string{opencodeName} }

func (opencode2Profile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(opencode2Name),
		// The sandbox pins the baked version and resolves upgrades itself; the
		// env var disables opencode's own auto-update checks at startup. The
		// fixed server password gives the launcher and the in-VM client a
		// credential v2's mandatory basic auth accepts.
		AgentEnv: map[string]string{
			"OPENCODE_DISABLE_AUTOUPDATE": opencodeAutoupdateDisabled,
			"OPENCODE_SERVER_PASSWORD":    opencode2ServerPassword,
		},
		InstallCommand: `curl -fsSL https://opencode.ai/v2/install | bash -s -- --version "$OPENCODE2_VERSION" && cp /root/.opencode/bin/opencode /opt/agents-sandbox/bin/opencode2`,
		RequiresNode:   false,
	}
}

// opencode2ServeLog is where the start command redirects the opencode2 daemon's
// output, tailed for diagnostics when the daemon never becomes healthy.
const opencode2ServeLog = "/tmp/opencode2-serve.log"

func (opencode2Profile) DaemonStartCmd(serveOnly bool) string {
	hostname := "127.0.0.1"
	if serveOnly {
		hostname = "0.0.0.0"
	}
	return fmt.Sprintf(
		"nohup opencode2 serve --hostname %s --port %s > %s 2>&1 &",
		hostname,
		opencode2Port,
		opencode2ServeLog,
	)
}

func (opencode2Profile) DaemonKillCmd() string { return "pkill -f 'opencode2 serve' || true" }

func (opencode2Profile) DaemonHealthCmd() string {
	// v2's health endpoint is /api/info. There is no /api/health route: once
	// authenticated it answers 404 with an empty body, which fails to parse as
	// JSON and so reads as not-ready. No -f: on an HTTP error the body (e.g.
	// v2's UnauthorizedError) is kept for the probe's diagnostic detail instead
	// of being swallowed.
	return "curl -sm2 " + opencode2CurlAuth + " http://127.0.0.1:" + opencode2Port + "/api/info"
}

// parseOpencode2Health decodes v2's ServerInfo response. Unlike v1's
// {"healthy": bool}, /api/info has no health flag: a response carrying a
// version means the server is ready.
func parseOpencode2Health(stdout string) (bool, error) {
	var serverInfo struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(stdout), &serverInfo); err != nil {
		return false, fmt.Errorf("parse health response: %w", err)
	}
	return serverInfo.Version != "", nil
}

func (opencode2Profile) DaemonHealthParse(stdout string) (bool, error) {
	return parseOpencode2Health(stdout)
}

func (opencode2Profile) DaemonLogPath() string { return opencode2ServeLog }

// DaemonAuthHint: v2's basic auth is mandatory with a baked credential, so an
// external client must use it rather than setting its own.
func (opencode2Profile) DaemonAuthHint() string {
	return "The server requires basic auth: connect as user \"" + opencode2ServerUsername +
		"\" with password \"" + opencode2ServerPassword + "\" (baked into the image)."
}

// opencode2ServerURL is the loopback base URL of the opencode2 serve daemon,
// used by the worktree API calls.
const opencode2ServerURL = "http://127.0.0.1:" + opencode2Port

// opencode2LocatePrelude resolves the project scoped to target and exports the
// worktree calls' inputs: LOC (the raw /api/location response), PID (the
// project id) and CANON (the project's canonical directory — the main checkout,
// which the worktree list must exclude). It aborts the command when the location
// call fails, or the project id or canonical directory is missing or empty, so
// a request is never sent with an empty projectID and the list is never filtered
// against a literal "null".
func opencode2LocatePrelude(target string) string {
	return "LOC=$(curl -sf " + opencode2CurlAuth + " --get --data-urlencode " +
		shellQuote("location[directory]="+target) + " " + opencode2ServerURL + "/api/location) || exit 1; " +
		"PID=$(printf '%s' \"$LOC\" | jq -re '.project.id | select(length > 0)') || exit 1; " +
		"CANON=$(printf '%s' \"$LOC\" | jq -re '.project.canonical | select(length > 0)') || exit 1; "
}

// WorktreeListCmd lists the project's worktrees through v2's project-scoped
// route, dropping the canonical root entry so a worktree name equal to the
// repository directory name never reuses the main checkout.
func (opencode2Profile) WorktreeListCmd(target string) string {
	return opencode2LocatePrelude(target) +
		"curl -sf " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/worktree?projectID=$PID | " +
		"jq -c --arg canonical \"$CANON\" '[.[]|select(.directory != $canonical)]'"
}

// WorktreeCreateCmd creates a worktree through v2's project-scoped route. The
// body is Worktree.CreateInput (additionalProperties: false): the server picks
// the directory, and the base ref travels as "branch". jq builds the body so
// arbitrary ref names are quoted safely.
func (opencode2Profile) WorktreeCreateCmd(spec WorktreeSpec) string {
	args := "--arg pid \"$PID\" --arg name " + shellQuote(spec.Name)
	object := "{projectID:$pid,name:$name}"
	if spec.Base != "" {
		args += " --arg branch " + shellQuote(spec.Base)
		object = "{projectID:$pid,name:$name,branch:$branch}"
	}
	return opencode2LocatePrelude(spec.Target) +
		"curl -sf " + opencode2CurlAuth + " -X POST " + opencode2ServerURL + "/api/worktree " +
		"-H 'Content-Type: application/json' -d \"$(jq -nc " + args + " " + shellQuote(object) + ")\""
}

func (opencode2Profile) WorktreeParseDir(stdout string) (string, bool) {
	return parseWorktreeDirectory(stdout)
}

func (opencode2Profile) LatestVersion(ctx context.Context) (string, error) {
	return latestOpenCode2Version(ctx)
}

func (opencode2Profile) NewerThan(a, b string) (bool, error) {
	return newerOpenCode2Than(a, b)
}

func (opencode2Profile) AttachCommand(target string, args []string) string {
	// v2 takes the working directory as a positional argument; it has no --dir
	// flag (which the launcher passed until it learned v2's CLI shape).
	parts := []string{"opencode2", "--server", "http://127.0.0.1:4096", shellQuote(target)}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}
