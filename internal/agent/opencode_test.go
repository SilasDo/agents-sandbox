package agent_test

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestOpencodeAttachCommand(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	cmd, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("opencode should implement AttachRunner")
	}
	got := cmd.AttachCommand("/workspace", []string{"--print-logs"})
	want := "opencode attach http://127.0.0.1:4096 --dir '/workspace' --print-logs"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestOpencodeConfigDirAliases(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if _, ok := agent.AsConfigDirAliaser(a); ok {
		t.Error("opencode (v1) must not declare config dir aliases")
	}
}

func TestOpencodeAttachCommandQuotesTarget(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	cmd, _ := agent.AsAttachRunner(a)
	cases := map[string]string{
		"/workspace/my notes": "opencode attach http://127.0.0.1:4096 --dir '/workspace/my notes'",
		"/workspace/it's":     `opencode attach http://127.0.0.1:4096 --dir '/workspace/it'\''s'`,
	}
	for target, want := range cases {
		if got := cmd.AttachCommand(target, nil); got != want {
			t.Errorf("AttachCommand(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestOpencodeWorktreeCreateCmdUsesName(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature/x"})
	want := "curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"feature/x\"}'"
	if got != want {
		t.Errorf("WorktreeCreateCmd = %q, want %q", got, want)
	}
}

func TestOpencodeDaemonStartCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	daemon := mustDaemon(t, a)
	local := daemon.DaemonStartCmd(false)
	if !strings.Contains(local, "--hostname 127.0.0.1") {
		t.Errorf("local DaemonStartCmd = %q, want hostname 127.0.0.1", local)
	}
	serve := daemon.DaemonStartCmd(true)
	if !strings.Contains(serve, "--hostname 0.0.0.0") {
		t.Errorf("serve DaemonStartCmd = %q, want hostname 0.0.0.0", serve)
	}
}

func TestOpencodeDaemonKillAndHealthCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	daemon := mustDaemon(t, a)
	if daemon.DaemonKillCmd() == "" {
		t.Error("DaemonKillCmd must not be empty")
	}
	if !strings.Contains(daemon.DaemonHealthCmd(), "127.0.0.1:4096") {
		t.Errorf("DaemonHealthCmd = %q, want port 4096", daemon.DaemonHealthCmd())
	}
	if daemon.DaemonLogPath() != "/tmp/opencode-serve.log" {
		t.Errorf("DaemonLogPath = %q, want /tmp/opencode-serve.log", daemon.DaemonLogPath())
	}
	if hint := daemon.DaemonAuthHint(); !strings.Contains(hint, "OPENCODE_SERVER_PASSWORD") {
		t.Errorf("DaemonAuthHint = %q, want mention of OPENCODE_SERVER_PASSWORD", hint)
	}
	if !strings.Contains(daemon.DaemonStartCmd(false), daemon.DaemonLogPath()) {
		t.Errorf("DaemonStartCmd must redirect to DaemonLogPath, got %q", daemon.DaemonStartCmd(false))
	}
}

func TestOpencodeDaemonHealthParse(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	daemon := mustDaemon(t, a)
	ok, err := daemon.DaemonHealthParse(`{"healthy":true,"version":"1.0.0"}`)
	if err != nil || !ok {
		t.Errorf("DaemonHealthParse(healthy) = %v, %v, want true, nil", ok, err)
	}
	notOK, err := daemon.DaemonHealthParse(`{"healthy":false}`)
	if err != nil || notOK {
		t.Errorf("DaemonHealthParse(unhealthy) = %v, %v, want false, nil", notOK, err)
	}
	if _, err := daemon.DaemonHealthParse("not json"); err == nil {
		t.Error("DaemonHealthParse(invalid) should error")
	}
}

func TestOpencodeWorktreeListCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	if !strings.Contains(worktree.WorktreeListCmd("/workspace"), "/experimental/worktree") {
		t.Errorf("WorktreeListCmd = %q", worktree.WorktreeListCmd("/workspace"))
	}
	if got := worktree.WorktreeListCmd(
		"/workspace/my notes",
	); got != "curl -sf http://127.0.0.1:4096/experimental/worktree" {
		t.Errorf("WorktreeListCmd = %q, want the global v1 route", got)
	}
}

func TestOpencodeWorktreeCreateCmdWithBase(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "f", Base: "main"})
	if !strings.Contains(got, `"startCommand":"git reset --hard main"`) {
		t.Errorf("WorktreeCreateCmd(with base) = %q", got)
	}
}

// TestOpencodeWorktreeCreateCmdIgnoresTarget pins that v1's global route does
// not read the launch directory: the command is identical whatever the target.
func TestOpencodeWorktreeCreateCmdIgnoresTarget(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	withoutTarget := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x", Base: "main"})
	withTarget := worktree.WorktreeCreateCmd(
		agent.WorktreeSpec{Name: "feat-x", Base: "main", Target: "/workspace/my notes"},
	)
	if withTarget != withoutTarget {
		t.Errorf("WorktreeCreateCmd changed with the target: %q != %q", withTarget, withoutTarget)
	}
}

