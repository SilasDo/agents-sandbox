package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// opencodeProfile is the opencode (v1) coding agent. It embeds opencodeConfig
// for the guest config path, auth, and host drop-in it shares with opencode2.
type opencodeProfile struct {
	opencodeConfig
}

func (opencodeProfile) Name() string { return opencodeName }

func (opencodeProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(opencodeName),
		AgentEnv: map[string]string{
			"OPENCODE_DISABLE_AUTOUPDATE":      opencodeAutoupdateDisabled,
			"OPENCODE_EXPERIMENTAL_WORKSPACES": "true",
		},
		InstallCommand: `curl -fsSL https://opencode.ai/install | bash -s -- --version "$OPENCODE_VERSION" && cp /root/.opencode/bin/opencode /opt/agents-sandbox/bin`,
		RequiresNode:   false,
	}
}

// opencodeServeLog is where the start command redirects the opencode daemon's
// output, tailed for diagnostics when the daemon never becomes healthy.
const opencodeServeLog = "/tmp/opencode-serve.log"

func (opencodeProfile) DaemonStartCmd(serveOnly bool) string {
	hostname := "127.0.0.1"
	if serveOnly {
		hostname = "0.0.0.0"
	}
	return fmt.Sprintf(
		"nohup opencode serve --hostname %s --port %s > %s 2>&1 &",
		hostname,
		"4096",
		opencodeServeLog,
	)
}

func (opencodeProfile) DaemonKillCmd() string { return "pkill -f 'opencode serve' || true" }
func (opencodeProfile) DaemonHealthCmd() string {
	// No -f: on an HTTP error the body is kept for the health probe's
	// diagnostic detail instead of being swallowed.
	return "curl -sm2 http://127.0.0.1:4096/global/health"
}
func (opencodeProfile) DaemonLogPath() string { return opencodeServeLog }

// DaemonAuthHint: v1's basic auth is opt-in (its /global/health is
// unauthenticated), so the credentials are the user's to set.
func (opencodeProfile) DaemonAuthHint() string {
	return "Optional: set OPENCODE_SERVER_PASSWORD (and OPENCODE_SERVER_USERNAME) to protect the server with basic auth."
}

func (opencodeProfile) DaemonHealthParse(stdout string) (bool, error) {
	return parseDaemonHealth(stdout)
}

// SessionStatusCmd returns the v1 server endpoint listing session states, used
// by the launcher to wait for active sessions to finish before stopping a VM.
func (opencodeProfile) SessionStatusCmd() string {
	return "curl -sf http://127.0.0.1:4096/session/status"
}

// QuestionListCmd returns the v1 server endpoint listing pending questions,
// used by the launcher to keep sessions awaiting user input from being cut off.
func (opencodeProfile) QuestionListCmd() string {
	return "curl -sf http://127.0.0.1:4096/question"
}

// WorktreeListCmd lists worktrees via v1's global route; the launch directory
// is ignored because the daemon resolves the project itself.
func (opencodeProfile) WorktreeListCmd(string) string {
	return "curl -sf http://127.0.0.1:4096/experimental/worktree"
}

func (opencodeProfile) WorktreeCreateCmd(spec WorktreeSpec) string {
	body := fmt.Sprintf(`{"name":%q}`, spec.Name)
	if spec.Base != "" {
		body = fmt.Sprintf(`{"name":%q,"startCommand":"git reset --hard %s"}`, spec.Name, spec.Base)
	}
	return fmt.Sprintf(
		"curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '%s'",
		body,
	)
}

func (opencodeProfile) WorktreeParseDir(stdout string) (string, bool) {
	return parseWorktreeDirectory(stdout)
}

func (opencodeProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestOpenCodeVersion(ctx)
}

func (opencodeProfile) NewerThan(a, b string) (bool, error) { return newerOpenCodeThan(a, b) }

func (opencodeProfile) AttachCommand(target string, args []string) string {
	parts := []string{opencodeName, "attach", "http://127.0.0.1:4096", "--dir", shellQuote(target)}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

func (opencodeProfile) VersionCmd() string { return "opencode --version" }
func (opencodeProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}

func (opencodeProfile) EventStream() EventStreamSpec {
	return EventStreamSpec{
		StreamCommand: "curl -N -s http://127.0.0.1:4096/global/event",
		BusyEvents:    []string{"message.part.updated", "session.updated"},
		AwaitingInput: []string{"permission.asked", "question.asked"},
		IdleEvents:    []string{"session.idle"},
		ErrorEvents:   []string{"session.error"},
		Name:          opencodeName,
		Parse:         parseOpencodeV1Event,
	}
}

// parseOpencodeV1Event decodes v1's /global/event SSE payload: the event type
// and session id live under payload.{type,properties.sessionID}, and a
// session.created event's parent (non-empty for a subagent session) lives
// under payload.properties.info.parentID.
func parseOpencodeV1Event(payload []byte) (StreamEvent, bool) {
	var raw struct {
		Payload struct {
			Type       string `json:"type"`
			Properties struct {
				SessionID string `json:"sessionID"`
				Info      struct {
					ParentID string `json:"parentID"`
				} `json:"info"`
			} `json:"properties"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return StreamEvent{}, false
	}
	if raw.Payload.Type == "" {
		return StreamEvent{}, false
	}
	return StreamEvent{
		Type:      raw.Payload.Type,
		SessionID: raw.Payload.Properties.SessionID,
		ParentID:  raw.Payload.Properties.Info.ParentID,
	}, true
}
