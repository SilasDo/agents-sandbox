package vm

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// mustWorktreeProvider returns the opencode profile's WorktreeProvider so tests
// can build the exact worktree commands the production path shells out with.
func mustWorktreeProvider(t *testing.T) agent.WorktreeProvider {
	t.Helper()
	provider, ok := agent.AsWorktreeProvider(opencodeAgent(t))
	if !ok {
		t.Fatal("opencode agent does not implement WorktreeProvider")
	}
	return provider
}

// recordingWorktreeAgent records the target and spec ResolveTarget hands to the
// provider, so the seam contract is pinned without a real agent.
type recordingWorktreeAgent struct {
	fakeAgent

	listTarget string
	createSpec agent.WorktreeSpec
}

func (r *recordingWorktreeAgent) WorktreeListCmd(target string) string {
	r.listTarget = target
	return "list"
}

func (r *recordingWorktreeAgent) WorktreeCreateCmd(spec agent.WorktreeSpec) string {
	r.createSpec = spec
	return "create"
}

func (r *recordingWorktreeAgent) WorktreeParseDir(string) (string, bool) {
	return "/workspace/wt/feat-x", true
}

func TestResolveTargetPassesTargetToProvider(t *testing.T) {
	ui := &termio.Mock{}
	a := &recordingWorktreeAgent{}
	sb := &msb.MockSandbox{ShellOut: map[string]msb.ShellResult{
		"list":   msb.NewTestResult(true, 0, `[]`, "", nil),
		"create": msb.NewTestResult(true, 0, `{"directory":"/workspace/wt/feat-x"}`, "", nil),
	}}
	dir, err := ResolveTarget(
		context.Background(),
		a,
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/workspace/wt/feat-x" {
		t.Errorf("got dir %q, want /workspace/wt/feat-x", dir)
	}
	if a.listTarget != mounts.WorkspaceMountPath {
		t.Errorf("list target = %q, want %q", a.listTarget, mounts.WorkspaceMountPath)
	}
	if a.createSpec.Target != mounts.WorkspaceMountPath || a.createSpec.Name != "feat-x" {
		t.Errorf("create spec = %+v, want Target %q and Name feat-x", a.createSpec, mounts.WorkspaceMountPath)
	}
}

func TestResolveTargetEmptySpecReturnsWorkspace(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellCalls: &[]string{}}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/workspace" {
		t.Errorf("expected /workspace, got %q", dir)
	}
	if len(*sb.ShellCalls) != 0 {
		t.Errorf("expected no shell calls for empty spec, got %v", *sb.ShellCalls)
	}
}

func TestResolveTargetNoDaemonProvider(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellCalls: &[]string{}}
	dir, err := ResolveTarget(
		context.Background(),
		&fakeAgent{},
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/workspace" {
		t.Errorf("expected /workspace for an agent without a DaemonProvider, got %q", dir)
	}
	if len(*sb.ShellCalls) != 0 {
		t.Errorf("expected no shell calls for an agent without a DaemonProvider, got %v", *sb.ShellCalls)
	}
}

func TestResolveTargetReusesExistingWorktree(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0,
				`["/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "bugfix-exit-zero"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST") {
			t.Errorf("expected reuse without create, but created a new worktree: %q", call)
		}
	}
}

func TestResolveTargetReuseWarnsIgnoredBase(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0,
				`["/home/dev/.local/share/opencode/worktree/abc/foo"]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	_, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "foo", Base: "main"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "ignoring base") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning that base is ignored on reuse; got: %v", ui.WarnCalls)
	}
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST") {
			t.Errorf("expected reuse without create, but created a new worktree: %q", call)
		}
	}
}

// TestResolveTargetReuseIgnoresUnresolvableBase pins the ordering constraint:
// the base is validated only on the create path, never on reuse. The failing
// ExecOut entry is never consulted under the correct ordering, so the call
// succeeds; if validation were moved ahead of the reuse check it would fail.
func TestResolveTargetReuseIgnoresUnresolvableBase(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	existing := "/home/dev/.local/share/opencode/worktree/abc/foo"
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0,
				`["`+existing+`"]`, "", nil),
		},
		ExecOut: map[string]msb.ShellResult{
			"git -C /workspace rev-parse --verify main^{commit}": msb.NewTestResult(
				false,
				128,
				"",
				"unknown revision",
				nil,
			),
		},
		ShellCalls: &[]string{},
		ExecCalls:  &[]string{},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "foo", Base: "main"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("reuse must ignore an unresolvable base, got error: %v", err)
	}
	if dir != existing {
		t.Errorf("got dir %q, want reused worktree %q", dir, existing)
	}
	if len(*sb.ExecCalls) != 0 {
		t.Errorf("reuse must not validate the base; exec calls: %v", *sb.ExecCalls)
	}
	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "ignoring base") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning that base is ignored on reuse; got: %v", ui.WarnCalls)
	}
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST") {
			t.Errorf("expected reuse without create, but created a new worktree: %q", call)
		}
	}
}

