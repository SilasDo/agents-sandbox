package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inoio/agents-sandbox/internal/configpaths"
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

// adoptLockHook is a test seam invoked inside the locked adoption, before the
// trigger is re-checked. A test uses it to simulate another checkout adopting the
// shared legacy identity while this run waited for the root lock.
var adoptLockHook func()

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
// A bare root records a second legacy identity (its pre-split path-derived
// slug), so adoption merges from every legacy identity the caller resolved, in
// order. Migrating a directory moves every agent's state, so every moved agent's
// legacy VM is destroyed, not only the requested one; otherwise a still-running
// sibling agent would recreate itself over the same home volume.
//
// With apply=false (a dry run) it prints the notice and leaves everything in
// place, returning the legacy slug so the rest of the pipeline keeps resolving
// against the pre-migration location.
//
// The destroy-and-move runs inside one root-state transaction, so two concurrent
// first-post-upgrade runs cannot both destroy and move. The trigger is
// re-checked while the lock is held: a second run that lost the race to another
// checkout sees the state already moved and continues normally under the
// checkout identity instead of failing.
func AdoptLegacyIdentity(
	ctx context.Context,
	client msb.Client,
	identity git.Identity,
	agentName string,
	apply bool,
	ui termio.UI,
) (IdentityAdoption, error) {
	checkoutKey := state.Key{Slug: identity.CheckoutSlug, Agent: agentName}
	slugs := legacySlugs(identity)

	legacy, pending, err := legacyStatePending(slugs, checkoutKey)
	if err != nil {
		return IdentityAdoption{}, fmt.Errorf("check for a pending legacy identity: %w", err)
	}
	if !pending {
		return IdentityAdoption{Slug: identity.CheckoutSlug}, nil
	}

	if !apply {
		ui.Warnf("%s", adoptionNotice(identity, agentName, legacy.HomeVolume, false))
		return IdentityAdoption{Slug: identity.RepoSlug, Pending: true}, nil
	}

	var result IdentityAdoption
	_, err = state.WithRootState(true, func(_ *state.RootTxn) error {
		// Re-check under the lock: another checkout (or another agent of this
		// one) may have adopted the shared legacy identity between the pre-lock
		// check and acquiring the lock. If so there is nothing left to adopt and
		// the caller continues normally, rather than tripping the
		// incomplete-migration failure on a source that is already gone.
		if adoptLockHook != nil {
			adoptLockHook()
		}
		legacy, pending, recheckErr := legacyStatePending(slugs, checkoutKey)
		if recheckErr != nil {
			return fmt.Errorf("check for a pending legacy identity: %w", recheckErr)
		}
		if !pending {
			result = IdentityAdoption{Slug: identity.CheckoutSlug}
			return nil
		}
		ui.Warnf("%s", adoptionNotice(identity, agentName, legacy.HomeVolume, true))
		if adoptErr := adoptLegacyIdentity(ctx, client, identity, slugs, checkoutKey, ui); adoptErr != nil {
			return adoptErr
		}
		result = IdentityAdoption{Slug: identity.CheckoutSlug, Pending: true, Applied: true}
		return nil
	})
	if err != nil {
		return IdentityAdoption{}, err
	}
	return result, nil
}

// legacySlugs returns the ordered, de-duplicated origin-keyed identities to adopt
// from: the repo slug (the legacy identity of a normal clone or any worktree),
// followed by the extra identities a bare root records. The checkout slug is
// never a source: state already there needs no migration.
func legacySlugs(identity git.Identity) []string {
	seen := map[string]bool{identity.CheckoutSlug: true}
	var slugs []string
	for _, slug := range append([]string{identity.RepoSlug}, identity.LegacyRepoSlugs...) {
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		slugs = append(slugs, slug)
	}
	return slugs
}

// legacyStatePending reports whether any legacy identity has a state file for the
// agent while the checkout-keyed one does not, and returns the first legacy state
// it read so the notice can name its home volume.
//
// The predicate is state-file absence, not directory absence, so a stray or
// aborted run that created the empty checkout directory cannot permanently skip
// migration, and it is re-evaluated on every run until it fires. It is
// deliberately first-adopter-wins: once the checkout state exists, later callers
// stop, which is the accepted asymmetry of moving one shared identity instead of
// copying it.
//
// It fails closed on reads: an unparsable or otherwise unreadable state is
// returned as an error rather than treated as absent, so an unknown document can
// never lead to destroying a legacy VM or moving state nobody has read.
func legacyStatePending(slugs []string, checkoutKey state.Key) (*state.HomeState, bool, error) {
	if _, err := state.ReadState(checkoutKey); err == nil {
		return nil, false, nil
	} else if !errors.Is(err, state.ErrStateNotFound) {
		return nil, false, err
	}

	var legacy *state.HomeState
	for _, slug := range slugs {
		found, err := state.ReadState(state.Key{Slug: slug, Agent: checkoutKey.Agent})
		if err != nil {
			if errors.Is(err, state.ErrStateNotFound) {
				continue
			}
			return nil, false, err
		}
		if legacy == nil {
			legacy = found
		}
	}
	if legacy == nil {
		return nil, false, nil
	}
	return legacy, true, nil
}

