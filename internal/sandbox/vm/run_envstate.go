package vm

import (
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// persistFingerprints records the env/secret/network/mount fingerprints in one
// home-state transaction, keeping every other field.
func persistFingerprints(k state.Key, fingerprints state.Fingerprints) error {
	return state.WithHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		*cur = fingerprints.Apply(*cur)
		return true, nil
	})
}
