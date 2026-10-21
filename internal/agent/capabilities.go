package agent

import "context"

// settingsFileName is the settings filename shared by the pi and claude-code
// agents, whose merged snippet config is written to <config dir>/settings.json.
const settingsFileName = "settings.json"

// DaemonProvider runs a long-lived server that clients attach to (opencode).
type DaemonProvider interface {
	DaemonStartCmd(serveOnly bool) string
	DaemonKillCmd() string
	DaemonHealthCmd() string
	DaemonHealthParse(stdout string) (bool, error)
	// DaemonLogPath is the in-VM file the start command redirects the daemon's
	// output to, tailed for diagnostics when the daemon never becomes healthy.
	DaemonLogPath() string
	// DaemonAuthHint is the one-line basic-auth note shown in serve-only
	// guidance, telling an external client how to authenticate (or that auth is
	// optional). It is empty when the daemon needs no credentials.
	DaemonAuthHint() string
}

// WorktreeProvider manages git worktrees through a running daemon. WorktreeListCmd
// takes the target — the launch directory the daemon scopes its worktree request
// to; the create path receives it on WorktreeSpec.Target instead. Agents with
// global routes ignore it.
type WorktreeProvider interface {
	WorktreeListCmd(target string) string
	WorktreeCreateCmd(spec WorktreeSpec) string
	WorktreeParseDir(stdout string) (string, bool)
}

// UpgradeChecker can resolve and compare releases.
type UpgradeChecker interface {
	LatestVersion(ctx context.Context) (string, error)
	NewerThan(a, b string) (bool, error)
}

// SessionStatusProvider exposes the daemon's session-status and pending-question
// endpoints so the launcher can wait for active sessions to finish before
// stopping a VM. Only the opencode (v1) daemon exposes these endpoints today;
// agents without them skip the quiescence wait.
type SessionStatusProvider interface {
	SessionStatusCmd() string
	QuestionListCmd() string
}

// ConfigMerger exposes data consumed by the shared merge logic in
// internal/configmerge.BuildMerged.
type ConfigMerger interface {
	SnippetPattern() string
	VMConfigPath(home string) string
	// ConfigFileNames returns the config filenames the agent reads from its VM
	// config directory that the merged config supersedes (and which a stale
	// host copy could otherwise shadow it with).
	ConfigFileNames() []string
}

// AttachRunner starts the client TUI/session.
type AttachRunner interface {
	AttachCommand(target string, args []string) string
}

// Provisioner declares host files to copy into the VM by default (drop-in).
type Provisioner interface {
	ProvisionRules() []ProvisionRule
}

// AsDaemonProvider returns the agent's DaemonProvider, if it implements one.
func AsDaemonProvider(a Agent) (DaemonProvider, bool) {
	p, ok := a.(DaemonProvider)
	return p, ok
}

// AsWorktreeProvider returns the agent's WorktreeProvider, if it implements one.
func AsWorktreeProvider(a Agent) (WorktreeProvider, bool) {
	p, ok := a.(WorktreeProvider)
	return p, ok
}

// AsUpgradeChecker returns the agent's UpgradeChecker, if it implements one.
func AsUpgradeChecker(a Agent) (UpgradeChecker, bool) { p, ok := a.(UpgradeChecker); return p, ok }

// AsSessionStatusProvider returns the agent's SessionStatusProvider, if it
// implements one.
func AsSessionStatusProvider(a Agent) (SessionStatusProvider, bool) {
	p, ok := a.(SessionStatusProvider)
	return p, ok
}

// AsConfigMerger returns the agent's ConfigMerger, if it implements one.
func AsConfigMerger(a Agent) (ConfigMerger, bool) { p, ok := a.(ConfigMerger); return p, ok }

// AsAttachRunner returns the agent's AttachRunner, if it implements one.
func AsAttachRunner(a Agent) (AttachRunner, bool) { p, ok := a.(AttachRunner); return p, ok }

