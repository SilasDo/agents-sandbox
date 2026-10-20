package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// The adoption tests use two fixed slugs so every path in the notice and every
// assertion is spelled out literally: widget-aaa is the legacy origin-derived
// identity, widget-bbb the checkout-scoped one.
const (
	adoptRepoSlug     = "widget-aaa"
	adoptCheckoutSlug = "widget-bbb"
	adoptAgentName    = "opencode"
	// adoptLegacyVolume is the home-volume name recorded in the legacy state,
	// embedding the legacy repo slug because the name rides along on migration.
	adoptLegacyVolume = "agents-sandbox-home-widget-aaa-opencode-1"
)

func adoptIdentity() git.Identity {
	return git.Identity{
		RepoSlug:     adoptRepoSlug,
		CheckoutSlug: adoptCheckoutSlug,
		BindRoot:     "/repo",
		CheckoutRoot: "/repo",
	}
}

func adoptRepoKey() state.Key     { return state.Key{Slug: adoptRepoSlug, Agent: adoptAgentName} }
func adoptCheckoutKey() state.Key { return state.Key{Slug: adoptCheckoutSlug, Agent: adoptAgentName} }

func adoptLegacyVMName() string { return projectVMName(adoptRepoKey()) }

// seedLegacyState writes the legacy origin-keyed state file that triggers
// adoption, and returns the path it wrote so a test can assert it survives.
func seedLegacyState(t *testing.T) string {
	t.Helper()
	if err := seedHomeState(adoptRepoKey(), state.HomeState{HomeVolume: adoptLegacyVolume}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}
	return filepath.Join(state.KeyDir(adoptRepoKey()), "state.yaml")
}

// statePresent reports whether the key has a readable state file, failing the
// test on any read error other than a missing file.
func statePresent(t *testing.T, k state.Key) bool {
	t.Helper()
	_, err := state.ReadState(k)
	switch {
	case err == nil:
		return true
	case errors.Is(err, state.ErrStateNotFound):
		return false
	default:
		t.Fatalf("ReadState(%v): %v", k, err)
		return false
	}
}

// TestAdoptLegacyIdentityMovesStateAndDestroysVM covers the happy path: the
// legacy VM is destroyed and the legacy state directory merges onto the
// checkout slug, so the caller continues under the checkout identity.
func TestAdoptLegacyIdentityMovesStateAndDestroysVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName()}
	var requested string
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		requested = name
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if requested != adoptLegacyVMName() {
		t.Errorf("GetSandbox name = %q, want %q", requested, adoptLegacyVMName())
	}
	if !handle.Destroyed {
		t.Error("legacy VM was not destroyed")
	}
	if !statePresent(t, adoptCheckoutKey()) {
		t.Error("no state file under the checkout slug after adoption")
	}
	if _, statErr := os.Stat(state.SlugDir(adoptRepoSlug)); !os.IsNotExist(statErr) {
		t.Errorf("legacy slug dir still present (stat error = %v)", statErr)
	}
	want := IdentityAdoption{Slug: adoptCheckoutSlug, Pending: true, Applied: true}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
}

// TestAdoptLegacyIdentityNoticeNamesEveryPath pins the notice's content: it is
// the only place the user learns where their state went, so every moved or
// recreated path has to appear in the block.
func TestAdoptLegacyIdentityNoticeNamesEveryPath(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName()}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(
		context.Background(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	notice := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{
		"Migrating this checkout to its own project identity",
		state.SlugDir(adoptRepoSlug),
		state.SlugDir(adoptCheckoutSlug),
		adoptLegacyVMName(),
		projectVMName(adoptCheckoutKey()),
		adoptLegacyVolume,
		naming.RunnerImageRef(adoptRepoSlug, adoptAgentName),
		naming.RunnerImageRef(adoptCheckoutSlug, adoptAgentName),
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice missing %q:\n%s", want, notice)
		}
	}
}

// TestAdoptLegacyIdentityNoticeWithoutHomeVolume pins the placeholder the notice
// renders when the legacy state records no home volume, so the row never shows
// an empty value.
func TestAdoptLegacyIdentityNoticeWithoutHomeVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedHomeState(adoptRepoKey(), state.HomeState{}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called during a dry run", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, false, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if notice := strings.Join(ui.WarnCalls, "\n"); !strings.Contains(notice, "(none recorded)") {
		t.Errorf("notice missing the empty home-volume placeholder:\n%s", notice)
	}
	if !got.Pending {
		t.Errorf("adoption = %+v, want a pending migration", got)
	}
}

// TestAdoptLegacyIdentityNoLegacyState covers a project that never had the
// legacy identity: nothing is read, destroyed, moved or printed.
func TestAdoptLegacyIdentityNoLegacyState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called with no pending migration", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if got != (IdentityAdoption{Slug: adoptCheckoutSlug}) {
		t.Errorf("adoption = %+v, want the checkout slug with nothing pending", got)
	}
	if len(ui.WarnCalls) != 0 || len(ui.InfoCalls) != 0 || len(ui.VerboseCalls) != 0 ||
		len(ui.OutCalls) != 0 || len(ui.PrintCalls) != 0 {
		t.Errorf("expected no output, got warn=%v info=%v verbose=%v out=%v print=%v",
			ui.WarnCalls, ui.InfoCalls, ui.VerboseCalls, ui.OutCalls, ui.PrintCalls)
	}
}

