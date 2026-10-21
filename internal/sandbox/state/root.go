package state

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/inoio/agents-sandbox/internal/atomicwrite"

	"gopkg.in/yaml.v3"
)

// RootState is the launcher-global state document at the top of the state root,
// distinct from per-project state files under <root>/<slug>/<agent>/state.yaml.
//
//exhaustruct:ignore
type RootState struct {
	Migrations map[string]Migration       `yaml:"migrations,omitempty"`
	Agents     map[string]AgentCheckState `yaml:"agents,omitempty"`
	// AdoptedHomeVolumes records, per legacy project identity, the checkout that
	// adopted its lost-state home volume (#144). See AdoptedHomeVolume.
	AdoptedHomeVolumes map[string]AdoptedHomeVolume `yaml:"adopted_home_volumes,omitempty"`
}

// MigrationStatus is the terminal or in-flight state of a one-time migration.
// The absent entry (no key) is the pending state and has no status value.
type MigrationStatus string

const (
	MigrationCopying      MigrationStatus = "copying"
	MigrationCompleted    MigrationStatus = "completed"
	MigrationDismissed    MigrationStatus = "dismissed"
	MigrationAcknowledged MigrationStatus = "acknowledged"
)

// Migration tracks one one-time migration.
//
// While Status is MigrationCopying the three lists are disjoint and together
// account for every legacy volume the campaign has considered:
//   - Copied: fully migrated into the private home.
//   - Kept: dismissed from the migration at the user's choice (option "copy the
//     missing volumes"); the present private volume is intentionally left as-is,
//     so its contents are unverified.
//   - Excluded: dropped from the migration by a copy failure; the partial
//     destination has been removed.
//
// At a terminal status, Kept and Excluded are dropped and Copied is retained as
// the host-owned record of the volumes that were fully migrated. The
// post-migration notice uses it to tell which legacy volumes are safe to remove:
// a Kept volume shares a name with a present private volume but is not Copied, so
// presence alone is never proof that a legacy volume was copied.
//
//exhaustruct:ignore
type Migration struct {
	Status   MigrationStatus `yaml:"status"`
	Copied   []string        `yaml:"copied,omitempty"`
	Kept     []string        `yaml:"kept,omitempty"`
	Excluded []string        `yaml:"excluded,omitempty"`
}

// Migration returns the named migration and whether it is present. A missing
// registry or entry is the pending state: the zero Migration.
func (s *RootState) Migration(name string) (Migration, bool) {
	m, ok := s.Migrations[name]
	return m, ok
}

// SetMigration records a migration entry, initializing the registry if nil.
func (s *RootState) SetMigration(name string, m Migration) {
	if s.Migrations == nil {
		s.Migrations = map[string]Migration{}
	}
	s.Migrations[name] = m
}

// AdoptedHomeVolume records the checkout that adopted a legacy project
// identity's lost-state home volume (#144). A legacy identity (every clone of
// one origin) has one VM and therefore one home volume, so exactly one checkout
// may own it. The record is keyed under the *legacy* identity so a second clone
// can see the claim and start fresh, instead of both recovering the same volume
// and sharing one home between two VMs.
//
//exhaustruct:ignore
type AdoptedHomeVolume struct {
	CheckoutSlug string `yaml:"checkout_slug"`
	Volume       string `yaml:"volume"`
}

// adoptedHomeVolumeKey keys an adoption claim by the legacy project identity;
// neither a slug nor an agent name contains "/", so the join is unambiguous.
func adoptedHomeVolumeKey(slug, agent string) string { return slug + "/" + agent }

// AdoptedHomeVolume returns the adoption claim for a legacy project identity.
func (s *RootState) AdoptedHomeVolume(slug, agent string) (AdoptedHomeVolume, bool) {
	claim, ok := s.AdoptedHomeVolumes[adoptedHomeVolumeKey(slug, agent)]
	return claim, ok
}

// ClaimAdoptedHomeVolume records checkoutSlug as the owner of the home volume a
// legacy project identity's VM mounted, initializing the registry if nil.
func (s *RootState) ClaimAdoptedHomeVolume(slug, agent, checkoutSlug, volume string) {
	if s.AdoptedHomeVolumes == nil {
		s.AdoptedHomeVolumes = map[string]AdoptedHomeVolume{}
	}
	s.AdoptedHomeVolumes[adoptedHomeVolumeKey(slug, agent)] = AdoptedHomeVolume{
		CheckoutSlug: checkoutSlug,
		Volume:       volume,
	}
}