// AsProvisioner returns the agent's Provisioner, if it implements one.
func AsProvisioner(a Agent) (Provisioner, bool) { p, ok := a.(Provisioner); return p, ok }

// ConfigDirAliaser declares alternate launcher config-dir names this agent used
// before its current ConfigDirName. They are fallback candidates only: the
// primary dir always wins when present.
type ConfigDirAliaser interface {
	ConfigDirAliases() []string
}

// AsConfigDirAliaser returns the agent's ConfigDirAliaser, if it implements one.
func AsConfigDirAliaser(a Agent) (ConfigDirAliaser, bool) {
	p, ok := a.(ConfigDirAliaser)
	return p, ok
}

// DetachedWorktreeReporter declares that the agent's daemon creates worktrees on
// a detached HEAD rather than a branch, so the launcher can warn the user to
// create a branch before they commit (and lose the work when the guest home
// volume goes away). Agents that keep a branch do not implement it.
type DetachedWorktreeReporter interface {
	WorktreeCreatesDetachedHead() bool
}

// AsDetachedWorktreeReporter returns the agent's DetachedWorktreeReporter, if it
// implements one.
func AsDetachedWorktreeReporter(a Agent) (DetachedWorktreeReporter, bool) {
	p, ok := a.(DetachedWorktreeReporter)
	return p, ok
}

// VersionProvider exposes how to read the installed agent version from a
// running image: the shell command to run and a parser for its output. Agents
// print their version in different formats, so parsing is per-agent.
type VersionProvider interface {
	VersionCmd() string
	ParseVersion(stdout string) (string, error)
}

// AsVersionProvider returns the agent's VersionProvider, if it implements one.
func AsVersionProvider(a Agent) (VersionProvider, bool) { p, ok := a.(VersionProvider); return p, ok }

// StreamEvent is a decoded daemon event: its type, the session it belongs to,
// and that session's parent (non-empty for a subagent session).
type StreamEvent struct {
	Type      string
	SessionID string
	ParentID  string
}

// EventStreamSpec describes the SSE event stream notify watches for an agent:
// the shell command that streams events in the VM, the event-type names that
// drive tracker transitions, and the agent name used in notification titles.
type EventStreamSpec struct {
	StreamCommand string
	BusyEvents    []string
	AwaitingInput []string
	IdleEvents    []string
	ErrorEvents   []string
	// StickyErrorEvents is the subset of ErrorEvents that latches the session
	// in the error state for the watcher's lifetime — reconnecting does not
	// clear it, since the watcher reuses one tracker across reconnects. An
	// error event not listed here ends only the current turn, so the session
	// notifies again on its next turn. Empty means no error event latches.
	StickyErrorEvents []string
	// SessionEvents are lifecycle event types whose payload may identify a
	// session's parent. A non-empty parent ID marks the session as a subagent
	// (child) session; no notification ever fires for a child session.
	SessionEvents []string
	// SessionListCommand is a one-shot shell command printing the daemon's raw
	// session list, run each time a stream opens to seed the filter with
	// sessions created before the watcher (re)connected. Empty disables seeding.
	SessionListCommand string
	// SessionListParse extracts the child session IDs from SessionListCommand's
	// stdout. The agent owns the list's JSON shape, so the command needs no jq
	// (or other tooling) in the runner image, which a custom base need not have.
	SessionListParse func(payload []byte) []string
	Name             string
	// Parse decodes one SSE data payload into a StreamEvent. ok is false when
	// the payload carries no event.
	Parse func(payload []byte) (StreamEvent, bool)
}

// EventStreamProvider exposes the SSE event stream an agent daemon serves.
type EventStreamProvider interface {
	EventStream() EventStreamSpec
}

// AsEventStreamProvider returns the agent's EventStreamProvider, if it
// implements one.
func AsEventStreamProvider(a Agent) (EventStreamProvider, bool) {
	p, ok := a.(EventStreamProvider)
	return p, ok
}
