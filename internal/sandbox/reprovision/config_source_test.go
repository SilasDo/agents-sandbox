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
