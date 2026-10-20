// Package harness holds end-to-end smoke tests that start every registered
// coding-agent harness in a real microsandbox VM and assert it reaches its
// full-screen TUI. The VM-driven test lives behind the `msb` build tag; the
// untagged files here are inert and shared by it: the harness table, the child
// environment allowlist, and the output helpers.
package harness

import (
	"bytes"
	"path/filepath"
	"strings"
)

// daemonTimeoutMarker is what the launcher prints when its daemon health probe
// never succeeds. A harness whose output contains it never reached the TUI.
const daemonTimeoutMarker = "did not become healthy"

// altScreenEnter is the terminal control sequence a harness emits as its
// full-screen TUI takes over the alternate screen buffer.
const altScreenEnter = "\x1b[?1049h"

// escapeByte starts an ANSI escape sequence.
const escapeByte = 0x1b

// isolatedEnvKeep lists the inherited variables the child may keep: only
// terminal and proxy settings. Dropping HOME, XDG_*, AGENTS_SANDBOX_*,
// OPENCODE_SANDBOX_*, and MSB_* is the point of the isolation.
var isolatedEnvKeep = []string{
	"PATH", "TERM", "LANG", "LC_ALL", "LC_CTYPE",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "no_proxy",
}

// harnessSpecs maps every registered harness to how the smoke test drives it.
// It must stay in sync with agent.Names(); the smoke test asserts the two key
// sets are equal.
var harnessSpecs = map[string]harnessSpec{
	"claude-code": {QuitKeys: []byte{0x03}, Network: ""},
	"opencode":    {QuitKeys: []byte{0x03}, Network: ""},
	"opencode2":   {QuitKeys: []byte{0x03}, Network: ""},
	"pi":          {QuitKeys: []byte{0x03}, Network: ""},
}

// harnessSpec describes how to drive one harness to its TUI and quit it.
type harnessSpec struct {
	// QuitKeys are written to the pty to leave the full-screen TUI.
	QuitKeys []byte
	// Network overrides the default `--network none` when a harness needs
	// egress to render.
	Network string
}

// isolatedEnv builds the child environment from inherited: it keeps only the
// allowlisted variables (defaulting TERM), then appends a private
// HOME/XDG_*/TMPDIR tree rooted at root, so the child reads and writes no real
// agents-sandbox config or state.
//
// projectDir is unused for now; a later task scopes the child's per-project
// config dir with it.
func isolatedEnv(root, _ string, inherited []string) []string {
	inheritedByName := make(map[string]string, len(inherited))
	for _, entry := range inherited {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			inheritedByName[name] = value
		}
	}

	env := make([]string, 0, len(isolatedEnvKeep)+7)
	for _, name := range isolatedEnvKeep {
		if value, ok := inheritedByName[name]; ok {
			env = append(env, name+"="+value)
		}
	}
	if _, ok := inheritedByName["TERM"]; !ok {
		env = append(env, "TERM=xterm-256color")
	}
	return append(env,
		"HOME="+filepath.Join(root, "home"),
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"TMPDIR="+filepath.Join(root, "tmp"),
	)
}

// stripANSI removes CSI escape sequences (ESC '[' ... final byte) from b so
// captured pty output can be searched as plain text.
func stripANSI(b []byte) string {
	stripped := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == escapeByte && i+1 < len(b) && b[i+1] == '[' {
			i = ansiSequenceEnd(b, i)
			continue
		}
		stripped = append(stripped, b[i])
	}
	return string(stripped)
}

// ansiSequenceEnd returns the index of the final byte of the CSI sequence
// starting at b[start] (ESC '['), or len(b)-1 when the sequence is truncated.
func ansiSequenceEnd(b []byte, start int) int {
	for i := start + 2; i < len(b); i++ {
		if isANSIFinalByte(b[i]) {
			return i
		}
	}
	return len(b) - 1
}

// isANSIFinalByte reports whether c terminates a CSI escape sequence; final
// bytes occupy the range '@' through '~' (0x40 to 0x7e).
func isANSIFinalByte(c byte) bool {
	return c >= 0x40 && c <= 0x7e
}

// tuiStarted reports whether the accumulated output shows a harness taking
// over the alternate screen, the signal that its full-screen TUI came up.
func tuiStarted(output []byte) bool {
	return bytes.Contains(output, []byte(altScreenEnter))
}
