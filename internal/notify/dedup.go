package notify

import (
	"fmt"
	"sync"

	sandboxstate "github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// claimer acquires named cross-process claims. It is an interface so tests can
// substitute a fake; production uses StateClaimer.
type claimer interface {
	Acquire(slug, key string) (release func(), claimed bool, err error)
}

// StateClaimer delegates claim acquisition to the shared sandbox state dir.
type StateClaimer struct{}

// Acquire claims a named token under the slug's state dir, returning a release
// func the holder calls when the claim should be freed.
func (StateClaimer) Acquire(slug, key string) (func(), bool, error) {
	return sandboxstate.AcquireClaim(slug, key)
}

// Dedup wraps a Backend so a given transition notifies at most once across
// every running client of the same project. The first client to claim the token
// forwards the notification to inner; the rest drop it. Claims are released via
// SessionBusy when the session returns to work, so a later transition for it
// can notify again.
//
// A turn can raise two input requests back to back in one step, so the session
// state cannot tell them apart; each notification carries the source event's
// envelope id instead. That id is kept in memory rather than in the token,
// because a token per request would leave a claim file per request — the state
// dir reuses one token per session and transition.
type Dedup struct {
	slug    string
	claimer claimer
	inner   Backend
	mu      sync.Mutex
	claims  map[string]map[Trigger]func()
	// seenInputs records, per session, the input event ids this client has
	// already forwarded, so the session's single input token can cover several
	// distinct requests without a token (and file) per request.
	seenInputs map[string]map[string]bool
}

// NewDedup builds a Backend that deduplicates notifications for slug across
// clients, forwarding claimed notifications to inner.
func NewDedup(slug string, c claimer, inner Backend) *Dedup {
	return &Dedup{
		slug:       slug,
		claimer:    c,
		inner:      inner,
		mu:         sync.Mutex{},
		claims:     map[string]map[Trigger]func(){},
		seenInputs: map[string]map[string]bool{},
	}
}

// claimKey derives the stable claim token for a session and trigger.
func claimKey(sessionID string, t Trigger) string {
	return fmt.Sprintf("%s-%d", sessionID, int(t))
}

// Notify claims the token for n and forwards it to inner only when this client
// wins the claim. Notifications without a session ID bypass dedup.
func (d *Dedup) Notify(n Notification) {
	if n.SessionID == "" {
		d.inner.Notify(n)
		return
	}
	if n.Trigger == TriggerInput {
		d.notifyInput(n)
		return
	}
	if d.held(n.SessionID, n.Trigger) {
		return
	}
	release, claimed, err := d.claimer.Acquire(d.slug, claimKey(n.SessionID, n.Trigger))
	if err != nil {
		// Fail open: deliver rather than risk dropping a real notification.
		d.inner.Notify(n)
		return
	}
	if !claimed {
		return
	}
	d.record(n.SessionID, n.Trigger, release)
	d.inner.Notify(n)
}

// notifyInput forwards a distinct input request. The session's single input
// token elects one client to deliver that session's input notifications (so
// claim files stay bounded); the in-memory seen set lets that one token cover
// the several requests a turn can raise, and drops a repeat of the same event.
func (d *Dedup) notifyInput(n Notification) {
	d.mu.Lock()
	if _, held := d.claims[n.SessionID][TriggerInput]; held {
		// This client owns the session's input token. It delivers another
		// request only when the envelope id differs; without an id it cannot
		// tell a new request from a repeat, so it drops.
		if n.EventID == "" || d.seenInputs[n.SessionID][n.EventID] {
			d.mu.Unlock()
			return
		}
	} else {
		d.mu.Unlock()
		release, claimed, err := d.claimer.Acquire(d.slug, claimKey(n.SessionID, TriggerInput))
		if err != nil {
			// Fail open: deliver rather than risk dropping a real notification.
			d.inner.Notify(n)
			return
		}
		if !claimed {
			return
		}
		d.record(n.SessionID, TriggerInput, release)
		d.mu.Lock()
	}
	if n.EventID != "" {
		if d.seenInputs[n.SessionID] == nil {
			d.seenInputs[n.SessionID] = map[string]bool{}
		}
		d.seenInputs[n.SessionID][n.EventID] = true
	}
	d.mu.Unlock()
	d.inner.Notify(n)
}

// record stores the release func for a session's trigger token.
func (d *Dedup) record(sessionID string, t Trigger, release func()) {
	d.mu.Lock()
	if d.claims[sessionID] == nil {
		d.claims[sessionID] = map[Trigger]func(){}
	}
	d.claims[sessionID][t] = release
	d.mu.Unlock()
}

// held reports whether this client already owns the claim for the session and
// trigger (a per-process shortcut over the authoritative flock).
func (d *Dedup) held(sessionID string, t Trigger) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.claims[sessionID][t]
	return ok
}

// SessionBusy releases every claim this client holds for the session, so a
// later transition for it can notify again. It is called by Watch when the
// session returns to work.
func (d *Dedup) SessionBusy(sessionID string) {
	d.mu.Lock()
	releases := d.claims[sessionID]
	delete(d.claims, sessionID)
	delete(d.seenInputs, sessionID)
	d.mu.Unlock()
	for _, release := range releases {
		release()
	}
}
