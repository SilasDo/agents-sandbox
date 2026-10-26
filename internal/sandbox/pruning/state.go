package pruning

import (
	"context"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// PruneState is a point-in-time view of the project VMs and task sandboxes in
// the private runtime home. ToPrune holds the project/agent keys whose project
// VM is being reclaimed (a stale, stopped VM) with its handle; ToKeep holds the
// keys that have a live, kept project VM. A key in neither set has no project VM
// at all, so its cached artifacts (e.g. runner images, unrecorded home volumes)
// are dangling and can be reclaimed. Tasks holds the transient task sandboxes to
// reclaim; they are deliberately kept out of the key maps so a leftover task can
// never mark its still-active project as stale.
//
// Age is the staleness threshold the snapshot was built with, reused by image
// retention as the window in which a live project's cached image variant counts
// as reusable.
type PruneState struct {
	Age     time.Duration
	ToPrune map[state.Key]msb.SandboxHandle
	ToKeep  map[state.Key]msb.SandboxHandle
	Tasks   []msb.SandboxHandle
}

func buildPruneState(ctx context.Context, age time.Duration) (PruneState, error) {
	result := PruneState{
		Age:     age,
		ToPrune: map[state.Key]msb.SandboxHandle{},
		ToKeep:  map[state.Key]msb.SandboxHandle{},
		Tasks:   nil,
	}
	handles, err := msb.Get().ListSandboxes(ctx, nil)
	if err != nil {
		return result, err
	}
	for _, h := range handles {
		identity := noteIdentity(h)
		if identity.Slug == "" {
			continue
		}
		if identity.Task {
			// Task sandboxes are transient workers: always reclaimable once
			// stopped, and never representative of a project's liveness.
			if msb.IsSandboxInactive(h.Status()) {
				result.Tasks = append(result.Tasks, h)
			}
			continue
		}
		key := state.Key{Slug: identity.Slug, Agent: identity.Agent}
		if prunable(h, age) {
			result.ToPrune[key] = h
			continue
		}
		result.ToKeep[key] = h
	}
	return result, nil
}

// noteIdentity resolves a sandbox's project key and kind, reading its labels
// (authoritative for anything the launcher created) and falling back to the name
// heuristic for a sandbox created before the labels existed. A config read error
// degrades to the name fallback rather than failing the whole prune.
func noteIdentity(h msb.SandboxHandle) naming.ArtifactInfo {
	var labels map[string]string
	if cfg, err := h.Config(); err == nil && cfg != nil {
		labels = cfg.Labels
	}
	return naming.SandboxIdentity(h.Name(), labels)
}

// prunable reports whether a project VM should be reclaimed: a stopped VM older
// than the age threshold.
func prunable(h msb.SandboxHandle, age time.Duration) bool {
	if !msb.IsSandboxInactive(h.Status()) {
		return false
	}
	return time.Since(h.UpdatedAt()) >= age
}
