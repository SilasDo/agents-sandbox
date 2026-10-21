package vm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// IdentityAdoption reports how a legacy origin-keyed project identity was
// adopted for the current checkout (#144).
//
//exhaustruct:ignore
type IdentityAdoption struct {
	// Slug is the effective project slug for the caller's remaining work: the
	// checkout slug, or the legacy repo slug while a pending migration is left
	// unapplied so a dry run resolves read-only against the old location.
	Slug string
	// Pending reports whether a legacy origin-keyed identity was found and
	// needed adopting.
	Pending bool
	// Applied reports whether the state directory was moved onto the checkout
	// slug this call. False when nothing was pending or the caller asked for a
	// dry run.
	Applied bool
}

// AdoptLegacyIdentity moves a project from the legacy origin-keyed identity to
// its checkout-scoped one on the first run after upgrade (#144).
//
// Before the identity split, one origin-derived slug keyed the state directory,
// the VM, the home volume and the runner image, so two clones of one remote
// collided on a single VM. Adoption destroys the legacy VM first and then merges
// the state directory onto the checkout slug. That order is crash-safe: a crash
// in between leaves the legacy state in place, so the next run re-adopts and
// merely recreates the VM, while moving first would leave an untracked VM
// running against the shared home volume. The merge is the idempotence record —
// the legacy state file is gone afterwards, so adoption cannot re-fire.
//
// With apply=false (a dry run) it prints the notice and leaves everything in
// place, returning the legacy slug so the rest of the pipeline keeps resolving
// against the pre-migration location.
//
// The destroy-and-move runs inside one root-state transaction, so two concurrent
// first-post-upgrade runs cannot both destroy and move.
func AdoptLegacyIdentity(
	ctx context.Context,
	client msb.Client,
	identity git.Identity,
	agentName string,
	apply bool,
	ui termio.UI,
) (IdentityAdoption, error) {
	repoKey := state.Key{Slug: identity.RepoSlug, Agent: agentName}
	checkoutKey := state.Key{Slug: identity.CheckoutSlug, Agent: agentName}

	legacy, pending, err := legacyStatePending(repoKey, checkoutKey)
	if err != nil {
		return IdentityAdoption{}, fmt.Errorf("check for a pending legacy identity: %w", err)
	}
	if !pending {
		return IdentityAdoption{Slug: identity.CheckoutSlug}, nil
	}

	ui.Warnf("%s", adoptionNotice(identity, agentName, legacy.HomeVolume, apply))

	if !apply {
		return IdentityAdoption{Slug: identity.RepoSlug, Pending: true}, nil
	}

	_, err = state.WithRootState(true, func(_ *state.RootTxn) error {
		return adoptLegacyIdentity(ctx, client, identity, repoKey, checkoutKey, ui)
	})
	if err != nil {
		return IdentityAdoption{}, err
	}
	return IdentityAdoption{Slug: identity.CheckoutSlug, Pending: true, Applied: true}, nil
}

// legacyStatePending reports whether the adoption trigger holds and returns the
// legacy origin-keyed state it read: the legacy state file exists and the
// checkout-keyed one does not. Returning the state avoids a second read that
// would race a concurrent writer after the predicate already proved it readable.
//
// The predicate is state-file absence, not directory absence, so a stray or
// aborted run that created the empty checkout directory cannot permanently skip
// migration, and it is re-evaluated on every run until it fires.
//
// It fails closed on reads: an unparsable or otherwise unreadable legacy state
// is returned as an error rather than treated as absent, so an unknown document
// can never lead to destroying the legacy VM or moving state nobody has read.
func legacyStatePending(repoKey, checkoutKey state.Key) (*state.HomeState, bool, error) {
	legacy, err := state.ReadState(repoKey)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}

	if _, err := state.ReadState(checkoutKey); err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			return legacy, true, nil
		}
		return nil, false, err
	}
	return nil, false, nil
}

