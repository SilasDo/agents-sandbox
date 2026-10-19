package reprovision

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// emptyCandidatesConfigPaths returns no candidates, exercising the guard that
// keeps ResolveConfigSource from indexing an empty candidate list.
type emptyCandidatesConfigPaths struct{ configpaths.ConfigPaths }

func (emptyCandidatesConfigPaths) UserAgentConfigDirCandidates(agent.Agent) []string {
	return nil
}

func (emptyCandidatesConfigPaths) ProjectAgentConfigDirCandidates(agent.Agent) []string {
	return nil
}

func TestResolveConfigSourceEmptyCandidates(t *testing.T) {
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return emptyCandidatesConfigPaths{} }
	t.Cleanup(func() { configpaths.Get = orig })

	a, _ := agent.Lookup("opencode2")
	if _, err := ResolveConfigSource(a); err == nil {
		t.Error("expected an error for empty config-dir candidates")
	}
}

// staticCandidates serves fixed candidate dirs so a test can point the
// resolver at paths whose os.Stat fails with a non-"not exist" error.
type staticCandidates struct {
	configpaths.ConfigPaths

	dirs []string
}

func (s staticCandidates) UserAgentConfigDirCandidates(agent.Agent) []string {
	return s.dirs
}

func (s staticCandidates) ProjectAgentConfigDirCandidates(agent.Agent) []string {
	return s.dirs
}

func withStaticCandidates(t *testing.T, dirs ...string) {
	t.Helper()
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return staticCandidates{dirs: dirs} }
	t.Cleanup(func() { configpaths.Get = orig })
}

func TestResolveConfigSourceStatError(t *testing.T) {
	// A candidate beneath a regular file makes os.Stat fail with ENOTDIR,
	// which must surface rather than be treated as "absent" — for the primary
	// candidate and for an alias candidate reached through the fallback loop.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := agent.Lookup("opencode2")

	t.Run("primary", func(t *testing.T) {
		withStaticCandidates(t, filepath.Join(blocker, "opencode2"))
		if _, err := ResolveConfigSource(a); err == nil {
			t.Error("expected an error when the primary candidate stat fails")
		}
	})

	t.Run("alias", func(t *testing.T) {
		withStaticCandidates(t, filepath.Join(t.TempDir(), "absent"), filepath.Join(blocker, "opencode"))
		if _, err := ResolveConfigSource(a); err == nil {
			t.Error("expected an error when an alias candidate stat fails")
		}
	})
}

func TestResolveConfigSource(t *testing.T) {
	cases := []struct {
		name                       string
		user2, user1, proj2, proj1 bool
		wantFallback               string
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
			got, err := ResolveConfigSource(a)
			if err != nil {
				t.Fatalf("ResolveConfigSource: %v", err)
			}

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
