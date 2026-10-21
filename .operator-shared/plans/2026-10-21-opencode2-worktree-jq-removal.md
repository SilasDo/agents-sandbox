# opencode2 Worktree jq Removal Implementation Plan

Status: implemented on `fix/opencode2-worktree-jq` (PR #261, pending merge). Both tasks landed in one
commit; the whole-branch review found only minors, all addressed (resolve-call count + resolve transport
failure tests, the extra parser fixtures the deleted jq-filter test pinned, and small wording/formatting).

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `agents-sandbox run --agent opencode2 --worktree <name>` work on a custom runner base that has no `jq`, by moving all daemon-JSON handling on the worktree path out of guest shell commands and into Go.

**Architecture:** The `agent.WorktreeProvider` seam changes from one merged shell command per operation to explicit steps (resolve → list → create), because v2's project id comes from a separate `/api/location` response that a jq-free shell cannot extract. Each profile decodes its own daemon's JSON in Go: v2 resolves the project, drops the canonical root, and builds the create body in Go; the shared reuse match (`findWorktreeDir`) stays in `vm` over a normalized directory list. No guest command uses `jq` afterwards.

**Tech Stack:** Go 1.26, `curl` in the guest (unchanged and out of scope), stdlib `encoding/json`, `golangci-lint`.

**Spec:** `.operator-shared/specs/opencode2-worktree-api.md`

## Global Constraints

- Module `github.com/inoio/agents-sandbox`, Go 1.26.
- `make fmt` / `golangci-lint fmt` to format (never `go fmt`); `make lint` (never `go vet`).
- TDD: write the failing test, run it, implement, run it green, commit.
- No guest worktree command may contain `jq` after this change.
- Every v2 request keeps the baked credential (`opencode2CurlAuth`, `-u opencode:opencode`) and the bounded flags (`opencode2CurlFlags`, `-fsS -m 30`).
- The v2 create body stays exactly `Worktree.CreateInput` (`additionalProperties: false`): only `projectID`, `name`, and an optional `branch` — never `strategy`, `directory`, `from`, or `startCommand`.
- `opencode` (v1) keeps its routes and create body; the only v1 behavior change is that a malformed list payload now aborts instead of reading as "no worktrees".
- `curl` remains a guest prerequisite; do not remove or replace it in this plan.
- Verified against `opencode v2.0.32` (the pinned version); do not change the pin.

## Review Focus

- A worktree name equal to the repository directory name → the main checkout is never reused (`project.canonical` is dropped from the list).
- An exit-0 but malformed worktree list (renamed/blanked/retyped `directory`, a non-array payload, `null`) → aborts before creating, for **both** providers; it never reads as "no worktrees" and never creates a second worktree.
- `/api/location` exits non-zero, or returns a missing/empty `id` or `canonical` → aborts before the list, and no request is ever sent with an empty `projectID`.
- A worktree name or base ref containing shell metacharacters (spaces, single quotes) → correctly JSON-encoded in the Go-built body and shell-quoted as the whole `-d` argument.
- A custom base with no `jq` → `--worktree` on `opencode2` issues no `jq` call; the only external tool it needs is `curl`.

---

### Task 1: Remove the guest `jq` dependency from the worktree path

**Files:**
- Modify: `internal/agent/capabilities.go` (`WorktreeProvider`, add `WorktreeProject`)
- Modify: `internal/agent/agent.go` (`WorktreeSpec`: drop `Target`)
- Modify: `internal/agent/opencode.go`
- Modify: `internal/agent/opencode2.go`
- Modify: `internal/sandbox/vm/worktree.go` (`ResolveTarget`, `findWorktreeDir`)
- Delete: `internal/agent/opencode2_worktree_filter_test.go`
- Test: `internal/agent/opencode_test.go`, `internal/agent/opencode2_test.go`, `internal/sandbox/vm/worktree_test.go`, `internal/sandbox/vm/resolve_target_test.go`, `internal/sandbox/vm/coverage_more_test.go`

**Interfaces:**
- Consumes: `opencode2CurlFlags`, `opencode2CurlAuth`, `opencode2ServerURL`, `shellQuote` (all existing in `internal/agent`).
- Produces: `agent.WorktreeProject{ID, Canonical string}`.
- Produces the interface (exact):

```go
type WorktreeProvider interface {
	WorktreeResolveCmd(target string) string
	WorktreeParseProject(stdout string) (WorktreeProject, bool)
	WorktreeListCmd(project WorktreeProject) string
	WorktreeParseList(stdout string, project WorktreeProject) ([]string, bool)
	WorktreeCreateCmd(spec WorktreeSpec, project WorktreeProject) string
	WorktreeParseDir(stdout string) (string, bool)
}
```

- Produces: `agent.WorktreeSpec{Name, Base string}` (no `Target`).
- Produces: `vm.findWorktreeDir(dirs []string, slug string) (string, bool)`; `vm.resolveWorktreeProject(ctx, sb, provider, target) (agent.WorktreeProject, error)`.
- Removes: `opencode2LocatePrelude`, `opencode2WorktreeListFilter`, and the `Target` field.

- [ ] **Step 1: Write the failing v1 provider tests**

In `internal/agent/opencode_test.go`, replace `TestOpencodeWorktreeListCmd` and `TestOpencodeWorktreeCreateCmdIgnoresTarget`, and add parser/resolve tests:

```go
func TestOpencodeWorktreeHasNoResolveStep(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	if got := worktree.WorktreeResolveCmd("/workspace"); got != "" {
		t.Errorf("WorktreeResolveCmd = %q, want empty (v1 routes are global)", got)
	}
	if got := worktree.WorktreeListCmd(agent.WorktreeProject{}); got != "curl -sf http://127.0.0.1:4096/experimental/worktree" {
		t.Errorf("WorktreeListCmd = %q, want the global v1 route", got)
	}
}

func TestOpencodeWorktreeParseList(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	cases := []struct {
		name string
		in   string
		want []string
		ok   bool
	}{
		{name: "string paths", in: `["/wt/a","/wt/b"]`, want: []string{"/wt/a", "/wt/b"}, ok: true},
		{name: "objects", in: `[{"directory":"/wt/a"}]`, want: []string{"/wt/a"}, ok: true},
		{name: "empty array", in: `[]`, want: []string{}, ok: true},
		{name: "non-array object", in: `{"directory":"/wt/a"}`, ok: false},
		{name: "null", in: `null`, ok: false},
		{name: "empty input", in: ``, ok: false},
		{name: "invalid json", in: `not json`, ok: false},
		{name: "renamed field", in: `[{"path":"/wt/a"}]`, ok: false},
		{name: "blank directory", in: `[{"directory":""}]`, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := worktree.WorktreeParseList(tc.in, agent.WorktreeProject{})
			if ok != tc.ok || !slices.Equal(got, tc.want) {
				t.Errorf("WorktreeParseList(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}
```

Update the two existing create tests to the new signature by passing `agent.WorktreeProject{}` (their expected strings are unchanged):
`worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature/x"}, agent.WorktreeProject{})`.

- [ ] **Step 2: Write the failing v2 provider tests**

In `internal/agent/opencode2_test.go`, replace `TestOpencode2WorktreeListCmd`, `TestOpencode2WorktreeCurlsBoundedAndKeepStderr`, `TestOpencode2WorktreeCreateCmd`, `TestOpencode2WorktreeCreateCmdWithBase`, `TestOpencode2WorktreeCreateCmdQuotesTargetAndBase`, and `TestOpencode2WorktreePreludeGuardsFailClosed` with:

```go
func TestOpencode2WorktreeResolveCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeResolveCmd("/workspace")
	want := "curl -fsS -m 30 -u opencode:opencode --get --data-urlencode 'location[directory]=/workspace' http://127.0.0.1:4096/api/location"
	if got != want {
		t.Errorf("WorktreeResolveCmd = %q, want %q", got, want)
	}
}

func TestOpencode2WorktreeListCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeListCmd(agent.WorktreeProject{ID: "abc"})
	want := "curl -fsS -m 30 -u opencode:opencode http://127.0.0.1:4096/api/worktree?projectID='abc'"
	if got != want {
		t.Errorf("WorktreeListCmd = %q, want %q", got, want)
	}
}

func TestOpencode2WorktreeCreateCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature-x"}, agent.WorktreeProject{ID: "abc"})
	want := `curl -fsS -m 30 -u opencode:opencode -X POST http://127.0.0.1:4096/api/worktree -H 'Content-Type: application/json' -d '{"projectID":"abc","name":"feature-x"}'`
	if got != want {
		t.Errorf("WorktreeCreateCmd = %q, want %q", got, want)
	}
}

