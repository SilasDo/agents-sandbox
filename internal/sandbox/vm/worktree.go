package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const defaultTargetDir = mounts.WorkspaceMountPath

// validateWorktreeBase confirms the base ref resolves among existing local refs
// inside the VM. It never fetches: the ref must already be present locally.
func validateWorktreeBase(ctx context.Context, sb msb.Sandbox, dir, base string) error {
	out, err := sb.Exec(ctx, "git", []string{"-C", dir, "rev-parse", "--verify", base + "^{commit}"})
	if err != nil {
		return fmt.Errorf("check base %q: %w", base, err)
	}
	if !out.Success() {
		return fmt.Errorf("base %q does not resolve locally (no fetch is performed)", base)
	}
	return nil
}

// findWorktreeDir scans the daemon's worktree list response for a directory
// whose base name matches the given slug. The list may be an array of
// directory path strings (current daemon) or an array of objects each carrying
// a directory field (newer daemon). ok is false when no match is found.
func findWorktreeDir(listStdout string, slug string) (string, bool) {
	if strings.TrimSpace(listStdout) == "" {
		return "", false
	}
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(listStdout), &entries); err != nil {
		return "", false
	}
	for _, entry := range entries {
		var directory string
		switch {
		case len(entry) > 0 && entry[0] == '"':
			var path string
			if json.Unmarshal(entry, &path) != nil {
				continue
			}
			directory = path
		default:
			var info struct {
				Directory string `json:"directory"`
			}
			if json.Unmarshal(entry, &info) != nil {
				continue
			}
			directory = info.Directory
		}
		if directory != "" && filepath.Base(directory) == slug {
			return directory, true
		}
	}
	return "", false
}

// worktreeDetachedNote is the parenthetical appended to a worktree message for
// agents whose daemon leaves a new worktree on a detached HEAD (v2), naming the
// recovery; it is empty for agents that keep a branch (v1).
func worktreeDetachedNote(a agent.Agent) string {
	reporter, ok := agent.AsDetachedWorktreeReporter(a)
	if !ok || !reporter.WorktreeCreatesDetachedHead() {
		return ""
	}
	return " (detached HEAD; run `git switch -c <branch>` to keep your commits)"
}

// ResolveTarget returns the --dir target for attach. An empty spec → defaultDir.
// With a name → reuse an existing worktree via the provider's HTTP API when one
// already exists for that name, otherwise create a new one, and return its
// directory path. When a base is present on an existing (reused) worktree the
// base is silently ignored with a warning. When a base is present on a fresh
// create the base is validated against the launch directory (no fetch) before
// the create, so an unresolvable base aborts without creating a worktree. An
// agent without a worktree provider falls back to defaultDir. defaultDir is the
// launch directory (the guest CWD, e.g. `/workspace/<sub>`).
func ResolveTarget(
	ctx context.Context,
	a agent.Agent,
	sb msb.Sandbox,
	spec options.WorktreeSpec,
	defaultDir string,
	ui termio.UI,
) (string, error) {
	provider, hasWorktree := agent.AsWorktreeProvider(a)
	if !hasWorktree {
		return defaultDir, nil
	}

	if spec.Name == "" {
		return defaultDir, nil
	}

	ui.Verbosef("checking for an existing worktree %q", spec.Name)
	listOut, err := sb.Shell(ctx, provider.WorktreeListCmd(defaultDir))
	if err != nil {
		return "", fmt.Errorf("list worktrees for %q: %w", spec.Name, err)
	}
	if listOut.Success() {
		if dir, ok := findWorktreeDir(listOut.Stdout(), spec.Name); ok {
			if spec.Base != "" {
				ui.Warnf("worktree %q already exists; ignoring base %q", spec.Name, spec.Base)
			}
			ui.Infof("reusing existing worktree %q: %s%s", spec.Name, dir, worktreeDetachedNote(a))
			return dir, nil
		}
	}

	if spec.Base != "" {
		if err = validateWorktreeBase(ctx, sb, defaultDir, spec.Base); err != nil {
			return "", fmt.Errorf("worktree %q: %w", spec.Name, err)
		}
	}

	ui.Verbosef("creating worktree %q", spec.Name)
	createSpec := agent.WorktreeSpec{
		Name:   spec.Name,
		Base:   spec.Base,
		Target: defaultDir,
	}
	out, err := sb.Shell(ctx, provider.WorktreeCreateCmd(createSpec))
	if err != nil {
		return "", fmt.Errorf("create worktree %q: %w", spec.Name, err)
	}
	if !out.Success() {
		return "", fmt.Errorf("create worktree %q failed (exit %d): %s", spec.Name, out.ExitCode(), out.Stderr())
	}

	dir, ok := provider.WorktreeParseDir(out.Stdout())
	if !ok {
		return "", fmt.Errorf("parse worktree response for %q", spec.Name)
	}

	ui.Infof("created worktree %q: %s%s", spec.Name, dir, worktreeDetachedNote(a))
	if filepath.Base(dir) != spec.Name {
		ui.Warnf("requested worktree name %q was already taken; the daemon created %s", spec.Name, dir)
	}
	return dir, nil
}
