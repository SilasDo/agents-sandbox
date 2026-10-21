package notify

import (
	"errors"
	"sync"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// sharedSink records notifications from multiple Dedup instances backed by the
// same underlying sink.
type sharedSink struct {
	mu  sync.Mutex
	got []Notification
}

func (s *sharedSink) Notify(n Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, n)
}

func (s *sharedSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func TestDedupCrossClientSingleNotification(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "dedupcross-a"
	sink := &sharedSink{}

	c1 := NewDedup(slug, StateClaimer{}, sink)
	c2 := NewDedup(slug, StateClaimer{}, sink)

	n := Notification{SessionID: "ses_A", Trigger: TriggerDone, Title: "opencode: done", Body: "x"}
	c1.Notify(n) // first claim -> delivered
	c2.Notify(n) // already claimed -> dropped
	if sink.count() != 1 {
		t.Fatalf("expected 1 notification across two clients, got %d", sink.count())
	}

	// Session returns to work, releasing the claim.
	c1.SessionBusy("ses_A")
	c2.Notify(n) // now a fresh transition can claim again
	if sink.count() != 2 {
		t.Fatalf("expected 2nd notification after SessionBusy, got %d", sink.count())
	}
}

func TestDedupDifferentSessionsIndependent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "dedupses-a"
	sink := &sharedSink{}
	c := NewDedup(slug, StateClaimer{}, sink)

	c.Notify(Notification{SessionID: "ses_A", Trigger: TriggerDone})
	c.Notify(Notification{SessionID: "ses_B", Trigger: TriggerDone})
	if sink.count() != 2 {
		t.Fatalf("expected 2 notifications for 2 sessions, got %d", sink.count())
	}
}

func TestDedupDifferentTriggersIndependent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "deduptrig-a"
	sink := &sharedSink{}
	c := NewDedup(slug, StateClaimer{}, sink)

	c.Notify(Notification{SessionID: "ses_A", Trigger: TriggerDone})
	c.Notify(Notification{SessionID: "ses_A", Trigger: TriggerInput})
	if sink.count() != 2 {
		t.Fatalf("expected 2 notifications for 2 triggers, got %d", sink.count())
	}
}

func TestDedupBypassesWhenNoSessionID(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "dedupnosid-a"
	sink := &sharedSink{}
	c := NewDedup(slug, StateClaimer{}, sink)

	c.Notify(Notification{Trigger: TriggerDone})
	c.Notify(Notification{Trigger: TriggerDone})
	if sink.count() != 2 {
		t.Fatalf("expected 2 notifications without session ID, got %d", sink.count())
	}
}

// sseBlockFor builds an SSE block carrying a sessionID, so dedup is exercised.
func sseBlockFor(typ, sessionID string) []byte {
	return []byte(
		"data: {\"directory\":\"/workspace\",\"project\":\"p\",\"payload\":{\"id\":\"evt\",\"type\":\"" + typ + "\",\"properties\":{\"sessionID\":\"" + sessionID + "\"}}}\n\n",
	)
}

func TestDedupSameClientSecondNotifyDropped(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	sink := &sharedSink{}
	c := NewDedup("dedupheld-a", StateClaimer{}, sink)

	n := Notification{SessionID: "ses_A", Trigger: TriggerDone}
	c.Notify(n) // claims and delivers
	c.Notify(n) // held locally -> dropped
	if sink.count() != 1 {
		t.Fatalf("expected 1 notification after a repeat, got %d", sink.count())
	}
}