func TestOpencode2WorktreeCreateCmdWithBase(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature-x", Base: "main"}, agent.WorktreeProject{ID: "abc"})
	want := `curl -fsS -m 30 -u opencode:opencode -X POST http://127.0.0.1:4096/api/worktree -H 'Content-Type: application/json' -d '{"projectID":"abc","name":"feature-x","branch":"main"}'`
	if got != want {
		t.Errorf("WorktreeCreateCmd(with base) = %q, want %q", got, want)
	}
}

// TestOpencode2WorktreeCreateCmdQuotesJSON pins that a base ref with a single
// quote is JSON-encoded and the whole -d argument single-quoted, so no shell
// expansion or quoting bug reaches the daemon.
func TestOpencode2WorktreeCreateCmdQuotesJSON(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x", Base: "it's"}, agent.WorktreeProject{ID: "abc"})
	if !strings.Contains(got, `-d '{"projectID":"abc","name":"feat-x","branch":"it'\''s"}'`) {
		t.Errorf("create body not shell-quoted as expected: %q", got)
	}
}

// TestOpencode2WorktreeCommandsHaveNoJq is the point of the change: a custom
// base without jq must work, so no worktree command may invoke jq.
func TestOpencode2WorktreeCommandsHaveNoJq(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	commands := map[string]string{
		"resolve": worktree.WorktreeResolveCmd("/workspace"),
		"list":    worktree.WorktreeListCmd(agent.WorktreeProject{ID: "abc"}),
		"create":  worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature-x"}, agent.WorktreeProject{ID: "abc"}),
	}
	for name, cmd := range commands {
		if strings.Contains(cmd, "jq") {
			t.Errorf("%s command still requires jq: %q", name, cmd)
		}
	}
}

