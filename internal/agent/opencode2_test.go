package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestOpencode2LookupAndName(t *testing.T) {
	a, ok := agent.Lookup("opencode2")
	if !ok {
		t.Fatal("Lookup(\"opencode2\") returned not-ok")
	}
	if a.Name() != "opencode2" {
		t.Errorf("Name() = %q, want opencode2", a.Name())
	}
	found := false
	for _, n := range agent.Names() {
		if n == "opencode2" {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() = %v, want to include opencode2", agent.Names())
	}
}

func TestOpencode2ConfigDirName(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	if a.ConfigDirName() != "opencode2" {
		t.Errorf("ConfigDirName = %q, want opencode2 (v2 has its own config namespace)", a.ConfigDirName())
	}
}

func TestOpencode2ConfigDirAliases(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	al, ok := agent.AsConfigDirAliaser(a)
	if !ok {
		t.Fatal("opencode2 should declare config dir aliases")
	}
	if got := al.ConfigDirAliases(); !slices.Equal(got, []string{"opencode"}) {
		t.Errorf("ConfigDirAliases = %v, want [opencode]", got)
	}
}

func TestOpencode2WorktreeCreatesDetachedHead(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	reporter, ok := agent.AsDetachedWorktreeReporter(a)
	if !ok {
		t.Fatal("opencode2 should implement DetachedWorktreeReporter")
	}
	if !reporter.WorktreeCreatesDetachedHead() {
		t.Error("WorktreeCreatesDetachedHead = false, want true (v2 leaves a detached HEAD)")
	}
}

func TestOpencode2ImageSpec(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	spec := a.ImageSpec()
	if spec.VersionArg != "OPENCODE2_VERSION" {
		t.Errorf("VersionArg = %q, want OPENCODE2_VERSION", spec.VersionArg)
	}
	if _, ok := spec.AgentEnv["OPENCODE_DISABLE_AUTOUPDATE"]; !ok {
		t.Errorf("AgentEnv = %v, want OPENCODE_DISABLE_AUTOUPDATE key", spec.AgentEnv)
	}
	// v2's serve enforces basic auth on /api/* (auto-generating a password when
	// unset), so the daemon, the health probe, and the in-VM client must share a
	// fixed credential.
	if got := spec.AgentEnv["OPENCODE_SERVER_PASSWORD"]; got != "opencode" {
		t.Errorf("AgentEnv[OPENCODE_SERVER_PASSWORD] = %q, want opencode", got)
	}
	wantInstall := `curl -fsSL https://opencode.ai/v2/install | bash -s -- --version "$OPENCODE2_VERSION" && cp /root/.opencode/bin/opencode /opt/agents-sandbox/bin/opencode2`
	if spec.InstallCommand != wantInstall {
		t.Errorf("InstallCommand = %q, want %q", spec.InstallCommand, wantInstall)
	}
	if spec.RequiresNode {
		t.Error("RequiresNode = true, want false (opencode2 ships a native binary)")
	}
}

func TestOpencode2AttachCommand(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("opencode2 should implement AttachRunner")
	}
	got := runner.AttachCommand("/workspace", []string{"--print-logs"})
	want := "opencode2 --server http://127.0.0.1:4096 '/workspace' --print-logs"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestOpencode2AttachCommandQuotesTarget(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	runner, _ := agent.AsAttachRunner(a)
	got := runner.AttachCommand("/workspace/my notes", nil)
	want := "opencode2 --server http://127.0.0.1:4096 '/workspace/my notes'"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestOpencode2DaemonStartCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	daemon := mustDaemon(t, a)
	local := daemon.DaemonStartCmd(false)
	if !strings.Contains(local, "opencode2 serve --hostname 127.0.0.1 --port 4096") {
		t.Errorf("local DaemonStartCmd = %q, want opencode2 serve on 127.0.0.1:4096", local)
	}
	if !strings.Contains(local, "/tmp/opencode2-serve.log") {
		t.Errorf("local DaemonStartCmd = %q, want opencode2-serve.log", local)
	}
	serve := daemon.DaemonStartCmd(true)
	if !strings.Contains(serve, "--hostname 0.0.0.0") {
		t.Errorf("serve DaemonStartCmd = %q, want hostname 0.0.0.0", serve)
	}
}

func TestOpencode2DaemonKillAndHealthCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	daemon := mustDaemon(t, a)
	if !strings.Contains(daemon.DaemonKillCmd(), "pkill -f 'opencode2 serve'") {
		t.Errorf("DaemonKillCmd = %q, want pkill opencode2 serve", daemon.DaemonKillCmd())
	}
	health := daemon.DaemonHealthCmd()
	if !strings.Contains(health, "127.0.0.1:4096/api/info") {
		t.Errorf("DaemonHealthCmd = %q, want v2 /api/info endpoint", health)
	}
	if !strings.Contains(health, "-u opencode:opencode") {
		t.Errorf("DaemonHealthCmd = %q, want basic auth credentials", health)
	}
	if daemon.DaemonLogPath() != "/tmp/opencode2-serve.log" {
		t.Errorf("DaemonLogPath = %q, want /tmp/opencode2-serve.log", daemon.DaemonLogPath())
	}
	if hint := daemon.DaemonAuthHint(); !strings.Contains(hint, `user "opencode"`) ||
		!strings.Contains(hint, `password "opencode"`) {
		t.Errorf("DaemonAuthHint = %q, want the baked opencode:opencode credential", hint)
	}
}

