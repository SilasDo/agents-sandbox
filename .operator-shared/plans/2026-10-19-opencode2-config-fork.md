# opencode2 Config Namespace Fork Implementation Plan

> **Status:** implemented on `opencode2-config-fork` (PR #232); all tasks complete. A later adversarial review
> hardened config-dir name validation and the resolver's error handling.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give opencode2 its own launcher config namespace (`…/agents-sandbox/opencode2/`) so v1 and v2 config can diverge, while the guest config path and host drop-in stay `opencode`.

**Architecture:** An optional `agent.ConfigDirAliaser` capability declares legacy dir names; `configpaths` maps an ordered name list to ordered candidate dirs (pure, no I/O); a `reprovision.ResolveConfigSource` resolver picks one source name globally by directory existence and warns when it falls back. `ConfigDirName` becomes `opencode2`; `VMConfigPath`/`ProvisionRules`/`ConfigFileNames`/`SnippetPattern` stay `opencode`.

**Tech Stack:** Go 1.26, Cobra/Viper CLI, `testify`-free stdlib tests, `golangci-lint`.

**Spec:** `.operator-shared/specs/opencode2-config-fork.md`

## Global Constraints

- Module `github.com/inoio/agents-sandbox`, Go 1.26.
- Format with `make fmt` / `golangci-lint fmt` (never `go fmt`); lint with `make lint` / `golangci-lint run` (never `go vet`).
- TDD: write the failing test, run it, implement, run it green, commit.
- No behavior change for the `opencode` (v1) agent.
- Guest target `VMConfigPath` and host drop-in `ProvisionRules` remain `opencode`.
- Warning fires **only** when the resolver falls back (`Fallback != ""`); a primary dir existing anywhere suppresses the fallback and the warning.
- Candidate path methods must never create directories.

## Review Focus

- An agent that declares aliases but has no config dir anywhere → resolves to the primary dirs, no warning.
- Partial migration (a primary dir exists *and* legacy files remain) → primary wins, legacy ignored, **no** warning.
- Candidate methods are non-creating: creating the mock base then calling candidates leaves the leaf absent.
- Agents without aliases (and without a `ConfigMerger`) are unaffected — candidate list is `[primary]`.
- The fallback warning fires exactly once per config load.

---

### Task 1: opencode2 config-dir name and alias capability

**Files:**
- Modify: `internal/agent/capabilities.go`
- Modify: `internal/agent/opencode2.go`
- Modify: `internal/agent/opencode_shared.go` (comment only)
- Test: `internal/agent/opencode2_test.go`, `internal/agent/opencode_test.go`

**Interfaces:**
- Produces: `agent.ConfigDirAliaser` interface with `ConfigDirAliases() []string`; `agent.AsConfigDirAliaser(a Agent) (ConfigDirAliaser, bool)`; `opencode2Profile.ConfigDirName() == "opencode2"` and `opencode2Profile.ConfigDirAliases() == []string{"opencode"}`.

- [ ] **Step 1: Write the failing tests**

In `internal/agent/opencode2_test.go`, change `TestOpencode2ConfigDirName` to expect `"opencode2"`, and add:

```go
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
```

In `internal/agent/opencode_test.go`, add:

```go
func TestOpencodeConfigDirAliases(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if _, ok := agent.AsConfigDirAliaser(a); ok {
		t.Error("opencode (v1) must not declare config dir aliases")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run 'ConfigDirName|ConfigDirAliases' -v`
Expected: FAIL — opencode2 `ConfigDirName` is `opencode`; `AsConfigDirAliaser` undefined.

- [ ] **Step 3: Implement the capability and override**

In `internal/agent/capabilities.go`:

```go
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
```

In `internal/agent/opencode2.go`, add (the embedded `opencodeConfig.ConfigDirName` is shadowed):

```go
// ConfigDirName scopes opencode2's own launcher config dir; the guest config
// path and host drop-in still come from the embedded opencodeConfig.
func (opencode2Profile) ConfigDirName() string { return opencode2Name }

// ConfigDirAliases names the legacy shared config dir opencode2 used before it
// gained its own namespace.
func (opencode2Profile) ConfigDirAliases() []string { return []string{opencodeName} }
```

In `internal/agent/opencode_shared.go`, rewrite the `opencodeConfig` doc comment: it no longer means "both agents share"; it is v1's config behavior, embedded by opencode2 for the guest path / file names / drop-in rules while opencode2 overrides `ConfigDirName`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/ -run 'ConfigDirName|ConfigDirAliases' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/capabilities.go internal/agent/opencode.go internal/agent/opencode2.go internal/agent/opencode_shared.go internal/agent/opencode_test.go internal/agent/opencode2_test.go
git commit -m "feat(agent): give opencode2 its own config-dir name with a legacy alias"
```

---

### Task 2: configpaths candidate methods

**Files:**
- Modify: `internal/configpaths/configpaths.go` (interface + real)
- Modify: `internal/configpaths/testmock.go` (mock + fail-fast)
- Modify: `internal/sandbox/image/render_test.go` (`projectDockerfilePaths` test double)
- Test: `internal/configpaths/mockpaths_test.go`, `internal/configpaths/failfast_test.go`, `internal/configpaths/config_paths_test.go`

**Interfaces:**
- Consumes: `agent.ConfigDirAliaser` / `AsConfigDirAliaser` (Task 1).
- Produces: `ConfigPaths.UserAgentConfigDirCandidates(a agent.Agent) []string` and `ConfigPaths.ProjectAgentConfigDirCandidates(a agent.Agent) []string` — ordered `[primary, alias1, …]`, de-duplicated, absolute, never creating directories.

- [ ] **Step 1: Write the failing tests**

Add to `internal/configpaths/failfast_test.go`'s `TestFailFastConfigPathsPanics` table:

```go
{"UserAgentConfigDirCandidates", func() { f.UserAgentConfigDirCandidates(a) }},
{"ProjectAgentConfigDirCandidates", func() { f.ProjectAgentConfigDirCandidates(a) }},
```

Add to `internal/configpaths/mockpaths_test.go` (add `"slices"` import):

```go
func TestMockAgentConfigDirCandidates(t *testing.T) {
	WithMockConfigPaths(t)
	base := Get().(*mockConfigPaths).baseDir
	oc, _ := agent.Lookup("opencode")
	oc2, _ := agent.Lookup("opencode2")

	if got := Get().UserAgentConfigDirCandidates(oc); !slices.Equal(got, []string{filepath.Join(base, "userconfig", "opencode")}) {
		t.Errorf("opencode user candidates = %v", got)
	}
	want := []string{filepath.Join(base, "userconfig", "opencode2"), filepath.Join(base, "userconfig", "opencode")}
	if got := Get().UserAgentConfigDirCandidates(oc2); !slices.Equal(got, want) {
		t.Errorf("opencode2 user candidates = %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(base, "userconfig", "opencode2")); !os.IsNotExist(err) {
		t.Errorf("candidate methods must not create dirs; stat err = %v", err)
	}
}
```

Add to `internal/configpaths/config_paths_test.go` (uses `WithRealConfigPaths` + `t.Setenv`):

```go
func TestRealAgentConfigDirCandidates(t *testing.T) {
	WithRealConfigPaths(t)
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	oc2, _ := agent.Lookup("opencode2")
	want := []string{
		"/xdg/config/agents-sandbox/opencode2",
		"/xdg/config/agents-sandbox/opencode",
	}
	if got := Get().UserAgentConfigDirCandidates(oc2); !slices.Equal(got, want) {
		t.Errorf("user candidates = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/configpaths/ -run 'Candidates|FailFast' -v`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement the interface, real, mock, and fail-fast**

Add to the `ConfigPaths` interface in `internal/configpaths/configpaths.go`:

```go
	UserAgentConfigDirCandidates(a agent.Agent) []string
	ProjectAgentConfigDirCandidates(a agent.Agent) []string
```

Add the real implementation and shared helper:

```go
// UserAgentConfigDirCandidates returns the agent's user config-dir candidates,
// primary first, then each alias. Paths only: the method never creates dirs.
func (c *realConfigPaths) UserAgentConfigDirCandidates(a agent.Agent) []string {
	return agentConfigDirCandidates(c.UserConfigDir(), a)
}

// ProjectAgentConfigDirCandidates returns the project config-dir candidates.
func (c *realConfigPaths) ProjectAgentConfigDirCandidates(a agent.Agent) []string {
	return agentConfigDirCandidates(projectConfigDir, a)
}

func agentConfigDirCandidates(base string, a agent.Agent) []string {
	names := []string{a.ConfigDirName()}
	if al, ok := agent.AsConfigDirAliaser(a); ok {
		names = append(names, al.ConfigDirAliases()...)
	}
	dirs := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		dirs = append(dirs, filepath.Join(base, n))
	}
	return dirs
}
```

In `internal/configpaths/testmock.go`, add to `mockConfigPaths` (join the base directly — do **not** call the creating `UserConfigDir`/`ProjectConfigDir`):

```go
func (m *mockConfigPaths) UserAgentConfigDirCandidates(a agent.Agent) []string {
	return agentConfigDirCandidates(filepath.Join(m.baseDir, "userconfig"), a)
}

func (m *mockConfigPaths) ProjectAgentConfigDirCandidates(a agent.Agent) []string {
	return agentConfigDirCandidates(filepath.Join(m.baseDir, "projectconfig"), a)
}
```

Add to `failFastConfigPaths`:

```go
func (f *failFastConfigPaths) UserAgentConfigDirCandidates(_ agent.Agent) []string {
	f.mustMock()
	return nil
}
func (f *failFastConfigPaths) ProjectAgentConfigDirCandidates(_ agent.Agent) []string {
	f.mustMock()
	return nil
}
```

Add to `projectDockerfilePaths` in `internal/sandbox/image/render_test.go`:

```go
func (p *projectDockerfilePaths) UserAgentConfigDirCandidates(agent.Agent) []string {
	return nil
}
func (p *projectDockerfilePaths) ProjectAgentConfigDirCandidates(agent.Agent) []string {
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/configpaths/ ./internal/sandbox/image/ -run 'Candidates|FailFast|Render' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configpaths/ internal/sandbox/image/render_test.go
git commit -m "feat(configpaths): add ordered agent config-dir candidates"
```

---

### Task 3: ResolveConfigSource

**Files:**
- Modify: `internal/sandbox/reprovision/config_files.go`
- Test: `internal/sandbox/reprovision/config_source_test.go` (create)

**Interfaces:**
- Consumes: `ConfigPaths.UserAgentConfigDirCandidates` / `ProjectAgentConfigDirCandidates` (Task 2).
- Produces: `reprovision.ConfigSource{ UserDir, ProjectDir, Fallback string }` and `reprovision.ResolveConfigSource(a agent.Agent) ConfigSource`.

- [ ] **Step 1: Write the failing test**

Create `internal/sandbox/reprovision/config_source_test.go`:

```go
package reprovision

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestResolveConfigSource(t *testing.T) {
	cases := []struct {
		name                    string
		user2, user1, proj2, proj1 bool
		wantFallback            string
	}{
		{"nothing", false, false, false, false, ""},
		{"user primary", true, false, false, false, ""},
		{"project primary", false, false, true, false, ""},
		{"user legacy", false, true, false, false, "opencode"},
		{"project legacy", false, false, false, true, "opencode"},
		{"primary beats legacy", true, true, true, true, ""},
		{"any primary wins", false, true, true, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			cp := configpaths.Get()
			mk := func(dir, name string, on bool) {
				if on {
					if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			}
			mk(cp.UserConfigDir(), "opencode2", tc.user2)
			mk(cp.UserConfigDir(), "opencode", tc.user1)
			mk(cp.ProjectConfigDir(), "opencode2", tc.proj2)
			mk(cp.ProjectConfigDir(), "opencode", tc.proj1)

			a, _ := agent.Lookup("opencode2")
			got := ResolveConfigSource(a)

			name := "opencode2"
			if tc.wantFallback != "" {
				name = tc.wantFallback
			}
			want := ConfigSource{
				UserDir:    filepath.Join(cp.UserConfigDir(), name),
				ProjectDir: filepath.Join(cp.ProjectConfigDir(), name),
				Fallback:   tc.wantFallback,
			}
			if got != want {
				t.Errorf("ResolveConfigSource = %+v, want %+v", got, want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/reprovision/ -run TestResolveConfigSource -v`
Expected: FAIL — `ConfigSource`/`ResolveConfigSource` undefined.

- [ ] **Step 3: Implement the resolver**

Add to `internal/sandbox/reprovision/config_files.go`:

```go
// ConfigSource is the resolved launcher config-dir pair for an agent. Fallback
// is the legacy dir name actually in use, or "" when the primary dirs are used.
type ConfigSource struct {
	UserDir    string
	ProjectDir string
	Fallback   string
}

// ResolveConfigSource picks the agent's launcher config dirs. The primary dirs
// win when either exists in any scope; otherwise the first alias whose dir
// exists in either scope is used for both scopes. When nothing exists the
// primary dirs are returned and Fallback is empty.
func ResolveConfigSource(a agent.Agent) ConfigSource {
	userCands := cp.Get().UserAgentConfigDirCandidates(a)
	projectCands := cp.Get().ProjectAgentConfigDirCandidates(a)
	if len(userCands) == 0 || len(projectCands) == 0 {
		return ConfigSource{}
	}
	if anyDirExists(userCands[0], projectCands[0]) {
		return ConfigSource{UserDir: userCands[0], ProjectDir: projectCands[0]}
	}
	for i := 1; i < len(userCands); i++ {
		if anyDirExists(userCands[i], projectCands[i]) {
			return ConfigSource{
				UserDir:    userCands[i],
				ProjectDir: projectCands[i],
				Fallback:   filepath.Base(userCands[i]),
			}
		}
	}
	return ConfigSource{UserDir: userCands[0], ProjectDir: projectCands[0]}
}

// anyDirExists reports whether any path names an existing directory.
func anyDirExists(dirs ...string) bool {
	for _, d := range dirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sandbox/reprovision/ -run TestResolveConfigSource -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/reprovision/config_files.go internal/sandbox/reprovision/config_source_test.go
git commit -m "feat(reprovision): resolve agent config source with legacy fallback"
```

---

### Task 4: Wire the resolved source into merge/mirror and warn

**Files:**
- Modify: `internal/sandbox/reprovision/config_files.go`
- Test: `internal/sandbox/reprovision/config_files_test.go`, `cmd/agents-sandbox/cli_config_test.go`

**Interfaces:**
- Consumes: `ConfigSource` / `ResolveConfigSource` (Task 3).
- Produces: `ConfigFiles.Source ConfigSource`; `buildMergedConfig(a, vmHome, src)`, `buildMirror(a, vmHome, src)`, `mirrorFilesFromConfig(a, vmHome, src)`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/sandbox/reprovision/config_files_test.go`:

```go
func TestLoadConfigFilesFallsBackToLegacyDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	a, _ := agent.Lookup("opencode2")

	legacy := filepath.Join(cp.UserConfigDir(), "opencode")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, legacy, "opencode-model.json", `{"model":"v2-legacy"}`)
	testutil.WriteFile(t, legacy, "tui.json", `{"theme":"legacy"}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFiles(a, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	if cf.Source.Fallback != "opencode" {
		t.Errorf("Source.Fallback = %q, want opencode", cf.Source.Fallback)
	}
	if !strings.Contains(string(cf.Merged), "v2-legacy") {
		t.Errorf("merged config did not read the legacy dir: %s", cf.Merged)
	}
	mirrorPath := filepath.Join(reprovisionVMConfigDir(t, a), "tui.json")
	if _, ok := cf.Mirror[mirrorPath]; !ok {
		t.Errorf("mirror missing legacy file %q; have %v", mirrorPath, cf.Mirror)
	}
	if n := countWarns(ui.WarnCalls, "legacy config"); n != 1 {
		t.Errorf("legacy fallback warning count = %d, want 1; warnings: %v", n, ui.WarnCalls)
	}
}

func TestLoadConfigFilesPrimarySuppressesLegacy(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	a, _ := agent.Lookup("opencode2")

	for _, dir := range []string{filepath.Join(cp.UserConfigDir(), "opencode2"), filepath.Join(cp.UserConfigDir(), "opencode")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	testutil.WriteFile(t, filepath.Join(cp.UserConfigDir(), "opencode2"), "opencode-model.json", `{"model":"v2-primary"}`)
	testutil.WriteFile(t, filepath.Join(cp.UserConfigDir(), "opencode"), "opencode-model.json", `{"model":"v1-legacy"}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFiles(a, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	if cf.Source.Fallback != "" {
		t.Errorf("Source.Fallback = %q, want empty (primary wins)", cf.Source.Fallback)
	}
	if !strings.Contains(string(cf.Merged), "v2-primary") || strings.Contains(string(cf.Merged), "v1-legacy") {
		t.Errorf("merged config should read only the primary dir: %s", cf.Merged)
	}
	if n := countWarns(ui.WarnCalls, "legacy config"); n != 0 {
		t.Errorf("unexpected legacy warning: %v", ui.WarnCalls)
	}
}
```

Add the two small helpers in the same test file (or `coverage_test.go`):

```go
func reprovisionVMConfigDir(t *testing.T, a agent.Agent) string {
	t.Helper()
	cm, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("agent has no ConfigMerger")
	}
	return filepath.Dir(cm.VMConfigPath(VMHomeDir))
}

func countWarns(warns []string, substr string) int {
	n := 0
	for _, w := range warns {
		if strings.Contains(w, substr) {
			n++
		}
	}
	return n
}
```

Add to `cmd/agents-sandbox/cli_config_test.go`, reusing the existing `setupCommandFixtures` harness and adding a sibling to `mustOpencode`:

```go
func mustOpencode2(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode2")
	if !ok {
		t.Fatal("opencode2 agent not registered")
	}
	return a
}

func TestConfigAgentOpencode2UsesOwnDir(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "--agent", "opencode2")
	srcDir := configpaths.Get().UserAgentConfigDir(mustOpencode2(t))
	testutil.WriteFile(t, srcDir, "opencode-model.json", `{"model":"v2"}`)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent --agent opencode2: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, `"model": "v2"`) {
		t.Errorf("opencode2 config agent did not read its own dir, got:\n%s", joined)
	}
	if !strings.Contains(joined, srcDir) {
		t.Errorf("expected the opencode2 source dir %q in output, got:\n%s", srcDir, joined)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/reprovision/ -run 'FallsBackToLegacy|PrimarySuppressesLegacy' -v`
Expected: FAIL — `ConfigFiles.Source` undefined; merge still reads the primary dir only.

- [ ] **Step 3: Implement the wiring and warning**

In `internal/sandbox/reprovision/config_files.go`:

- Add `Source ConfigSource` to `ConfigFiles`.
- In `LoadConfigFilesForHost`, resolve once at the top:

```go
	src := ResolveConfigSource(a)
```

  Pass `src` into `buildMergedConfig(a, vmHome, src)` and `buildMirror(a, vmHome, src)`, set `Source: src` on the returned `ConfigFiles`, and after the other warnings add:

```go
	if src.Fallback != "" {
		ui.Warnf("agent %s is using the legacy config directory %q; move its files to %q to silence this warning",
			a.Name(), src.Fallback, a.ConfigDirName())
	}
```

- Change `buildMergedConfig(a agent.Agent, vmHome string, src ConfigSource)` to pass `src.UserDir`/`src.ProjectDir` to `config.BuildMerged`.
- Change `buildMirror(a agent.Agent, vmHome string, src ConfigSource)` to pass `src.UserDir`/`src.ProjectDir` to `config.ScanMirror`.
- Change `mirrorFilesFromConfig(a agent.Agent, vmHome string, src ConfigSource)` likewise, and update `Describe` to pass `cf.Source`:

```go
	return cf.Merged, cf.Sources, hostFiles, mirrorFilesFromConfig(a, vmHome, cf.Source), nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/reprovision/ ./cmd/agents-sandbox/ -run 'Config|Legacy|FallsBack|Primary' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/reprovision/config_files.go internal/sandbox/reprovision/*_test.go cmd/agents-sandbox/cli_config_test.go
git commit -m "feat(reprovision): read opencode2 config from its own dir with legacy fallback"
```

---

### Task 5: Docs and changelog

**Files:**
- Modify: `docs/configuration/agent.md`
- Modify: `CHANGELOG.md`

**Interfaces:** none (documentation only).

- [ ] **Step 1: Update the agent docs**

In `docs/configuration/agent.md`:
- opencode2 bullet: replace "It shares the `opencode` config directory (v2 reads the same files as v1)" with a statement that opencode2 now owns `~/.config/agents-sandbox/opencode2/` and `.agents-sandbox/opencode2/`, while its VM config path and host drop-in stay `~/.config/opencode/`.
- The "Each agent owns its config directories" list and the config-snippet/mirror sections: note `opencode2` uses `…/opencode2/` instead of the old shared `…/opencode/`.
- Add one sentence documenting the legacy fallback: while an `opencode2` dir is absent, opencode2 reads the old shared `opencode` dir and warns on every run; creating the `opencode2` dir switches to it.

- [ ] **Step 2: Add the changelog entry**

Under `CHANGELOG.md` → `[Unreleased]` → `Changed`, add one line: opencode2 reads its agent config from `~/.config/agents-sandbox/opencode2/` (and the project equivalent) instead of the shared `opencode` dir; it falls back to the old dir with a warning while the new dir does not exist. Guest config path is unchanged.

- [ ] **Step 3: Run the docs check**

Run: `ci/check-docs.sh`
Expected: PASS (no broken links introduced).

- [ ] **Step 4: Whole-branch verification**

Run: `make check`
Expected: PASS (fmt, lint, tests).

- [ ] **Step 5: Commit**

```bash
git add docs/configuration/agent.md CHANGELOG.md
git commit -m "docs: document opencode2 config namespace fork"
```
