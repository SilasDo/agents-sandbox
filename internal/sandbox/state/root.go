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
	Migrations map[string]Migration `yaml:"migrations,omitempty"`
	Updates    Updates              `yaml:"updates,omitempty"`
}

// Updates groups the launcher-global update-check state: Launcher for
// agents-sandbox itself, Agents for the tools baked into the runner image.
type Updates struct {
	Launcher LauncherUpdateState        `yaml:"launcher,omitempty"`
	Agents   map[string]AgentCheckState `yaml:"agents,omitempty"`
}

// IsZero reports whether the namespace holds nothing, so yaml.v3's omitempty
// drops the key entirely.
func (u Updates) IsZero() bool {
	return u.Launcher.IsZero() && len(u.Agents) == 0
}

// LauncherUpdateState is the launcher's own update-check state, formerly the
// standalone upgrade.json file.
type LauncherUpdateState struct {
	LastChecked       time.Time `yaml:"last_checked"`
	DismissedVersions []string  `yaml:"dismissed_versions,omitempty"`
}

// IsZero reports whether no launcher check has been recorded.
func (l LauncherUpdateState) IsZero() bool {
	return l.LastChecked.IsZero() && len(l.DismissedVersions) == 0
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

// AgentCheck returns the named agent's check state from updates.agents. A
// missing entry is the zero AgentCheckState, whose zero LastChecked is always
// due.
func (s *RootState) AgentCheck(name string) AgentCheckState {
	return s.Updates.Agents[name]
}

// SetAgentCheck records an agent check state under updates.agents,
// initializing the registry if nil.
func (s *RootState) SetAgentCheck(name string, c AgentCheckState) {
	if s.Updates.Agents == nil {
		s.Updates.Agents = map[string]AgentCheckState{}
	}
	s.Updates.Agents[name] = c
}

// LauncherUpdate returns the launcher's own update-check state
// (updates.launcher).
func (s *RootState) LauncherUpdate() LauncherUpdateState {
	return s.Updates.Launcher
}

// SetLauncherUpdate records the launcher's update-check state.
func (s *RootState) SetLauncherUpdate(l LauncherUpdateState) {
	s.Updates.Launcher = l
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
//
// It also folds the legacy top-level agents: key (pre-updates: root documents)
// into Updates.Agents, so an old document reads correctly and the next write
// normalizes it. The new shape wins: a non-empty updates.agents is adopted as-is
// and the legacy key ignored, while updates.launcher survives either way.
func parseRootState(data []byte) (RootState, error) {
	var wire struct {
		Migrations   map[string]Migration       `yaml:"migrations"`
		Updates      Updates                    `yaml:"updates"`
		LegacyAgents map[string]AgentCheckState `yaml:"agents"`
	}
	if err := yaml.Unmarshal(data, &wire); err != nil {
		return RootState{}, fmt.Errorf("%w: %w", ErrStateUnparsable, err)
	}
	if len(wire.Updates.Agents) == 0 {
		wire.Updates.Agents = wire.LegacyAgents
	}
	return RootState{Migrations: wire.Migrations, Updates: wire.Updates}, nil
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