func TestOpencode2DaemonHealthParse(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	daemon := mustDaemon(t, a)
	// v2's health endpoint is /api/info, which returns ServerInfo (no healthy
	// field); a decoded response carrying a version means the server is ready.
	ok, err := daemon.DaemonHealthParse(
		`{"version":"2.0.32","pid":1,"urls":["http://127.0.0.1:4096"],"paths":{"tmp":"/tmp"}}`,
	)
	if err != nil || !ok {
		t.Errorf("DaemonHealthParse(server info) = %v, %v, want true, nil", ok, err)
	}
	notOK, err := daemon.DaemonHealthParse(`{"error":{"name":"UnauthorizedError"}}`)
	if err != nil || notOK {
		t.Errorf("DaemonHealthParse(error body) = %v, %v, want false, nil", notOK, err)
	}
	parsed, err := daemon.DaemonHealthParse("not json")
	if parsed {
		t.Error("DaemonHealthParse(invalid) ok = true, want false")
	}
	if _, ok := errors.AsType[*json.SyntaxError](err); !ok {
		t.Errorf("DaemonHealthParse(invalid) error = %v, want *json.SyntaxError", err)
	}
}

func TestOpencode2WorktreeListCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeListCmd("/workspace")
	// Exact equality (mirroring the create tests) pins the whole command: a
	// substring check would be satisfied by the shared location prelude even if
	// the list curl lost its credential or fell back to a v1 route.
	want := `LOC=$(curl -fsS -m 30 -u opencode:opencode --get --data-urlencode 'location[directory]=/workspace' http://127.0.0.1:4096/api/location) || exit 1; PID=$(printf '%s' "$LOC" | jq -re '.project.id | select(length > 0)') || exit 1; CANON=$(printf '%s' "$LOC" | jq -re '.project.canonical | select(length > 0)') || exit 1; LIST=$(curl -fsS -m 30 -u opencode:opencode http://127.0.0.1:4096/api/worktree?projectID=$PID) || exit 1; printf '%s' "$LIST" | jq -ce --arg canonical "$CANON" 'if type == "array" and all(.[]; type == "object" and (.directory | type == "string" and length > 0)) then [.[]|select(.directory != $canonical)] else error("worktree list: unexpected payload") end'`
	if got != want {
		t.Errorf("WorktreeListCmd = %q, want %q", got, want)
	}
}

// TestOpencode2WorktreeCurlsBoundedAndKeepStderr pins that every v2 worktree
// curl — the shared location prelude plus the list/create calls — carries a
// bounded timeout and keeps curl's error message, so a hung daemon cannot block
// the launcher forever and an HTTP failure reaches the user instead of emerging
// as an empty stderr.
func TestOpencode2WorktreeCurlsBoundedAndKeepStderr(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	commands := map[string]string{
		"list":   worktree.WorktreeListCmd("/workspace"),
		"create": worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature-x", Target: "/workspace"}),
	}
	for name, cmd := range commands {
		segments := strings.Split(cmd, "curl ")
		if len(segments) < 3 {
			t.Fatalf("%s command should issue the location prelude plus one call: %q", name, cmd)
		}
		for _, segment := range segments[1:] {
			if !strings.HasPrefix(segment, "-fsS -m 30 ") {
				t.Errorf("%s curl is unbounded or silences its error: %q", name, "curl "+segment)
			}
		}
	}
}