// adoptLegacyIdentity destroys the legacy VM and merges the legacy state
// directory onto the checkout slug, with the root transaction's lock held.
func adoptLegacyIdentity(
	ctx context.Context,
	client msb.Client,
	identity git.Identity,
	repoKey, checkoutKey state.Key,
	ui termio.UI,
) error {
	if err := destroyLegacyVM(ctx, client, repoKey, ui); err != nil {
		return err
	}

	skipped, err := state.MoveSlugDir(identity.RepoSlug, identity.CheckoutSlug)
	if err != nil {
		return fmt.Errorf("move project state to %s: %w", state.SlugDir(identity.CheckoutSlug), err)
	}
	for _, entry := range skipped {
		ui.Warnf("left %s in the legacy state directory %s", entry, state.SlugDir(identity.RepoSlug))
	}

	// The merge is what makes the state-file trigger a real idempotence record:
	// it always delivers <agent>/state.yaml, because the trigger guarantees no
	// state file exists at the destination. If it did not arrive, continuing
	// would run under the checkout identity with no state, silently provisioning
	// a fresh home volume and losing the agent's auth. Failing loudly is the only
	// outcome that cannot lose the user's volume, so never fall back here. The
	// notice has already promised a move and the legacy VM is gone by now, so the
	// error has to say what is half-done and what to clear to finish it; the read
	// error is wrapped for a non-not-found failure (I/O, parse).
	if _, verifyErr := state.ReadState(checkoutKey); verifyErr != nil {
		return fmt.Errorf(
			"migration to the checkout identity did not complete: the legacy state file %s did not arrive at %s (the move skipped [%s], read error: %w); the legacy project VM %s is already gone, so clear those entries under the checkout state directory %s and rerun",
			filepath.Join(state.KeyDir(repoKey), "state.yaml"),
			filepath.Join(state.KeyDir(checkoutKey), "state.yaml"),
			strings.Join(skipped, ", "),
			verifyErr,
			projectVMName(repoKey),
			state.SlugDir(identity.CheckoutSlug),
		)
	}
	return nil
}

// destroyLegacyVM destroys the legacy origin-keyed project VM if it exists,
// through the shared identity-safe teardown (destroyProjectVMHandle): a name
// that is already gone needs no destroy, but a handle whose identity was
// replaced belongs to a sandbox this run did not create, so adopting it would
// destroy a stranger's VM and the adoption stops instead.
func destroyLegacyVM(ctx context.Context, client msb.Client, repoKey state.Key, ui termio.UI) error {
	name := projectVMName(repoKey)

	handle, err := client.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsNotFound(err) {
			ui.Verbosef("legacy project VM %s is already gone", name)
			return nil
		}
		return fmt.Errorf("check legacy project VM %q: %w", name, err)
	}

	alreadyGone, destroyErr := destroyProjectVMHandle(
		ctx,
		handle,
		name,
		"rerun to finish migrating this checkout",
	)
	if destroyErr != nil {
		return destroyErr
	}
	if alreadyGone {
		ui.Verbosef("legacy project VM %s was already gone; continuing with the state move", name)
	} else {
		ui.Verbosef("destroyed legacy project VM %s", name)
	}
	return nil
}

// adoptionNotice renders the whole migration notice as one block: a headline,
// a reassurance line, and the indented detail rows. It is emitted before any
// work starts, so its rows are prospective ("destroyed", "recreated") rather
// than a receipt, and one Warnf call carries it because the notice must survive
// --quiet — it changes where the project's state lives.
func adoptionNotice(identity git.Identity, agentName, legacyVolume string, apply bool) string {
	headline := "Migrating this checkout to its own project identity (repository + checkout slugs)."
	if !apply {
		headline = "Would migrate this checkout to its own project identity (repository + checkout slugs)."
	}
	homeVolume := legacyVolume
	if homeVolume == "" {
		homeVolume = "(none recorded)"
	}

	return fmt.Sprintf(`%s
No action needed: files, credentials and the home volume are kept; only the project VM is recreated.

  state dir     %s  ->  %s
  home volume   %s   (kept as-is)
  VM            %s   (destroyed)
                %s   (recreated)
  runner image  %s  ->  %s   (rebuilt)`,
		headline,
		state.SlugDir(identity.RepoSlug), state.SlugDir(identity.CheckoutSlug),
		homeVolume,
		projectVMName(state.Key{Slug: identity.RepoSlug, Agent: agentName}),
		projectVMName(state.Key{Slug: identity.CheckoutSlug, Agent: agentName}),
		naming.RunnerImageRef(identity.RepoSlug, agentName),
		naming.RunnerImageRef(identity.CheckoutSlug, agentName),
	)
}