// ForgetAdoptedHomeVolume removes every claim a checkout holds for the given
// volume, once the volume has been recovered into the checkout's own state.
func (s *RootState) ForgetAdoptedHomeVolume(checkoutSlug, volume string) {
	for key, claim := range s.AdoptedHomeVolumes {
		if claim.CheckoutSlug == checkoutSlug && claim.Volume == volume {
			delete(s.AdoptedHomeVolumes, key)
		}
	}
}

// ClearAdoptedHomeVolume retires a checkout's adoption claim for a recovered
// volume. It is best-effort bookkeeping: callers warn on error, since a stale
// claim is harmless once the legacy VM is gone (no other checkout can adopt it).
func ClearAdoptedHomeVolume(checkoutSlug, volume string) error {
	if volume == "" {
		return nil
	}
	_, err := WithRootState(true, func(txn *RootTxn) error {
		before := len(txn.State().AdoptedHomeVolumes)
		txn.State().ForgetAdoptedHomeVolume(checkoutSlug, volume)
		if len(txn.State().AdoptedHomeVolumes) == before {
			return nil
		}
		return txn.Commit()
	})
	return err
}

// AgentCheckInterval is the minimum time between network upgrade checks for an
// agent, shared machine-wide.
const AgentCheckInterval = 24 * time.Hour

// AgentCheckState is the machine-global network-check state for one agent,
// keyed by agent name. It caches the last check time and the latest version
// observed, so the upgrade prompt never names another project's version.
//
//exhaustruct:ignore
type AgentCheckState struct {
	LastChecked   time.Time `yaml:"last_checked"`
	LatestVersion string    `yaml:"latest_version"`
}

// DueForCheck reports whether the network check is due at time t: never checked
// or at least AgentCheckInterval since the last check.
func (c AgentCheckState) DueForCheck(t time.Time) bool {
	return c.LastChecked.IsZero() || t.Sub(c.LastChecked) >= AgentCheckInterval
}

// AgentCheck returns the named agent's check state. A missing entry is the zero
// AgentCheckState, whose zero LastChecked is always due.
func (s *RootState) AgentCheck(name string) AgentCheckState {
	return s.Agents[name]
}

// SetAgentCheck records an agent check state, initializing the registry if nil.
func (s *RootState) SetAgentCheck(name string, c AgentCheckState) {
	if s.Agents == nil {
		s.Agents = map[string]AgentCheckState{}
	}
	s.Agents[name] = c
}

func rootStateFile() string {
	return filepath.Join(stateRoot(), "state.yaml")
}

// marshalRootState is a seam over yaml.Marshal so tests can exercise the
// marshal failure path, which the concrete RootState shape cannot trigger.
var marshalRootState = yaml.Marshal

// ReadRootState loads the launcher-global state. A missing file yields a zero
// RootState with no error; a parse failure is wrapped with ErrStateUnparsable
// (as in ReadState) and other I/O failures are returned plain.
func ReadRootState() (*RootState, error) {
	path := rootStateFile()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RootState{}, nil
		}
		return nil, fmt.Errorf("read root state file %s: %w", path, err)
	}
	state, err := parseRootState(data)
	if err != nil {
		return nil, fmt.Errorf("parse root state file %s: %w", path, err)
	}
	return &state, nil
}

// parseRootState is the pure unmarshal step of ReadRootState: arbitrary bytes
// in, RootState or ErrStateUnparsable-wrapped error out, no file I/O. Kept
// separate so FuzzParseState can drive it without a filesystem.
func parseRootState(data []byte) (RootState, error) {
	var state RootState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return RootState{}, fmt.Errorf("%w: %w", ErrStateUnparsable, err)
	}
	return state, nil
}

// writeRootState atomically writes the launcher-global state to disk. Unexported
// so every root-state mutation flows through WithRootState.
func writeRootState(s RootState) error {
	data, err := marshalRootState(s)
	if err != nil {
		return fmt.Errorf("marshal root state: %w", err)
	}
	if err := atomicwrite.WriteFile(rootStateFile(), data); err != nil {
		return fmt.Errorf("write root state: %w", err)
	}
	return nil
}