func TestOpencode2WorktreeCreateCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature/x", Target: "/workspace"})
	want := `LOC=$(curl -fsS -m 30 -u opencode:opencode --get --data-urlencode 'location[directory]=/workspace' http://127.0.0.1:4096/api/location) || exit 1; PID=$(printf '%s' "$LOC" | jq -re '.project.id | select(length > 0)') || exit 1; CANON=$(printf '%s' "$LOC" | jq -re '.project.canonical | select(length > 0)') || exit 1; curl -fsS -m 30 -u opencode:opencode -X POST http://127.0.0.1:4096/api/worktree -H 'Content-Type: application/json' -d "$(jq -nc --arg pid "$PID" --arg name 'feature/x' '{projectID:$pid,name:$name}')"`
	if got != want {
		t.Errorf("WorktreeCreateCmd = %q, want %q", got, want)
	}
}

func TestOpencode2WorktreeCreateCmdWithBase(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature-x", Base: "main", Target: "/workspace"})
	want := `LOC=$(curl -fsS -m 30 -u opencode:opencode --get --data-urlencode 'location[directory]=/workspace' http://127.0.0.1:4096/api/location) || exit 1; PID=$(printf '%s' "$LOC" | jq -re '.project.id | select(length > 0)') || exit 1; CANON=$(printf '%s' "$LOC" | jq -re '.project.canonical | select(length > 0)') || exit 1; curl -fsS -m 30 -u opencode:opencode -X POST http://127.0.0.1:4096/api/worktree -H 'Content-Type: application/json' -d "$(jq -nc --arg pid "$PID" --arg name 'feature-x' --arg branch 'main' '{projectID:$pid,name:$name,branch:$branch}')"`
	if got != want {
		t.Errorf("WorktreeCreateCmd(with base) = %q, want %q", got, want)
	}
}

func TestOpencode2WorktreeCreateCmdQuotesTargetAndBase(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x", Base: "it's", Target: "/workspace/my notes"})
	if !strings.Contains(got, `'location[directory]=/workspace/my notes'`) {
		t.Errorf("target not single-quoted: %q", got)
	}
	if !strings.Contains(got, `--arg branch 'it'\''s'`) {
		t.Errorf("base not shell-quoted: %q", got)
	}
}

// TestOpencode2WorktreePreludeGuardsFailClosed pins the two prelude guards: an
// explicit empty project id must not yield `projectID=`, and a missing/null
// canonical must abort instead of becoming the literal "null" the list filter
// would then compare every entry against (keeping the canonical root).
func TestOpencode2WorktreePreludeGuardsFailClosed(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	commands := map[string]string{
		"list":   worktree.WorktreeListCmd("/workspace"),
		"create": worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature-x", Target: "/workspace"}),
	}
	for name, got := range commands {
		if !strings.Contains(got, "jq -re '.project.id | select(length > 0)') || exit 1") {
			t.Errorf("%s command project-id guard must reject an empty id: %q", name, got)
		}
		if !strings.Contains(got, "jq -re '.project.canonical | select(length > 0)') || exit 1") {
			t.Errorf("%s command canonical guard must reject a missing canonical: %q", name, got)
		}
		if strings.Contains(got, "jq -re '.project.id')") || strings.Contains(got, "jq -r '.project.canonical'") {
			t.Errorf("%s command still has the fail-open guards: %q", name, got)
		}
	}
}

func TestOpencode2WorktreeParseDir(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	dir, ok := worktree.WorktreeParseDir(`{"directory":"/home/dev/ws","strategy":"git_worktree"}`)
	if !ok || dir != "/home/dev/ws" {
		t.Errorf("WorktreeParseDir = %q, %v, want /home/dev/ws, true", dir, ok)
	}
	if _, ok := worktree.WorktreeParseDir("not json"); ok {
		t.Error("WorktreeParseDir(invalid) should return not-ok")
	}
	if _, ok := worktree.WorktreeParseDir(`{"directory":""}`); ok {
		t.Error("WorktreeParseDir(empty) should return not-ok")
	}
}