// adoptLegacyIdentity destroys the legacy VMs and merges every legacy state
// directory onto the checkout slug, with the root transaction's lock held.
func adoptLegacyIdentity(
	ctx context.Context,
	client msb.Client,
	identity git.Identity,
	slugs []string,
	checkoutKey state.Key,
	ui termio.UI,
) error {
	primary := slugs[0]
	var skipped []string
	for _, slug := range slugs {
		if err := destroyLegacyVMs(ctx, client, slug, ui); err != nil {
			return err
		}
		sourceSkipped, err := state.MoveSlugDir(slug, identity.CheckoutSlug)
		if err != nil {
			return fmt.Errorf("move project state to %s: %w", state.SlugDir(identity.CheckoutSlug), err)
		}
		for _, entry := range sourceSkipped {
			ui.Warnf("left %s in the legacy state directory %s", entry, state.SlugDir(slug))
		}
		skipped = append(skipped, sourceSkipped...)
	}

	if err := migrateLegacyConfigDirs(identity, ui); err != nil {
		return err
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
		primaryKey := state.Key{Slug: primary, Agent: checkoutKey.Agent}
		return fmt.Errorf(
			"migration to the checkout identity did not complete: the legacy state file %s did not arrive at %s (the move skipped [%s], read error: %w); the legacy project VM %s is already gone, so clear those entries under the checkout state directory %s and rerun",
			filepath.Join(state.KeyDir(primaryKey), "state.yaml"),
			filepath.Join(state.KeyDir(checkoutKey), "state.yaml"),
			strings.Join(skipped, ", "),
			verifyErr,
			projectVMName(primaryKey),
			state.SlugDir(identity.CheckoutSlug),
		)
	}
	return nil
}

// destroyLegacyVMs destroys the legacy origin-keyed project VM of every agent
// whose state lives under slug. The whole slug directory is moved, so leaving a
// sibling agent's VM running would let it keep writing the shared home volume
// while a later run recreates that agent under the checkout name over the same
// volume.
func destroyLegacyVMs(ctx context.Context, client msb.Client, slug string, ui termio.UI) error {
	names, err := state.StatefulAgentNames(slug)
	if err != nil {
		return fmt.Errorf("list legacy state for %s: %w", slug, err)
	}
	for _, name := range names {
		if err := destroyLegacyVM(ctx, client, state.Key{Slug: slug, Agent: name}, ui); err != nil {
			return err
		}
	}
	return nil
}

// destroyLegacyVM destroys the legacy origin-keyed project VM of one agent if it
// exists, through the shared identity-safe teardown (destroyProjectVMHandle): a
// name that is already gone needs no destroy, but a handle whose identity was
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

// migrateLegacyConfigDirs preserves a legacy identity's per-slug user config
// directory by renaming it onto the repo slug, the one the launcher reads from
// now. It only applies to a legacy identity that is not already the repo slug (a
// bare root's path-derived slug); a source whose slug is the repo slug already
// reads the right directory. An absent legacy directory, or an existing target,
// is left alone.
func migrateLegacyConfigDirs(identity git.Identity, ui termio.UI) error {
	paths := configpaths.Get()
	for _, slug := range identity.LegacyRepoSlugs {
		if slug == identity.RepoSlug {
			continue
		}
		from := paths.UserSlugConfigDir(slug)
		to := paths.UserSlugConfigDir(identity.RepoSlug)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		if _, err := os.Stat(to); err == nil {
			ui.Verbosef("kept existing config dir %s; legacy config dir %s left in place", to, from)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return fmt.Errorf("create config dir %s: %w", filepath.Dir(to), err)
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("move config dir %s to %s: %w", from, to, err)
		}
		ui.Verbosef("moved legacy config dir %s to %s", from, to)
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

	var extraStateRows, configRow strings.Builder
	for _, slug := range identity.LegacyRepoSlugs {
		fmt.Fprintf(
			&extraStateRows,
			"\n                %s  ->  %s",
			state.SlugDir(slug),
			state.SlugDir(identity.CheckoutSlug),
		)
	}
	if len(identity.LegacyRepoSlugs) > 0 {
		paths := configpaths.Get()
		fmt.Fprintf(
			&configRow,
			"\n  config dir    %s  ->  %s   (if present)",
			paths.UserSlugConfigDir(identity.LegacyRepoSlugs[0]),
			paths.UserSlugConfigDir(identity.RepoSlug),
		)
	}

	return fmt.Sprintf(`%s
No action needed: files, credentials and the home volume are kept; only the project VM is recreated.

  state dir     %s  ->  %s%s
  home volume   %s   (kept as-is)
  VM            %s   (destroyed)
                %s   (recreated)
  runner image  %s  ->  %s   (rebuilt)%s`,
		headline,
		state.SlugDir(identity.RepoSlug), state.SlugDir(identity.CheckoutSlug), extraStateRows.String(),
		homeVolume,
		projectVMName(state.Key{Slug: identity.RepoSlug, Agent: agentName}),
		projectVMName(state.Key{Slug: identity.CheckoutSlug, Agent: agentName}),
		naming.RunnerImageRef(identity.RepoSlug, agentName),
		naming.RunnerImageRef(identity.CheckoutSlug, agentName),
		configRow.String(),
	)
}