func TestOpencode2WorktreeCurlsBoundedAndKeepStderr(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	commands := map[string]string{
		"resolve": worktree.WorktreeResolveCmd("/workspace"),
		"list":    worktree.WorktreeListCmd(agent.WorktreeProject{ID: "abc"}),
		"create":  worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature-x"}, agent.WorktreeProject{ID: "abc"}),
	}
	for name, cmd := range commands {
		if !strings.HasPrefix(cmd, "curl -fsS -m 30 -u opencode:opencode ") {
			t.Errorf("%s command must lead with the bounded, authenticated curl: %q", name, cmd)
		}
	}
}

func TestParseOpencode2WorktreeProject(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	cases := []struct {
		name string
		in   string
		want agent.WorktreeProject
		ok   bool
	}{
		{name: "valid", in: `{"project":{"id":"abc","canonical":"/repo"}}`, want: agent.WorktreeProject{ID: "abc", Canonical: "/repo"}, ok: true},
		{name: "empty id", in: `{"project":{"id":"","canonical":"/repo"}}`, ok: false},
		{name: "empty canonical", in: `{"project":{"id":"abc","canonical":""}}`, ok: false},
		{name: "missing project", in: `{}`, ok: false},
		{name: "invalid json", in: `not json`, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := worktree.WorktreeParseProject(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Errorf("WorktreeParseProject(%q) = %+v, %v; want %+v, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestParseOpencode2WorktreeList(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	project := agent.WorktreeProject{ID: "abc", Canonical: "/repo"}
	cases := []struct {
		name string
		in   string
		want []string
		ok   bool
	}{
		{name: "drops canonical", in: `[{"directory":"/repo"},{"directory":"/wt/feat-x"}]`, want: []string{"/wt/feat-x"}, ok: true},
		{name: "server-suffixed survives", in: `[{"directory":"/repo"},{"directory":"/wt/feat-x-2"}]`, want: []string{"/wt/feat-x-2"}, ok: true},
		{name: "canonical only", in: `[{"directory":"/repo"}]`, want: []string{}, ok: true},
		{name: "empty array", in: `[]`, want: []string{}, ok: true},
		{name: "non-array object", in: `{"directory":"/repo"}`, ok: false},
		{name: "null", in: `null`, ok: false},
		{name: "renamed field", in: `[{"directory":"/repo"},{"path":"/wt/feat-x"}]`, ok: false},
		{name: "string entry", in: `["/wt/feat-x"]`, ok: false},
		{name: "blank directory", in: `[{"directory":""}]`, ok: false},
		{name: "numeric directory", in: `[{"directory":123}]`, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := worktree.WorktreeParseList(tc.in, project)
			if ok != tc.ok || !slices.Equal(got, tc.want) {
				t.Errorf("WorktreeParseList(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}
```

Add `"slices"` to the test file's imports if absent. Delete `internal/agent/opencode2_worktree_filter_test.go` (its jq-executing fixtures are now covered by `TestParseOpencode2WorktreeList`).

- [ ] **Step 3: Write the failing vm orchestration tests**

In `internal/sandbox/vm/worktree_test.go`, replace `recordingWorktreeAgent` and `TestResolveTargetPassesTargetToProvider`:

```go
type recordingWorktreeAgent struct {
	fakeAgent
	resolveTarget   string
	badProject      bool
	listProject     agent.WorktreeProject
	createSpec      agent.WorktreeSpec
	createProject   agent.WorktreeProject
}

func (r *recordingWorktreeAgent) WorktreeResolveCmd(target string) string {
	r.resolveTarget = target
	return "resolve"
}

func (r *recordingWorktreeAgent) WorktreeParseProject(string) (agent.WorktreeProject, bool) {
	if r.badProject {
		return agent.WorktreeProject{}, false
	}
	return agent.WorktreeProject{ID: "abc", Canonical: "/repo"}, true
}

func (r *recordingWorktreeAgent) WorktreeListCmd(project agent.WorktreeProject) string {
	r.listProject = project
	return "list"
}

func (r *recordingWorktreeAgent) WorktreeParseList(string, agent.WorktreeProject) ([]string, bool) {
	return []string{}, true
}

func (r *recordingWorktreeAgent) WorktreeCreateCmd(spec agent.WorktreeSpec, project agent.WorktreeProject) string {
	r.createSpec = spec
	r.createProject = project
	return "create"
}

func (r *recordingWorktreeAgent) WorktreeParseDir(string) (string, bool) {
	return "/workspace/wt/feat-x", true
}

func TestResolveTargetResolvesThenPassesProjectToProvider(t *testing.T) {
	ui := &termio.Mock{}
	a := &recordingWorktreeAgent{}
	sb := &msb.MockSandbox{ShellOut: map[string]msb.ShellResult{
		"resolve": msb.NewTestResult(true, 0, `{}`, "", nil),
		"list":    msb.NewTestResult(true, 0, `[]`, "", nil),
		"create":  msb.NewTestResult(true, 0, `{"directory":"/workspace/wt/feat-x"}`, "", nil),
	}}
	dir, err := ResolveTarget(
		context.Background(), a, sb,
		options.WorktreeSpec{Name: "feat-x"}, mounts.WorkspaceMountPath, ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/workspace/wt/feat-x" {
		t.Errorf("got dir %q, want /workspace/wt/feat-x", dir)
	}
	if a.resolveTarget != mounts.WorkspaceMountPath {
		t.Errorf("resolve target = %q, want %q", a.resolveTarget, mounts.WorkspaceMountPath)
	}
	want := agent.WorktreeProject{ID: "abc", Canonical: "/repo"}
	if a.listProject != want || a.createProject != want {
		t.Errorf("projects = list %+v, create %+v; want %+v", a.listProject, a.createProject, want)
	}
	if a.createSpec.Name != "feat-x" {
		t.Errorf("create spec = %+v, want Name feat-x", a.createSpec)
	}
}

func TestResolveTargetAbortsWhenResolveFails(t *testing.T) {
	ui := &termio.Mock{}
	a := &recordingWorktreeAgent{}
	sb := &msb.MockSandbox{
		ShellOut:   map[string]msb.ShellResult{"resolve": msb.NewTestResult(false, 1, "", "boom", nil)},
		ShellCalls: &[]string{},
	}
	if _, err := ResolveTarget(
		context.Background(), a, sb,
		options.WorktreeSpec{Name: "feat-x"}, mounts.WorkspaceMountPath, ui,
	); err == nil {
		t.Fatal("expected an error when the resolve command exits non-zero")
	}
	for _, call := range *sb.ShellCalls {
		if call == "list" || call == "create" {
			t.Errorf("must not list or create after a failed resolve; calls: %v", *sb.ShellCalls)
		}
	}
}

func TestResolveTargetAbortsWhenProjectUnparsable(t *testing.T) {
	ui := &termio.Mock{}
	a := &recordingWorktreeAgent{badProject: true}
	sb := &msb.MockSandbox{
		ShellOut:   map[string]msb.ShellResult{"resolve": msb.NewTestResult(true, 0, "garbage", "", nil)},
		ShellCalls: &[]string{},
	}
	if _, err := ResolveTarget(
		context.Background(), a, sb,
		options.WorktreeSpec{Name: "feat-x"}, mounts.WorkspaceMountPath, ui,
	); err == nil {
		t.Fatal("expected an error when the project cannot be parsed")
	}
	for _, call := range *sb.ShellCalls {
		if call == "list" || call == "create" {
			t.Errorf("must not list or create after an unparsable resolve; calls: %v", *sb.ShellCalls)
		}
	}
}

// TestResolveTargetV1SkipsResolveStep pins that an agent without a resolve
// command (v1) shells out only for the list and create.
func TestResolveTargetV1SkipsResolveStep(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(
				agent.WorktreeSpec{Name: "feat-x"}, agent.WorktreeProject{},
			): msb.NewTestResult(true, 0, `{"directory":"/workspace/wt/feat-x"}`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	if _, err := ResolveTarget(
		context.Background(), opencodeAgent(t), sb,
		options.WorktreeSpec{Name: "feat-x"}, mounts.WorkspaceMountPath, ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*sb.ShellCalls) != 2 {
		t.Errorf("v1 should shell out exactly twice (list, create); calls: %v", *sb.ShellCalls)
	}
}
```

- [ ] **Step 4: Run the new tests to verify they fail**

Run: `go test ./internal/agent/... ./internal/sandbox/vm/`
Expected: FAIL to compile — the new interface methods, `agent.WorktreeProject`, and the new signatures do not exist yet.

- [ ] **Step 5: Change the seam types**

In `internal/agent/capabilities.go`, add the project type and replace the `WorktreeProvider` interface with the six-method version from the Interfaces block. Document: the daemon's JSON is parsed in Go by the owning agent so no guest command needs `jq`; the steps are explicit because v2's routes are project-scoped (`WorktreeResolveCmd` is empty for global routes, which skip the step); `WorktreeParseList` returns a normalized directory list, already filtered of the canonical root, and `ok == false` on a bad payload so a failed list never reads as "no worktrees".

In `internal/agent/agent.go`, drop `Target` from `WorktreeSpec` (keep `Name`, `Base`, and the `//exhaustruct:ignore` marker).

- [ ] **Step 6: Implement the v1 provider**

In `internal/agent/opencode.go`, replace the three worktree methods:

```go
// WorktreeResolveCmd: v1's routes are global, so there is no resolution step.
func (opencodeProfile) WorktreeResolveCmd(string) string { return "" }

// WorktreeParseProject is never called (v1 has no resolve step).
func (opencodeProfile) WorktreeParseProject(string) (WorktreeProject, bool) {
	return WorktreeProject{}, false
}

// WorktreeListCmd lists worktrees via v1's global route.
func (opencodeProfile) WorktreeListCmd(WorktreeProject) string {
	return "curl -sf http://127.0.0.1:4096/experimental/worktree"
}

// WorktreeParseList decodes v1's list — path strings or {directory} objects —
// into directory paths. A non-array payload, or an entry without a non-empty
// string directory, is not ok, so a route or field rename never reads as "no
// worktrees".
func (opencodeProfile) WorktreeParseList(stdout string, _ WorktreeProject) ([]string, bool) {
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil || entries == nil {
		return nil, false
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		var directory string
		switch {
		case len(entry) > 0 && entry[0] == '"':
			if json.Unmarshal(entry, &directory) != nil {
				return nil, false
			}
		default:
			var info struct {
				Directory string `json:"directory"`
			}
			if json.Unmarshal(entry, &info) != nil {
				return nil, false
			}
			directory = info.Directory
		}
		if directory == "" {
			return nil, false
		}
		dirs = append(dirs, directory)
	}
	return dirs, true
}
```

Update `WorktreeCreateCmd` to `func (opencodeProfile) WorktreeCreateCmd(spec WorktreeSpec, _ WorktreeProject) string`, body unchanged.

The `entries == nil` check is required: `json.Unmarshal("null", …)` leaves a nil slice with no error, and must not read as an empty successful list.

- [ ] **Step 7: Implement the v2 provider**

In `internal/agent/opencode2.go`, delete `opencode2LocatePrelude` and `opencode2WorktreeListFilter`, and replace the worktree methods:

```go
// WorktreeResolveCmd resolves the launch directory to its project through v2's
// /api/location route, which registers the project as a side effect.
func (opencode2Profile) WorktreeResolveCmd(target string) string {
	return "curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " --get --data-urlencode " +
		shellQuote("location[directory]="+target) + " " + opencode2ServerURL + "/api/location"
}

// WorktreeParseProject decodes /api/location. Both the id and the canonical
// directory are required: an empty id would send an invalid projectID, and an
// empty canonical would filter the list against "" and keep the main checkout.
func parseOpencode2WorktreeProject(stdout string) (WorktreeProject, bool) {
	var location struct {
		Project struct {
			ID        string `json:"id"`
			Canonical string `json:"canonical"`
		} `json:"project"`
	}
	if err := json.Unmarshal([]byte(stdout), &location); err != nil {
		return WorktreeProject{}, false
	}
	if location.Project.ID == "" || location.Project.Canonical == "" {
		return WorktreeProject{}, false
	}
	return WorktreeProject{ID: location.Project.ID, Canonical: location.Project.Canonical}, true
}

func (opencode2Profile) WorktreeParseProject(stdout string) (WorktreeProject, bool) {
	return parseOpencode2WorktreeProject(stdout)
}

// WorktreeListCmd lists the project's worktrees. The canonical root is dropped
// by WorktreeParseList, not here.
func (opencode2Profile) WorktreeListCmd(project WorktreeProject) string {
	return "curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " " +
		opencode2ServerURL + "/api/worktree?projectID=" + shellQuote(project.ID)
}

// WorktreeParseList decodes /api/worktree into directories, dropping the
// canonical root. Every entry must carry a non-empty string directory: a
// renamed, blanked, or retyped field would otherwise be skipped by the
// base-name match and a second worktree created beside the existing one.
func parseOpencode2WorktreeList(stdout string, canonical string) ([]string, bool) {
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil || entries == nil {
		return nil, false
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		var info struct {
			Directory string `json:"directory"`
		}
		if json.Unmarshal(entry, &info) != nil || info.Directory == "" {
			return nil, false
		}
		if info.Directory == canonical {
			continue
		}
		dirs = append(dirs, info.Directory)
	}
	return dirs, true
}

func (opencode2Profile) WorktreeParseList(stdout string, project WorktreeProject) ([]string, bool) {
	return parseOpencode2WorktreeList(stdout, project.Canonical)
}

// worktreeCreateInput is v2's Worktree.CreateInput (additionalProperties:
// false): only projectID, name, and an optional branch.
type worktreeCreateInput struct {
	ProjectID string `json:"projectID"`
	Name      string `json:"name"`
	Branch    string `json:"branch,omitempty"`
}

// WorktreeCreateCmd creates a worktree. The body is built in Go and
// shell-quoted, so arbitrary names and refs need no guest tooling.
func (opencode2Profile) WorktreeCreateCmd(spec WorktreeSpec, project WorktreeProject) string {
	body, _ := json.Marshal(worktreeCreateInput{
		ProjectID: project.ID,
		Name:      spec.Name,
		Branch:    spec.Base,
	})
	return "curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " -X POST " + opencode2ServerURL + "/api/worktree " +
		"-H 'Content-Type: application/json' -d " + shellQuote(string(body))
}
```

Decoding each entry into a struct (rather than into a `[]{directory}` slice) is deliberate: it makes a string entry, an array entry, or a numeric `directory` fail the whole list.

- [ ] **Step 8: Orchestrate the steps in `vm.ResolveTarget`**

In `internal/sandbox/vm/worktree.go`, replace `findWorktreeDir` with the slice form and add the resolver:

```go
// findWorktreeDir returns the first directory whose base name matches slug.
func findWorktreeDir(dirs []string, slug string) (string, bool) {
	for _, dir := range dirs {
		if filepath.Base(dir) == slug {
			return dir, true
		}
	}
	return "", false
}

// resolveWorktreeProject resolves the launch directory to the daemon project the
// worktree routes are scoped to. An agent whose routes are global has no resolve
// command and returns the zero project without shelling out.
func resolveWorktreeProject(
	ctx context.Context, sb msb.Sandbox, provider agent.WorktreeProvider, target string,
) (agent.WorktreeProject, error) {
	command := provider.WorktreeResolveCmd(target)
	if command == "" {
		return agent.WorktreeProject{}, nil
	}
	out, err := sb.Shell(ctx, command)
	if err != nil {
		return agent.WorktreeProject{}, fmt.Errorf("resolve project for %q: %w", target, err)
	}
	if !out.Success() {
		return agent.WorktreeProject{}, fmt.Errorf(
			"resolve project for %q failed (exit %d): %s", target, out.ExitCode(), out.Stderr(),
		)
	}
	project, ok := provider.WorktreeParseProject(out.Stdout())
	if !ok {
		return agent.WorktreeProject{}, fmt.Errorf("resolve project for %q returned an unexpected payload", target)
	}
	return project, nil
}
```

In `ResolveTarget`, after the empty-name early return, resolve the project and use it:

```go
	project, err := resolveWorktreeProject(ctx, sb, provider, defaultDir)
	if err != nil {
		return "", fmt.Errorf("worktree %q: %w", spec.Name, err)
	}
```

Then: `provider.WorktreeListCmd(project)`; after the success check, add

```go
	dirs, ok := provider.WorktreeParseList(listOut.Stdout(), project)
	if !ok {
		return "", fmt.Errorf("list worktrees for %q returned an unexpected payload", spec.Name)
	}
	if dir, ok := findWorktreeDir(dirs, spec.Name); ok {
```

and change the create to

```go
	createSpec := agent.WorktreeSpec{Name: spec.Name, Base: spec.Base}
	out, err := sb.Shell(ctx, provider.WorktreeCreateCmd(createSpec, project))
```

Keep the existing list/create/base error messages and the reuse and detached-HEAD reporting unchanged. Update `ResolveTarget`'s doc comment: it now resolves the project first (v2) and the list/create reuse it.

- [ ] **Step 9: Update the remaining test call sites**

- `internal/sandbox/vm/worktree_test.go`, `resolve_target_test.go`, `coverage_more_test.go`: for v1 providers, `provider.WorktreeListCmd(mounts.WorkspaceMountPath)` → `provider.WorktreeListCmd(agent.WorktreeProject{})`, and `provider.WorktreeCreateCmd(agent.WorktreeSpec{…})` → add `, agent.WorktreeProject{}`.
- The two opencode2 vm tests (`TestResolveTargetOpencode2ReportsDetachedHead`, `TestResolveTargetOpencode2ReuseDoesNotClaimDetachedHead`): add a resolve `ShellOut` entry whose payload decodes to the project the list/create keys use:

```go
	project := agent.WorktreeProject{ID: "abc", Canonical: "/repo"}
	// ShellOut keys:
	provider.WorktreeResolveCmd(mounts.WorkspaceMountPath): msb.NewTestResult(
		true, 0, `{"project":{"id":"abc","canonical":"/repo"}}`, "", nil),
	provider.WorktreeListCmd(project): msb.NewTestResult(true, 0, `[]`, "", nil),
	provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}, project): msb.NewTestResult(
		true, 0, `{"directory":"/home/dev/.local/share/opencode/worktree/abc/feat-x"}`, "", nil),
```

For the reuse test the list fixture is `[{"directory":"/home/dev/.local/share/opencode/worktree/abc/feat-x"}]` (its base name matches the slug). Drop `Target` from every `agent.WorktreeSpec{…}` literal.
- Replace the `TestFindWorktreeDirStringList`/`ObjectList`/`NoMatch`/`EmptyList` tests: the two parse tests move to `internal/agent` (Step 1/2), leaving `findWorktreeDir` tested over slices:

```go
func TestFindWorktreeDirMatch(t *testing.T) {
	dirs := []string{"/wt/review-test", "/wt/bugfix-exit-zero"}
	dir, ok := findWorktreeDir(dirs, "bugfix-exit-zero")
	if !ok || dir != "/wt/bugfix-exit-zero" {
		t.Errorf("findWorktreeDir = %q, %v; want /wt/bugfix-exit-zero, true", dir, ok)
	}
}

func TestFindWorktreeDirNoMatch(t *testing.T) {
	if _, ok := findWorktreeDir([]string{"/wt/other-topic"}, "bugfix-exit-zero"); ok {
		t.Error("expected no match for a slug absent from the list")
	}
}

func TestFindWorktreeDirEmpty(t *testing.T) {
	if _, ok := findWorktreeDir(nil, "bugfix-exit-zero"); ok {
		t.Error("expected no match for an empty list")
	}
}
```

- [ ] **Step 10: Run the packages green**

Run: `go test ./internal/agent/... ./internal/sandbox/vm/`
Expected: PASS.

- [ ] **Step 11: Run the full check**

Run: `make check`
Expected: fmt, lint, and tests all pass.

- [ ] **Step 12: Commit**

```bash
git add internal/agent internal/sandbox/vm
git commit -m "fix(worktree): parse daemon JSON in Go so a custom base needs no jq"
```

---

### Task 2: Docs, changelog, spec status

**Files:**
- Modify: `docs/branch-sessions.md` (only if it states a `jq`/tooling requirement; check first)
- Modify: `CHANGELOG.md` (`## [Unreleased]` → `### Fixed`)
- Modify: `.operator-shared/specs/opencode2-worktree-api.md` (status line)

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

- [ ] **Step 1: Check the worktree docs**

Run: `rg -n "jq|worktree" docs/branch-sessions.md docs/runner-image.md`
If a page states or implies that `--worktree` needs `jq` in the image, correct it. Do not otherwise edit `docs/runner-image.md`: the remaining in-guest `curl` prerequisite is a separate, out-of-scope concern.

- [ ] **Step 2: Add the changelog entry**

Under `## [Unreleased]` → `### Fixed`, add one entry: `--worktree` for `opencode2` ran its worktree commands through `jq` inside the VM, so a custom runner base without `jq` failed; the launcher now parses the worktree API responses in Go, and a malformed worktree list fails the run instead of silently creating a second worktree (#257).

- [ ] **Step 3: Update the spec status**

Set the status line of `.operator-shared/specs/opencode2-worktree-api.md` to `Status: implemented on <branch> (pending merge)` and drop the "not yet implemented" clause for #257. If any command string or behavior diverged from the spec during implementation, correct the spec's contract text in the same commit — the spec is the source of truth for the next reader.

- [ ] **Step 4: Run the full check**

Run: `make check`
Expected: fmt, lint, docs check, and tests all pass.

- [ ] **Step 5: Commit**

```bash
git add docs CHANGELOG.md .operator-shared
git commit -m "docs: changelog the jq-free worktree path and close the spec"
```

---

## Post-plan verification (manual, user-side)

The dev VM cannot start microsandbox VMs; the end-to-end paths below are the user's to run:

- A project whose Dockerfile `FROM` is a custom base without `jq` (e.g. `debian:trixie-slim`): `agents-sandbox run --agent opencode2 --worktree feature-x` creates a worktree and attaches — no `jq: command not found`.
- A second identical run reuses the same directory (no `-2`).
- `-w feature-x:main` checks out at `main` (detached HEAD).
- `agents-sandbox run --agent opencode --worktree feature-x` behaves exactly as before on a managed base.