func TestOpencode2LatestVersionCancelledCtx(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("opencode2 should implement UpgradeChecker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestOpencode2NewerThanBetaBuilds(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	checker := mustChecker(t, a)
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "newer beta build", a: "0.0.0-beta-18866", b: "0.0.0-beta-17823", want: true},
		{name: "older beta build", a: "0.0.0-beta-17823", b: "0.0.0-beta-18866", want: false},
		{name: "equal beta build", a: "0.0.0-beta-18866", b: "0.0.0-beta-18866", want: false},
		{name: "cross digit length newer", a: "0.0.0-beta-10000", b: "0.0.0-beta-9999", want: true},
		{name: "cross digit length older", a: "0.0.0-beta-9999", b: "0.0.0-beta-10000", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checker.NewerThan(tc.a, tc.b)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("NewerThan(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestOpencode2NewerThanInvalidVersion(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	checker := mustChecker(t, a)
	if _, err := checker.NewerThan("not-a-version", "0.0.0-beta-18866"); err == nil {
		t.Error("NewerThan(invalid) should error")
	}
}

func TestOpencode2ConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("opencode2 should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "opencode*.json*" {
		t.Errorf("SnippetPattern = %q", merger.SnippetPattern())
	}
	if merger.VMConfigPath("/home/user") != filepath.Join("/home/user", ".config", "opencode", "opencode.jsonc") {
		t.Errorf("VMConfigPath = %q", merger.VMConfigPath("/home/user"))
	}
}

func TestOpencode2ConfigFileNames(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("opencode2 should implement ConfigMerger")
	}
	names := merger.ConfigFileNames()
	want := []string{
		"config.json",
		"opencode.json",
		"opencode.jsonc",
		"opencode.json5",
		"opencode.yaml",
		"opencode.yml",
	}
	if !slices.Equal(names, want) {
		t.Errorf("ConfigFileNames = %v, want %v", names, want)
	}
}

func TestOpencode2ProvisionRules(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("opencode2 should implement Provisioner")
	}
	rules := provisioner.ProvisionRules()
	if len(rules) == 0 {
		t.Fatal("ProvisionRules must not be empty")
	}
	found := false
	for _, r := range rules {
		if r.Dir == ".config/opencode" {
			found = true
		}
	}
	if !found {
		t.Errorf("ProvisionRules missing .config/opencode: %+v", rules)
	}
}

func TestOpencode2EventStream(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	provider, ok := agent.AsEventStreamProvider(a)
	if !ok {
		t.Fatal("opencode2 should implement EventStreamProvider")
	}
	got := provider.EventStream()
	if got.Parse == nil {
		t.Fatal("EventStream().Parse is nil")
	}
	got.Parse = nil
	if got.SessionList == nil {
		t.Fatal("EventStream().SessionList is nil")
	}
	if want := "curl -fsS -m 30 -u opencode:opencode http://127.0.0.1:4096/api/session"; got.SessionList.Command() != want {
		t.Errorf("SessionList.Command() = %q, want %q", got.SessionList.Command(), want)
	}
	if children := got.SessionList.Parse([]byte(
		`{"data":[{"id":"ses_child","parentID":"ses_top"}]}`,
	)); len(children) != 1 || children[0] != "ses_child" {
		t.Errorf("SessionList.Parse() = %v, want [ses_child]", children)
	}
	got.SessionList = nil
	want := agent.EventStreamSpec{
		StreamCommand: "curl -N -s -u opencode:opencode http://127.0.0.1:4096/api/event",
		BusyEvents:    []string{"session.execution.started", "session.step.started"},
		AwaitingInput: []string{"permission.asked", "form.created"},
		IdleEvents:    []string{"session.execution.succeeded"},
		ErrorEvents:   []string{"session.error", "session.execution.failed"},
		SessionEvents: []string{"session.created"},
		Name:          "opencode2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EventStream() = %+v, want %+v", got, want)
	}
}
