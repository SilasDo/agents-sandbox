package upgrade

import "time"

// LauncherState is the launcher's own upgrade-check state: when the GitHub
// release check last ran and which newer versions the user asked not to be
// notified about again.
type LauncherState struct {
	LastChecked       time.Time
	DismissedVersions []string
}

// Store reads and writes LauncherState. The launcher injects a
// root-state-backed implementation that keeps the state in the launcher-global
// state.yaml document; a nil Store falls back to memoryStore, which checks
// normally but persists nothing.
type Store interface {
	Load() (LauncherState, error)
	Save(LauncherState) error
}

// memoryStore is the default Store for a caller that injects none. It keeps
// state in memory, so an unconfigured check behaves identically within one
// process and leaves no trace behind.
type memoryStore struct {
	state LauncherState
}

func (m *memoryStore) Load() (LauncherState, error) { return m.state, nil }

func (m *memoryStore) Save(s LauncherState) error {
	m.state = s
	return nil
}