// TestHandleEventSameStepInputRequestsBothNotify drives the full path for the
// v2 gap: two approvals asked back to back in one step, with no step boundary
// between them. The first request claims the input token; the second carries a
// distinct envelope id, claims its own token, and notifies too.
func TestHandleEventSameStepInputRequestsBothNotify(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	sink := &sharedSink{}
	dedup := NewDedup("dedupinturn-b", StateClaimer{}, sink)
	tr := NewTracker(opencode2Spec())
	sid := "ses_top"

	handleEvent(tr, dedup, agent.StreamEvent{Type: "session.execution.started", SessionID: sid})
	handleEvent(tr, dedup, agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_1"})
	handleEvent(tr, dedup, agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_2"})

	if sink.count() != 2 {
		t.Fatalf("expected both same-step input requests to notify, got %d", sink.count())
	}
}

// TestHandleEventRepeatedInputEventNotifiesOnce is the other direction: the
// same envelope id (a replayed event) claims the same token and notifies once.
func TestHandleEventRepeatedInputEventNotifiesOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	sink := &sharedSink{}
	dedup := NewDedup("dedupinturn-c", StateClaimer{}, sink)
	tr := NewTracker(opencode2Spec())
	sid := "ses_top"

	handleEvent(tr, dedup, agent.StreamEvent{Type: "session.execution.started", SessionID: sid})
	handleEvent(tr, dedup, agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_1"})
	handleEvent(tr, dedup, agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_1"})

	if sink.count() != 1 {
		t.Fatalf("expected a repeat of the same input event to notify once, got %d", sink.count())
	}
}

// recordingClaimer records the tokens it is asked for, so a test can assert the
// claim set stays bounded.
type recordingClaimer struct {
	mu   sync.Mutex
	keys []string
}

func (c *recordingClaimer) Acquire(_, key string) (func(), bool, error) {
	c.mu.Lock()
	c.keys = append(c.keys, key)
	c.mu.Unlock()
	return func() {}, true, nil
}

func (c *recordingClaimer) uniqueKeys() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	unique := map[string]bool{}
	for _, k := range c.keys {
		unique[k] = true
	}
	return unique
}

// TestDedupDistinctInputsShareOneClaimKey pins the storage bound: `AcquireClaim`
// creates one file per token and never deletes it, so a token per request would
// leak a file for every permission or form. Distinct requests in a turn must
// therefore share the session's single input claim token.
func TestDedupDistinctInputsShareOneClaimKey(t *testing.T) {
	sink := &sharedSink{}
	claimer := &recordingClaimer{}
	c := NewDedup("dedupbounded-a", claimer, sink)

	for _, id := range []string{"evt_1", "evt_2", "evt_3"} {
		c.Notify(Notification{SessionID: "ses_A", Trigger: TriggerInput, EventID: id})
	}
	if sink.count() != 3 {
		t.Fatalf("expected 3 distinct input notifications, got %d", sink.count())
	}
	if keys := claimer.uniqueKeys(); len(keys) != 1 {
		t.Fatalf("expected one input claim token for three requests, got %v", keys)
	}
}

// TestDedupSameInputEventDeliveredOnce pins dedup's own idempotence: the same
// input event delivered twice (the tracker normally suppresses it first) is
// forwarded once.
func TestDedupSameInputEventDeliveredOnce(t *testing.T) {
	sink := &sharedSink{}
	c := NewDedup("dedupinputrepeat-a", &recordingClaimer{}, sink)

	n := Notification{SessionID: "ses_A", Trigger: TriggerInput, EventID: "evt_1"}
	c.Notify(n)
	c.Notify(n)
	if sink.count() != 1 {
		t.Fatalf("expected the same input event delivered once, got %d", sink.count())
	}
}

// TestDedupCrossClientInputsSingleDeliverer guards cross-client behavior with
// the shared input token: one client owns the session's input claim and
// delivers each distinct request, the other drops them.
func TestDedupCrossClientInputsSingleDeliverer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "dedupcross-input-a"
	sink := &sharedSink{}
	c1 := NewDedup(slug, StateClaimer{}, sink)
	c2 := NewDedup(slug, StateClaimer{}, sink)

	n1 := Notification{SessionID: "ses_A", Trigger: TriggerInput, EventID: "evt_1"}
	n2 := Notification{SessionID: "ses_A", Trigger: TriggerInput, EventID: "evt_2"}
	c1.Notify(n1)
	c2.Notify(n1)
	c1.Notify(n2)
	c2.Notify(n2)
	if sink.count() != 2 {
		t.Fatalf("expected each distinct request delivered once across clients, got %d", sink.count())
	}
}

// errorClaimer simulates a claim backend that is unavailable.
type errorClaimer struct{}

func (errorClaimer) Acquire(string, string) (func(), bool, error) {
	return nil, false, errors.New("claim backend unavailable")
}

func TestDedupFailsOpenOnClaimError(t *testing.T) {
	sink := &sharedSink{}
	c := NewDedup("deduperr-a", errorClaimer{}, sink)

	c.Notify(Notification{SessionID: "ses_A", Trigger: TriggerDone})
	if sink.count() != 1 {
		t.Fatalf("expected fail-open delivery on claim error, got %d", sink.count())
	}
}