// TestOpencodeWorktreeNotDetached pins that v1 keeps its worktrees on a branch,
// so the launcher never reports a detached HEAD for opencode.
func TestOpencodeWorktreeNotDetached(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if _, ok := agent.AsDetachedWorktreeReporter(a); ok {
		t.Error("opencode must not implement DetachedWorktreeReporter (v1 keeps a branch)")
	}
}

func TestOpencodeWorktreeParseDir(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	dir, ok := worktree.WorktreeParseDir(`{"directory":"/home/dev/ws"}`)
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

func TestOpencodeLatestVersionCancelledCtx(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("opencode should implement UpgradeChecker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestOpencodeNewerThan(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	checker := mustChecker(t, a)
	gt, err := checker.NewerThan("1.2.0", "1.1.0")
	if err != nil || !gt {
		t.Errorf("NewerThan(1.2.0, 1.1.0) = %v, %v, want true, nil", gt, err)
	}
	lt, err := checker.NewerThan("1.0.0", "1.1.0")
	if err != nil || lt {
		t.Errorf("NewerThan(1.0.0, 1.1.0) = %v, %v, want false, nil", lt, err)
	}
	if _, err := checker.NewerThan("notaversion", "1.0.0"); err == nil {
		t.Error("NewerThan(invalid) should error")
	}
}

func TestOpencodeConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("opencode should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "opencode*.json*" {
		t.Errorf("SnippetPattern = %q", merger.SnippetPattern())
	}
	if merger.VMConfigPath("/home/user") != filepath.Join("/home/user", ".config", "opencode", "opencode.jsonc") {
		t.Errorf("VMConfigPath = %q", merger.VMConfigPath("/home/user"))
	}
}

func TestOpencodeConfigFileNames(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("opencode should implement ConfigMerger")
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

func TestOpencodeProvisionRules(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("opencode should implement Provisioner")
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

func mustDaemon(t *testing.T, a agent.Agent) agent.DaemonProvider {
	t.Helper()
	daemon, ok := agent.AsDaemonProvider(a)
	if !ok {
		t.Fatal("opencode should implement DaemonProvider")
	}
	return daemon
}

func mustWorktree(t *testing.T, a agent.Agent) agent.WorktreeProvider {
	t.Helper()
	worktree, ok := agent.AsWorktreeProvider(a)
	if !ok {
		t.Fatal("opencode should implement WorktreeProvider")
	}
	return worktree
}

func mustChecker(t *testing.T, a agent.Agent) agent.UpgradeChecker {
	t.Helper()
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("opencode should implement UpgradeChecker")
	}
	return checker
}

func TestOpencodeEventStream(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	provider, ok := agent.AsEventStreamProvider(a)
	if !ok {
		t.Fatal("opencode should implement EventStreamProvider")
	}
	got := provider.EventStream()
	if got.Parse == nil {
		t.Fatal("EventStream().Parse is nil")
	}
	got.Parse = nil
	want := agent.EventStreamSpec{
		StreamCommand:      "curl -N -s http://127.0.0.1:4096/global/event",
		BusyEvents:         []string{"message.part.updated", "session.updated"},
		AwaitingInput:      []string{"permission.asked", "question.asked"},
		IdleEvents:         []string{"session.idle"},
		ErrorEvents:        []string{"session.error"},
		StickyErrorEvents:  []string{"session.error"},
		SessionEvents:      []string{"session.created", "session.updated"},
		SessionListCommand: "curl -sf -m 30 http://127.0.0.1:4096/session | jq -c '[.[] | select(.parentID != null) | .id]'",
		Name:               "opencode",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EventStream() = %+v, want %+v", got, want)
	}
}

func TestOpencodeImplementsSessionStatusProvider(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	p, ok := agent.AsSessionStatusProvider(a)
	if !ok {
		t.Fatal("opencode should implement SessionStatusProvider")
	}
	if got, want := p.SessionStatusCmd(), "curl -sf http://127.0.0.1:4096/session/status"; got != want {
		t.Errorf("SessionStatusCmd() = %q, want %q", got, want)
	}
	if got, want := p.QuestionListCmd(), "curl -sf http://127.0.0.1:4096/question"; got != want {
		t.Errorf("QuestionListCmd() = %q, want %q", got, want)
	}
}

func TestPiLacksSessionStatusProvider(t *testing.T) {
	a, _ := agent.Lookup("pi")
	if _, ok := agent.AsSessionStatusProvider(a); ok {
		t.Error("pi should not implement SessionStatusProvider")
	}
}
