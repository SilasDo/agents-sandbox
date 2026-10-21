package vm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// legacyVMNotFound returns a client that serves one live legacy VM (the handle)
// for the legacy project VM name and "sandbox not found" for everything else, the
// shape a lost-state adoption starts from.
func legacyVMNotFound(handle msb.SandboxHandle) *msb.MockMsbClient {
	return &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == adoptLegacyVMName() {
			return handle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
}

func legacyVMWithHome(volume string) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{
		Name_: adoptLegacyVMName(),
		Mounts: map[string]msbSdk.MountConfig{
			mounts.VMHomeDir: msbSdk.Mount.Named(volume, msbSdk.MountOptions{}),
		},
	}
}

// TestPersistRecoveredHomeVolumeEmptyIsNoOp covers the guard for a VM with no
// home mount: there is nothing to preserve, so no state is written.
func TestPersistRecoveredHomeVolumeEmptyIsNoOp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := persistRecoveredHomeVolume(adoptCheckoutKey(), ""); err != nil {
		t.Fatalf("persistRecoveredHomeVolume: %v", err)
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("an empty recovered volume must not write a state file")
	}
}

// TestPersistRecoveredHomeVolumeReadError covers the write failure's wrapping: a
// directory at the state file path makes the recovery read fail with a
// non-not-found error, so the recorded volume is reported rather than lost.
func TestPersistRecoveredHomeVolumeReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := adoptCheckoutKey()
	if err := os.MkdirAll(filepath.Join(state.KeyDir(k), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := persistRecoveredHomeVolume(k, adoptLegacyVolume)
	if err == nil {
		t.Fatal("expected a read error to fail the recovered-volume write")
	}
	if !strings.Contains(err.Error(), "record the recovered home volume") {
		t.Errorf("error = %v, want it to name the recovered-volume write", err)
	}
}

// TestAdoptLegacyIdentityPersistRecoveredVolumeFailureFailsClosed covers the
// marker write failing after the claim committed: adoption aborts and, crucially,
// leaves the legacy VM running so the volume is not lost.
func TestAdoptLegacyIdentityPersistRecoveredVolumeFailureFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// The checkout state directory is read-only, so the marker write cannot
	// create its temp file. The read still sees a missing file (the trigger).
	keyDir := state.KeyDir(adoptCheckoutKey())
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyDir, 0o555); err != nil {
		t.Skipf("cannot make the checkout state dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(keyDir, 0o700) })

	handle := legacyVMWithHome(adoptLegacyVolume)
	ui := termio.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(
		context.Background(),
		legacyVMNotFound(handle),
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected the marker write failure to abort adoption")
	}
	if handle.Destroyed {
		t.Error("the legacy VM must survive a failed marker write")
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("a failed marker write must not leave a checkout state file")
	}
	// The claim commits before the marker, so the volume is already owned and
	// recoverable even though the marker write failed.
	root, rootErr := state.ReadRootState()
	if rootErr != nil {
		t.Fatalf("ReadRootState: %v", rootErr)
	}
	if _, ok := root.AdoptedHomeVolume(adoptRepoSlug, adoptAgentName); !ok {
		t.Error("the claim must commit before the marker, leaving the volume owned on a marker failure")
	}
}

// TestAdoptLegacyIdentityClaimCommitFailureFailsClosed covers the claim commit
// failing: the root-state write is the first durable step of a VM-only adoption,
// so its failure aborts before the marker and before the destroy.
func TestAdoptLegacyIdentityClaimCommitFailureFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Pre-create the root lock so a read-only state root still lets the
	// transaction acquire its lock and read (a missing root state file), leaving
	// only the final atomic write to fail. Pre-create the checkout state
	// directory too, so on the old ordering the marker write would succeed and the
	// assertion below would catch it.
	root := configpaths.Get().UserStateDir()
	lockPath := filepath.Join(root, "locks", "root.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state.KeyDir(adoptCheckoutKey()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Skipf("cannot make the state root read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	handle := legacyVMWithHome(adoptLegacyVolume)
	ui := termio.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(
		context.Background(),
		legacyVMNotFound(handle),
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected the claim commit failure to abort adoption")
	}
	if handle.Destroyed {
		t.Error("the legacy VM must survive a failed claim commit")
	}
	// The claim commits before the marker, so a failed commit must not leave the
	// recovery marker behind (which would let a second clone recover the volume).
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("a failed claim commit must not leave a recovery marker")
	}
}

// TestAdoptLegacyIdentityClaimVolumeMismatchFailsClosed covers the reconciled-
// claim guard: a claim this checkout owns but that names a different volume than
// the live legacy VM mounts cannot be trusted, so adoption refuses rather than
// destroying the VM and overwriting the record.
func TestAdoptLegacyIdentityClaimVolumeMismatchFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := seedRootState(state.RootState{AdoptedHomeVolumes: map[string]state.AdoptedHomeVolume{
		"widget-aaa/opencode": {CheckoutSlug: adoptCheckoutSlug, Volume: "some-other-volume"},
	}}); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	handle := legacyVMWithHome(adoptLegacyVolume)
	ui := termio.NewTestMock(t)

	_, err := AdoptLegacyIdentity(
		context.Background(),
		legacyVMNotFound(handle),
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err == nil {
		t.Fatal("expected a mismatched adoption claim to fail closed")
	}
	if !strings.Contains(err.Error(), "refusing to destroy it") {
		t.Errorf("error = %v, want the fail-closed refusal", err)
	}
	if handle.Destroyed {
		t.Error("the legacy VM must survive a mismatched adoption claim")
	}
}

// TestAdoptLegacyIdentityRecheckErrorPropagates covers the locked re-check
// failing: the legacy state became unreadable while this run waited for the root
// lock, so adoption reports it instead of proceeding on unknown state.
func TestAdoptLegacyIdentityRecheckErrorPropagates(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	legacyStatePath := seedLegacyState(t)
	adoptLockHook = func() {
		if err := os.WriteFile(legacyStatePath, []byte("{ corrupt: yaml: ["), 0o600); err != nil {
			t.Errorf("corrupt legacy state: %v", err)
		}
	}
	t.Cleanup(func() { adoptLockHook = nil })

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	ui := termio.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err == nil {
		t.Fatal("expected the locked re-check to propagate the unreadable legacy state")
	}
	if !strings.Contains(err.Error(), "check for a pending legacy identity") {
		t.Errorf("error = %v, want the pending-identity check failure", err)
	}
}
