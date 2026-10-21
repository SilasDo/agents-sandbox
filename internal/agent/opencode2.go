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

// opencode2CurlFlags apply to every v2 worktree API call: -m bounds the request
// so a hung daemon cannot block the launcher indefinitely, and -sS keeps curl's
// error message on stderr (plain -s would silence it, leaving an empty error
// message) while -f makes an HTTP error status a failure.
const opencode2CurlFlags = "-fsS -m 30"

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

// WorktreeCreatesDetachedHead reports that v2's daemon creates worktrees on a
// detached HEAD (v1 keeps its own branch), so the launcher can tell the user how
// to keep their commits.
func (opencode2Profile) WorktreeCreatesDetachedHead() bool { return true }

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
	return "LOC=$(curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " --get --data-urlencode " +
		shellQuote("location[directory]="+target) + " " + opencode2ServerURL + "/api/location) || exit 1; " +
		"PID=$(printf '%s' \"$LOC\" | jq -re '.project.id | select(length > 0)') || exit 1; " +
		"CANON=$(printf '%s' \"$LOC\" | jq -re '.project.canonical | select(length > 0)') || exit 1; "
}

// opencode2WorktreeListFilter is the jq program that drops the project's
// canonical root from the worktree list. It accepts only an array whose entries
// each carry a non-empty string "directory", and fails the list otherwise: a
// non-array payload, or an entry whose directory field was renamed, blanked, or
// retyped, must not be accepted — the base-name match would silently ignore
// such an entry and create a second worktree beside the existing one.
const opencode2WorktreeListFilter = `if type == "array" and all(.[]; type == "object" and (.directory | type == "string" and length > 0)) then [.[]|select(.directory != $canonical)] else error("worktree list: unexpected payload") end`

// WorktreeListCmd lists the project's worktrees through v2's project-scoped
// route. The curl response is captured before jq runs, so curl's own failure
// propagates (a piped-into-jq failure would exit 0 with empty stdout and read as
// "no worktrees"); the filter then drops the canonical root entry and fails on a
// non-array payload (see opencode2WorktreeListFilter).
func (opencode2Profile) WorktreeListCmd(target string) string {
	return opencode2LocatePrelude(target) +
		"LIST=$(curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/worktree?projectID=$PID) || exit 1; " +
		"printf '%s' \"$LIST\" | jq -ce --arg canonical \"$CANON\" " + shellQuote(opencode2WorktreeListFilter)
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
		"curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " -X POST " + opencode2ServerURL + "/api/worktree " +
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

// EventStream exposes v2's authed /api/event SSE stream, which carries a flat
// envelope (type plus data.sessionID) unlike v1's nested payload shape. A user
// turn is one session.execution.started/succeeded pair and awaiting input is a
// permission.asked or form.created event.
func (opencode2Profile) EventStream() EventStreamSpec {
	return EventStreamSpec{
		StreamCommand:      "curl -N -s " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/event",
		BusyEvents:         []string{"session.execution.started", "session.step.started"},
		AwaitingInput:      []string{"permission.asked", "form.created"},
		IdleEvents:         []string{"session.execution.succeeded"},
		ErrorEvents:        []string{sessionErrorEvent, "session.execution.failed"},
		StickyErrorEvents:  nil,
		SessionEvents:      []string{"session.created"},
		SessionListCommand: "curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/session",
		SessionListParse:   parseOpencodeV2SessionList,
		Name:               opencode2Name,
		Parse:              parseOpencodeV2Event,
	}
}

// parseOpencodeV2Event decodes v2's /api/event SSE payload: the event type and
// session id live at data.sessionID, with two fallbacks the flat envelope makes
// necessary — a form.created nests its session id under data.form.sessionID, and
// a session event carries the parent (non-empty for a subagent session) under
// data.info.parentID.
func parseOpencodeV2Event(payload []byte) (StreamEvent, bool) {
	var raw struct {
		Type string `json:"type"`
		Data struct {
			SessionID string `json:"sessionID"`
			ParentID  string `json:"parentID"`
			Form      struct {
				SessionID string `json:"sessionID"`
			} `json:"form"`
			Info struct {
				ParentID string `json:"parentID"`
			} `json:"info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return StreamEvent{}, false
	}
	if raw.Type == "" {
		return StreamEvent{}, false
	}
	sessionID := raw.Data.SessionID
	if sessionID == "" {
		sessionID = raw.Data.Form.SessionID
	}
	parentID := raw.Data.ParentID
	if parentID == "" {
		parentID = raw.Data.Info.ParentID
	}
	return StreamEvent{Type: raw.Type, SessionID: sessionID, ParentID: parentID}, true
}

// parseOpencodeV2SessionList extracts child session ids from v2's /api/session
// response, which wraps the array under "data".
func parseOpencodeV2SessionList(payload []byte) []string {
	var body struct {
		Data []sessionParent `json:"data"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil
	}
	return childSessionIDs(body.Data)
}
