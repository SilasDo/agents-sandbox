package agent

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestOpencode2WorktreeListFilterExcludesCanonicalRoot runs the real jq filter
// (not just its literal text) over worktree-list fixtures, pinning the
// correctness property the spec relies on: the project's canonical root is
// dropped, so a worktree named like the repository never reuses the main
// checkout, while real worktrees — including a server-suffixed `name-2` — survive.
func TestOpencode2WorktreeListFilterExcludesCanonicalRoot(t *testing.T) {
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq not available")
	}
	const canonical = "/repo"
	run := func(t *testing.T, input string) []byte {
		t.Helper()
		cmd := exec.Command(jq, "-ce", "--arg", "canonical", canonical, opencode2WorktreeListFilter)
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("jq failed: %v: %s", err, out)
		}
		return out
	}

	tests := []struct {
		name string
		list string
		want []string
	}{
		{
			name: "canonical only",
			list: `[{"directory":"/repo"}]`,
			want: []string{},
		},
		{
			name: "mixed",
			list: `[{"directory":"/repo"},{"directory":"/repo/wt/feat-x"}]`,
			want: []string{"/repo/wt/feat-x"},
		},
		{
			name: "server-suffixed name survives",
			list: `[{"directory":"/repo"},{"directory":"/wt/feat-x-2"}]`,
			want: []string{"/wt/feat-x-2"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var entries []struct {
				Directory string `json:"directory"`
			}
			if err := json.Unmarshal(run(t, tc.list), &entries); err != nil {
				t.Fatalf("decode jq output: %v", err)
			}
			got := make([]string, 0, len(entries))
			for _, e := range entries {
				got = append(got, e.Directory)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("filter kept %v, want %v", got, tc.want)
			}
		})
	}
}

// TestOpencode2WorktreeListFilterRejectsNonArray pins the strict half of the
// filter: a non-array or empty payload exits non-zero instead of producing an
// empty success, so a route or field rename fails the list rather than reading
// as "no worktrees".
func TestOpencode2WorktreeListFilterRejectsNonArray(t *testing.T) {
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq not available")
	}
	for name, input := range map[string]string{
		"object payload": `{"directory":"/repo"}`,
		"null payload":   `null`,
		"empty input":    ``,
		"invalid json":   `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(jq, "-ce", "--arg", "canonical", "/repo", opencode2WorktreeListFilter)
			cmd.Stdin = strings.NewReader(input)
			if err := cmd.Run(); err == nil {
				t.Errorf("filter must fail on %s, got exit 0", name)
			}
		})
	}
}

// TestOpencode2WorktreeListFilterRejectsMalformedEntries pins that an entry
// without a usable directory fails the list instead of being accepted and then
// silently ignored by the base-name match. An entry whose field was renamed or
// blanked is exactly the field-rename case the list must not read as "no
// worktrees"; accepting it would create a second worktree beside the existing
// one.
func TestOpencode2WorktreeListFilterRejectsMalformedEntries(t *testing.T) {
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq not available")
	}
	for name, input := range map[string]string{
		"renamed field":      `[{"directory":"/repo"},{"path":"/wt/feat-x"}]`,
		"missing field":      `[{"directory":"/repo"},{}]`,
		"null directory":     `[{"directory":null}]`,
		"non-string field":   `[{"directory":123}]`,
		"empty directory":    `[{"directory":""}]`,
		"string entry":       `["/wt/feat-x"]`,
		"array entry":        `[["/wt/feat-x"]]`,
		"stringified object": `["{\"directory\":\"/wt/feat-x\"}"]`,
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(jq, "-ce", "--arg", "canonical", "/repo", opencode2WorktreeListFilter)
			cmd.Stdin = strings.NewReader(input)
			if err := cmd.Run(); err == nil {
				t.Errorf("filter must fail on %s, got exit 0", name)
			}
		})
	}
}