func TestResolveTargetCreatesWithoutBase(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}): msb.NewTestResult(true, 0,
				`{"directory":"/workspace/worktrees/feat-x"}`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/workspace/worktrees/feat-x"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
}

func TestResolveTargetCreatesWithBaseValidatesAndSendsStartCommand(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x", Base: "main"})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd: msb.NewTestResult(
				true,
				0,
				`{"directory":"/workspace/worktrees/feat-x"}`,
				"",
				nil,
			),
		},
		ExecOut: map[string]msb.ShellResult{
			"git -C /workspace rev-parse --verify main^{commit}": msb.NewTestResult(
				true,
				0,
				"abc123",
				"",
				nil,
			),
		},
		ShellCalls: &[]string{},
		ExecCalls:  &[]string{},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x", Base: "main"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/workspace/worktrees/feat-x"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
	if !slices.Contains(*sb.ExecCalls, "git -C /workspace rev-parse --verify main^{commit}") {
		t.Errorf("expected base validated against the launch directory; exec calls: %v", *sb.ExecCalls)
	}
	found := false
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, `git reset --hard main`) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected create body to carry startCommand reset; got: %v", *sb.ShellCalls)
	}
}

func TestResolveTargetCreateFailsOnUnresolvableBase(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x", Base: "nope"})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd: msb.NewTestResult(
				true,
				0,
				`{"directory":"/workspace/worktrees/feat-x"}`,
				"",
				nil,
			),
		},
		ExecOut: map[string]msb.ShellResult{
			"git -C /workspace rev-parse --verify nope^{commit}": msb.NewTestResult(
				false,
				128,
				"",
				"unknown revision",
				nil,
			),
		},
		ShellCalls: &[]string{},
	}
	if _, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x", Base: "nope"},
		mounts.WorkspaceMountPath,
		ui,
	); err == nil {
		t.Error("expected error for unresolvable base")
	}
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST") {
			t.Errorf("expected no create request for an unresolvable base: %q", call)
		}
	}
}

// opencode2Agent returns the opencode2 profile for tests exercising v2's
// agent-specific worktree behavior.
func opencode2Agent(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode2")
	if !ok {
		t.Fatal("opencode2 agent not registered")
	}
	return a
}

// infoCallContains reports whether any recorded info message contains every part.
func infoCallContains(calls []string, parts ...string) bool {
	for _, call := range calls {
		matched := true
		for _, part := range parts {
			if !strings.Contains(call, part) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// TestResolveTargetCreateReportsWorktree pins that a fresh create is announced
// at the default-visible level, naming the worktree and its directory.
func TestResolveTargetCreateReportsWorktree(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}): msb.NewTestResult(true, 0,
				`{"directory":"/workspace/worktrees/feat-x"}`, "", nil),
		},
	}
	if _, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !infoCallContains(ui.InfoCalls, "created worktree", "feat-x", "/workspace/worktrees/feat-x") {
		t.Errorf("expected a default-visible create message naming the directory; got: %v", ui.InfoCalls)
	}
}

// TestResolveTargetReuseReportsWorktree pins that reuse is announced at the
// default-visible level, naming the reused directory.
func TestResolveTargetReuseReportsWorktree(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	dir := "/home/dev/.local/share/opencode/worktree/abc/feat-x"
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0,
				`["`+dir+`"]`, "", nil),
		},
	}
	if _, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !infoCallContains(ui.InfoCalls, "reusing existing worktree", "feat-x", dir) {
		t.Errorf("expected a default-visible reuse message naming the directory; got: %v", ui.InfoCalls)
	}
}

// TestResolveTargetOpencode2ReportsDetachedHead pins that opencode2's detached
// HEAD (v2 never leaves a worktree on a branch) is surfaced with the recovery.
func TestResolveTargetOpencode2ReportsDetachedHead(t *testing.T) {
	ui := &termio.Mock{}
	a := opencode2Agent(t)
	provider, ok := agent.AsWorktreeProvider(a)
	if !ok {
		t.Fatal("opencode2 does not implement WorktreeProvider")
	}
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(agent.WorktreeSpec{
				Name:   "feat-x",
				Target: mounts.WorkspaceMountPath,
			}): msb.NewTestResult(true, 0,
				`{"directory":"/home/dev/.local/share/opencode/worktree/abc/feat-x"}`, "", nil),
		},
	}
	if _, err := ResolveTarget(
		context.Background(),
		a,
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !infoCallContains(ui.InfoCalls, "detached HEAD", "git switch -c <branch>") {
		t.Errorf("expected the create message to flag the detached HEAD and recovery; got: %v", ui.InfoCalls)
	}
}