// TestAdoptLegacyIdentityAlreadyAtCheckoutSlug covers the re-run case: a
// checkout-keyed state file makes the trigger false even while the legacy
// directory still exists, so the legacy state is deliberately left alone.
func TestAdoptLegacyIdentityAlreadyAtCheckoutSlug(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyStateFile := seedLegacyState(t)
	if err := seedHomeState(adoptCheckoutKey(), state.HomeState{HomeVolume: "home-bbb"}); err != nil {
		t.Fatalf("seed checkout state: %v", err)
	}

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called for an already-adopted project", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if got != (IdentityAdoption{Slug: adoptCheckoutSlug}) {
		t.Errorf("adoption = %+v, want a no-op at the checkout slug", got)
	}
	if _, statErr := os.Stat(legacyStateFile); statErr != nil {
		t.Errorf("legacy state file moved by a no-op adoption: %v", statErr)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("expected no notice, got %v", ui.WarnCalls)
	}
}

// TestAdoptLegacyIdentityUnparsableLegacyState verifies the fail-closed
// predicate: an unreadable legacy document aborts adoption before anything is
// destroyed or moved, because the caller cannot know what it would be adopting.
func TestAdoptLegacyIdentityUnparsableLegacyState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyDir := filepath.Join(state.SlugDir(adoptRepoSlug), adoptAgentName)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy state dir: %v", err)
	}
	testutil.WriteFile(t, legacyDir, "state.yaml", `": [`)

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName()}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(
		context.Background(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected an unparsable legacy state to fail adoption")
	}

	if handle.Destroyed {
		t.Error("legacy VM destroyed despite an unparsable legacy state")
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved despite an unparsable legacy state")
	}
	if _, statErr := os.Stat(filepath.Join(legacyDir, "state.yaml")); statErr != nil {
		t.Errorf("legacy state file removed: %v", statErr)
	}
}

// TestAdoptLegacyIdentityApplyFalseIsReadOnly covers the dry run: the notice is
// printed under the "would" headline, nothing is touched, and the caller is told
// to keep resolving against the legacy slug.
func TestAdoptLegacyIdentityApplyFalseIsReadOnly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyStateFile := seedLegacyState(t)

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called during a dry run", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, false, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	notice := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{
		"Would migrate this checkout to its own project identity",
		state.SlugDir(adoptCheckoutSlug),
		adoptLegacyVolume,
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("dry-run notice missing %q:\n%s", want, notice)
		}
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved during a dry run")
	}
	if _, statErr := os.Stat(legacyStateFile); statErr != nil {
		t.Errorf("legacy state removed during a dry run: %v", statErr)
	}
	want := IdentityAdoption{Slug: adoptRepoSlug, Pending: true, Applied: false}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
}

// TestAdoptLegacyIdentityLegacyVMAlreadyGone verifies that an absent legacy VM
// does not block adoption: the state move is the part that matters.
func TestAdoptLegacyIdentityLegacyVMAlreadyGone(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if !statePresent(t, adoptCheckoutKey()) {
		t.Error("state not moved although the legacy VM was already gone")
	}
	if got.Applied != true {
		t.Errorf("adoption = %+v, want it applied", got)
	}
}

// TestAdoptLegacyIdentityLegacyVMReplacedFailsClosed verifies the
// identity-aware destroy: the name now belongs to a sandbox this run did not
// create, so the VM is left alone and the state is not moved.
func TestAdoptLegacyIdentityLegacyVMReplacedFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msb.MockSandboxHandle{
		Name_:      adoptLegacyVMName(),
		DestroyErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "replaced"},
	}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err == nil {
		t.Fatal("expected a replaced legacy VM to fail adoption")
	}
	if !msb.IsReplaced(err) {
		t.Errorf("error = %v, want it to wrap ErrSandboxReplaced", err)
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved despite a replaced legacy VM")
	}
	if !statePresent(t, adoptRepoKey()) {
		t.Error("legacy state removed despite a replaced legacy VM")
	}
}

// TestAdoptLegacyIdentityStrandedStateFileFailsLoudly covers the mandatory
// post-move check. A dangling symlink at the destination agent directory makes
// the merge refuse to overwrite it and report a skip, so the legacy state file
// never arrives. Continuing would run under the checkout identity with no state
// — a fresh home volume and the agent's auth silently gone — so adoption must
// fail loudly instead.
func TestAdoptLegacyIdentityStrandedStateFileFailsLoudly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyStateFile := seedLegacyState(t)

	checkoutSlugDir := state.SlugDir(adoptCheckoutSlug)
	if err := os.MkdirAll(checkoutSlugDir, 0o700); err != nil {
		t.Fatalf("create checkout state dir: %v", err)
	}
	if err := os.Symlink(
		filepath.Join(t.TempDir(), "nowhere"),
		filepath.Join(checkoutSlugDir, adoptAgentName),
	); err != nil {
		t.Fatalf("create blocking symlink: %v", err)
	}

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName()}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err == nil {
		t.Fatal("expected a stranded state file to fail adoption")
	}
	if !strings.Contains(err.Error(), legacyStateFile) {
		t.Errorf("error = %v, want it to name the legacy state file %s", err, legacyStateFile)
	}
	if !strings.Contains(err.Error(), adoptAgentName) {
		t.Errorf("error = %v, want it to name the skipped entry %s", err, adoptAgentName)
	}
	if _, statErr := os.Stat(legacyStateFile); statErr != nil {
		t.Errorf("legacy state file gone after a loud failure: %v", statErr)
	}
}
