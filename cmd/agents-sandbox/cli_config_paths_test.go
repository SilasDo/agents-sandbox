package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// configPathsHint renders the stderr guidance sentence config paths prints,
// naming the committed project config and the generic user config dir.
func configPathsHint() string {
	return "Repo-wide settings belong in the committed project config (.agents-sandbox/config.yaml)" +
		" so every clone shares them; machine-specific settings belong in the generic user config (" +
		configpaths.Get().UserConfigDir() + ")."
}

func TestConfigPathsPrintsLayersToStdout(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdConfig, "paths")
	cp := configpaths.Get()
	// A file in the generic user layer proves Present is read per layer: that
	// line must not be flagged (missing) while the empty layers are.
	testutil.WriteFile(t, cp.UserConfigDir(), "config.yaml", "cpus: 2\n")
	slug := git.RepoSlug()

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config paths: %v", err)
	}

	perSlug := filepath.Join(cp.UserConfigDir(), slug)
	want := []string{
		"config layers (lowest to highest precedence):",
		"  generic user config  " + cp.UserConfigDir(),
		"  per-project user config  " + perSlug + "  (missing)",
		"  project config  " + cp.ProjectConfigDir() + "  (missing)",
		"  legacy env  OPENCODE_SANDBOX_*",
		"  env  AGENTS_SANDBOX_*",
		"  flags",
		"repo slug:  " + slug,
	}
	if len(ui.OutCalls) != len(want) {
		t.Fatalf(
			"config paths wrote %d stdout lines, want %d:\n%s",
			len(ui.OutCalls),
			len(want),
			strings.Join(ui.OutCalls, "\n"),
		)
	}
	for i, line := range want {
		if ui.OutCalls[i] != line {
			t.Errorf("stdout line %d = %q, want %q", i, ui.OutCalls[i], line)
		}
	}
}

func TestConfigPathsPrintsHintToStderr(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdConfig, "paths")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config paths: %v", err)
	}

	if !slices.Contains(ui.InfoCalls, configPathsHint()) {
		t.Errorf("stderr hint missing; InfoCalls=%q", ui.InfoCalls)
	}
	if out := strings.Join(ui.OutCalls, "\n"); strings.Contains(out, "Repo-wide settings belong") {
		t.Errorf("the hint must go to stderr only, got it on stdout:\n%s", out)
	}
}

func TestConfigPathsIsQuietAware(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdConfig, "paths", "--quiet")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config paths --quiet: %v", err)
	}

	if len(ui.OutCalls) != 0 {
		t.Errorf("--quiet must suppress the layer block and the repo slug, got: %v", ui.OutCalls)
	}
	if !slices.Contains(ui.InfoCalls, configPathsHint()) {
		t.Errorf("--quiet must still print the stderr hint; InfoCalls=%q", ui.InfoCalls)
	}
}