// TestResolveTargetOpencode2ReuseDoesNotClaimDetachedHead pins that the
// detached-HEAD note stays on the create path. A reused worktree's checkout
// state is unknown — the user may already have run `git switch -c <branch>`
// after the create-time note — so reuse must not restate the claim.
func TestResolveTargetOpencode2ReuseDoesNotClaimDetachedHead(t *testing.T) {
	ui := &termio.Mock{}
	a := opencode2Agent(t)
	provider, ok := agent.AsWorktreeProvider(a)
	if !ok {
		t.Fatal("opencode2 does not implement WorktreeProvider")
	}
	dir := "/home/dev/.local/share/opencode/worktree/abc/feat-x"
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0,
				`[{"directory":"`+dir+`"}]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	got, err := ResolveTarget(
		context.Background(),
		a,
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Errorf("got dir %q, want the reused %q", got, dir)
	}
	if !infoCallContains(ui.InfoCalls, "reusing existing worktree", "feat-x", dir) {
		t.Errorf("expected a default-visible reuse message naming the directory; got: %v", ui.InfoCalls)
	}
	for _, msg := range append(append([]string{}, ui.InfoCalls...), ui.WarnCalls...) {
		if strings.Contains(msg, "detached") {
			t.Errorf("reuse must not claim a detached HEAD: %q", msg)
		}
	}
}

// TestResolveTargetOpencodeDoesNotReportDetachedHead pins that v1 (which keeps a
// branch) never claims a detached HEAD.
func TestResolveTargetOpencodeDoesNotReportDetachedHead(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}): msb.NewTestResult(true, 0,
				`{"directory":"/workspace/worktrees/feat-x"}`, "", nil),
		},
	}
	if _, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, msg := range append(append([]string{}, ui.InfoCalls...), ui.WarnCalls...) {
		if strings.Contains(msg, "detached") {
			t.Errorf("opencode v1 must not report a detached HEAD: %q", msg)
		}
	}
}

// TestResolveTargetWarnsOnRenamedWorktree pins that a daemon-returned directory
// whose base name differs from the requested name (the -2 auto-suffix) is
// surfaced, naming the directory actually used.
func TestResolveTargetWarnsOnRenamedWorktree(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(mounts.WorkspaceMountPath): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}): msb.NewTestResult(true, 0,
				`{"directory":"/workspace/worktrees/feat-x-2"}`, "", nil),
		},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/workspace/worktrees/feat-x-2" {
		t.Errorf("got dir %q, want the daemon-returned /workspace/worktrees/feat-x-2", dir)
	}
	if !infoCallContains(ui.WarnCalls, "feat-x", "/workspace/worktrees/feat-x-2") {
		t.Errorf("expected a warning naming the renamed directory; got: %v", ui.WarnCalls)
	}
}

func TestFindWorktreeDirStringList(t *testing.T) {
	list := `[
		"/home/dev/.local/share/opencode/worktree/abc/review-test",
		"/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"
	]`
	dir, ok := findWorktreeDir(list, "bugfix-exit-zero")
	if !ok {
		t.Fatal("expected to find existing worktree for slug bugfix-exit-zero")
	}
	if want := "/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
}

func TestFindWorktreeDirObjectList(t *testing.T) {
	list := `[
		{"name":"bugfix-exit-zero","branch":"opencode/bugfix-exit-zero","directory":"/root/bugfix-exit-zero"}
	]`
	dir, ok := findWorktreeDir(list, "bugfix-exit-zero")
	if !ok {
		t.Fatal("expected to find worktree from object list")
	}
	if want := "/root/bugfix-exit-zero"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
}

func TestFindWorktreeDirNoMatch(t *testing.T) {
	list := `["/home/dev/.local/share/opencode/worktree/abc/other-topic"]`
	if _, ok := findWorktreeDir(list, "bugfix-exit-zero"); ok {
		t.Error("expected no match for a slug absent from the list")
	}
}

func TestFindWorktreeDirEmptyList(t *testing.T) {
	if _, ok := findWorktreeDir("", "bugfix-exit-zero"); ok {
		t.Error("expected no match for an empty list response")
	}
}

func TestValidateWorktreeBaseOK(t *testing.T) {
	sb := &msb.MockSandbox{ExecOut: map[string]msb.ShellResult{
		"git -C /w/feat rev-parse --verify main^{commit}": msb.NewTestResult(true, 0, "abc123", "", nil),
	}}
	if err := validateWorktreeBase(context.Background(), sb, "/w/feat", "main"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWorktreeBaseMissing(t *testing.T) {
	sb := &msb.MockSandbox{ExecOut: map[string]msb.ShellResult{
		"git -C /w/feat rev-parse --verify nope^{commit}": msb.NewTestResult(false, 128, "", "unknown revision", nil),
	}}
	if err := validateWorktreeBase(context.Background(), sb, "/w/feat", "nope"); err == nil {
		t.Error("expected error for unresolvable base")
	}
}
