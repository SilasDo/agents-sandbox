# Notify for opencode2 + subagent-session filtering — Implementation Plan

> **Status:** Implemented on `notify-opencode2-subagent-filter` (commits `dae8ab1`, `ea43e3e`, `edacd52`, `3479830`); awaiting PR review. A review round rebased it onto `main` and applied three Greptile fixes (v2 in-turn input re-arm via `session.step.started`, v1 seed command `-m 30`, `StickyErrorEvents` comment); a second round replaced the seed command's `jq` dependency with an agent-owned parser; a third folded that command and parser into one `SessionListSpec`/`NewSessionList` value (no nil-parser silent skip) and pinned all three seed outcomes; a fourth, after a live probe of the running daemon (`guides/opencode2-live-event-probe.md`), identified input events by envelope id so two approvals asked in one step both notify. See the spec's "Review fixes (PR #245)".
>
> **Note:** The steps below are the original TDD checklist, not a verbatim record — they were not executed as written. Two controller rulings moved the v1 decoder test into `internal/agent/decoders_internal_test.go` and pinned the v2 spec-contents test to literal strings in the external test package.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the notify watcher work for the `opencode2` agent and fire only for top-level sessions, moving the harness-specific event decoding into the agent profiles so `internal/notify` stays generic.

**Architecture:** The agent owns the stream contract. `agent.EventStreamSpec` gains a `Parse` decoder, the event-type lists, a lifecycle-event list, and a seed command; `internal/notify` keeps only SSE framing, the reconnect policy, the generic busy/idle/awaiting/error tracker, dedup, and the backend. `opencode` (v1) and `opencode2` (v2) each supply their own decoder. A session is a subagent child iff its `parentID` is non-empty; children are suppressed.

**Tech Stack:** Go 1.26, stdlib `encoding/json`/`bufio`, Cobra/Viper CLI, `golangci-lint` via `make`, `jq` inside the runner image.

**Spec:** `.operator-shared/specs/notify-opencode2-subagent-filter.md`

## Global Constraints

- Module `github.com/inoio/agents-sandbox`, Go 1.26. No new Go dependencies (parsing is stdlib; `jq` already ships in the runner image).
- Format and lint only via `make fmt` / `make lint` (`golangci-lint`). Never `go vet`, never hand-format or `go fmt`.
- `make check` (fmt + lint + test) must pass before every commit; `make compile-all` must stay green (it compiles the `integration`/`msb`-tagged files).
- Test style: table-driven `t.Run` subtests, colocated `*_test.go`.
- Docs: root docs link into `docs/` with rendered HTML URLs; pages under `docs/` link to each other with Jekyll `{% link %}` tags; `ci/check-docs.sh` enforces this. Add one `[Unreleased]` line to `CHANGELOG.md`.
- Notification behavior is unchanged: same channels (`desktop`, `audio: system|bell|off`), same triggers (`on-input`/`on-done`/`on-error`), same generic wording, same cross-client dedup.

## Review Focus

Failure modes the spec implies but no naive test covers; each gets a test in the owning task:

1. A subagent created **before** the watcher connects never emits its `session.created` to us — seeding must still suppress it (Task 2).
2. A v1 **top-level** `session.updated` is now both a lifecycle event and a busy event; it must still mark busy, not be swallowed (Task 2).
3. A v2 `form.created` nests the session id at `data.form.sessionID`, not `data.sessionID`; input must fire for the right session (Task 3).
4. A failed/empty/non-JSON seed command must not abort the stream and must not mark any session a child (Task 2).
5. An event with a type but no session id (e.g. `server.connected`) must not panic and must not notify (Task 1).

---

### Task 1: Agent-owned decoder; notify goes generic (v1)

Introduce the decoder seam and switch `internal/notify` onto it, keeping v1 behavior identical (no child filter yet).

**Files:**
- Modify: `internal/agent/capabilities.go`
- Modify: `internal/agent/opencode.go`
- Modify: `internal/notify/event.go`
- Modify: `internal/notify/watch.go`
- Create: `internal/agent/decoders_internal_test.go`
- Test: `internal/agent/opencode_test.go`
- Test: `internal/notify/watch_extra_test.go`, `internal/notify/event_test.go`, `internal/notify/event_extra_test.go`, `internal/notify/watch_test.go`

**Interfaces:**
- Produces:
  - `agent.StreamEvent{ Type, SessionID, ParentID string }`
  - `agent.EventStreamSpec.Parse func(payload []byte) (StreamEvent, bool)`
  - `func parseOpencodeV1Event(payload []byte) (StreamEvent, bool)` (unexported, `internal/agent`)
  - `func parseSSEBlock(parse func([]byte) (agent.StreamEvent, bool), lines []string) (agent.StreamEvent, bool)`
  - `func watchOnce(ctx context.Context, sb msb.Sandbox, tracker *Tracker, sink Backend, spec agent.EventStreamSpec) error`
  - `func handleEvent(tracker *Tracker, sink Backend, e agent.StreamEvent)`
- Consumes: existing `EventStreamSpec`, `Watch(ctx, sb, spec, sink)` unchanged.

- [ ] **Step 1: Write the failing v1 decoder test**

Create `internal/agent/decoders_internal_test.go` with `package agent` — the decoders are unexported, and `opencode_test.go` is the external `agent_test` package. Fixtures are copied from the current `internal/notify/watch_extra_test.go` (`TestParseSSEBlock`), plus child/top-level `session.created` shapes:

```go
package agent

import "testing"

func TestParseOpencodeV1Event(t *testing.T) {
	sessionDiff := `{"directory":"/workspace","project":"cb78d2f0","payload":{"id":"evt_a","type":"session.diff","properties":{"sessionID":"ses_f987482bcffeiws2F2jQHsI18v","diff":[]}}}`
	sessionIdle := `{"payload":{"id":"evt_b","type":"session.idle","properties":{"sessionID":"ses_f987482bcffeiws2F2jQHsI18v"}}}`
	serverConnected := `{"payload":{"id":"evt_c","type":"server.connected","properties":{}}}`
	childCreated := `{"payload":{"type":"session.created","properties":{"sessionID":"ses_child","info":{"id":"ses_child","parentID":"ses_parent"}}}}`
	topLevelCreated := `{"payload":{"type":"session.created","properties":{"sessionID":"ses_root","info":{"id":"ses_root"}}}}`

	cases := []struct {
		name string
		in   string
		want StreamEvent
		ok   bool
	}{
		{"v1 diff", sessionDiff, StreamEvent{Type: "session.diff", SessionID: "ses_f987482bcffeiws2F2jQHsI18v"}, true},
		{"v1 idle", sessionIdle, StreamEvent{Type: "session.idle", SessionID: "ses_f987482bcffeiws2F2jQHsI18v"}, true},
		{"no session id", serverConnected, StreamEvent{Type: "server.connected"}, true},
		{"child parent", childCreated, StreamEvent{Type: "session.created", SessionID: "ses_child", ParentID: "ses_parent"}, true},
		{"top-level parent empty", topLevelCreated, StreamEvent{Type: "session.created", SessionID: "ses_root"}, true},
		{"undecodable", `not json`, StreamEvent{}, false},
		{"missing type", `{"payload":{"properties":{"sessionID":"ses_1"}}}`, StreamEvent{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseOpencodeV1Event([]byte(tc.in))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/agent/ -run TestParseOpencodeV1Event -v`
Expected: FAIL / build error — `StreamEvent` and the parser do not exist.

- [ ] **Step 3: Add `StreamEvent` and `Parse` to `internal/agent/capabilities.go`**

Add above `EventStreamSpec`:

```go
// StreamEvent is a decoded daemon event: its type, the session it belongs to,
// and that session's parent (non-empty for a subagent session).
type StreamEvent struct {
	Type      string
	SessionID string
	ParentID  string
}
```

Add to `EventStreamSpec`:

```go
	// Parse decodes one SSE data payload into a StreamEvent. ok is false when
	// the payload carries no event.
	Parse func(payload []byte) (StreamEvent, bool)
```

- [ ] **Step 4: Implement `parseOpencodeV1Event` and wire it**

In `internal/agent/opencode.go`, add the decoder (mirrors the removed `parseSSEBlock` shape, plus `info.parentID`):

```go
func parseOpencodeV1Event(payload []byte) (StreamEvent, bool) {
	var raw struct {
		Payload struct {
			Type       string `json:"type"`
			Properties struct {
				SessionID string `json:"sessionID"`
				Info      struct {
					ParentID string `json:"parentID"`
				} `json:"info"`
			} `json:"properties"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return StreamEvent{}, false
	}
	if raw.Payload.Type == "" {
		return StreamEvent{}, false
	}
	return StreamEvent{
		Type:      raw.Payload.Type,
		SessionID: raw.Payload.Properties.SessionID,
		ParentID:  raw.Payload.Properties.Info.ParentID,
	}, true
}
```

Set `Parse: parseOpencodeV1Event` in `opencodeProfile.EventStream()`'s return value (add the `encoding/json` import).

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/agent/ -run TestParseOpencodeV1Event -v`
Expected: PASS.

- [ ] **Step 6: Fix the spec equality test**

`TestOpencodeEventStream` in `internal/agent/opencode_test.go` uses `reflect.DeepEqual`, which is false for any non-nil func. Before comparing, assert and nil the field:

```go
	got := provider.EventStream()
	if got.Parse == nil {
		t.Fatal("EventStream().Parse is nil")
	}
	got.Parse = nil
	want := agent.EventStreamSpec{
		StreamCommand: "curl -N -s http://127.0.0.1:4096/global/event",
		BusyEvents:    []string{"message.part.updated", "session.updated"},
		AwaitingInput: []string{"permission.asked", "question.asked"},
		IdleEvents:    []string{"session.idle"},
		ErrorEvents:   []string{"session.error"},
		Name:          "opencode",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EventStream() = %+v, want %+v", got, want)
	}
```

- [ ] **Step 7: Add the notify framing test and delete the moved parser test**

Delete `TestParseSSEBlock` from `internal/notify/watch_extra_test.go` (its fixtures and cases now live in `internal/agent`). Add a framing test that proves `notify` extracts the payload and delegates:

```go
func TestParseSSEBlockDelegatesToSpecParse(t *testing.T) {
	var seen []byte
	parse := func(payload []byte) (agent.StreamEvent, bool) {
		seen = payload
		return agent.StreamEvent{Type: "x"}, true
	}
	got, ok := parseSSEBlock(parse, []string{"event: ping", "data:  {\"a\":1}  "})
	if !ok || got.Type != "x" {
		t.Fatalf("got %+v ok=%v, want delegated event", got, ok)
	}
	if string(seen) != `{"a":1}` {
		t.Errorf("parse payload = %q, want trimmed data line", seen)
	}
	if _, ok := parseSSEBlock(parse, []string{"event: ping", ": comment"}); ok {
		t.Error("a block with no data line must not be an event")
	}
}
```

- [ ] **Step 8: Switch `internal/notify` onto `agent.StreamEvent` and `spec.Parse`**

- `internal/notify/event.go`: delete `type Event struct{...}`; use `agent.StreamEvent` in `Tracker.Handle`, `HandleEventType` (`t.Handle(agent.StreamEvent{Type: typ})`), and any doc comment.
- `internal/notify/watch.go`: replace `parseSSEBlock(lines []string) (Event, bool)` with the framing-only version that calls `parse`, and read the payload as before (last `data:` line, `bytes.TrimSpace`, empty ⇒ not an event).
- `internal/notify/watch.go`: `watchOnce` drops its `streamCommand string` parameter for `spec agent.EventStreamSpec`; open the stream with `sb.ShellStream(ctx, spec.StreamCommand)`; in the blank-line branch call `parseSSEBlock(spec.Parse, block)`. `Watch` passes `spec` (it already has it) instead of `spec.StreamCommand`.
- `internal/notify/watch.go`: `handleEvent(tracker *Tracker, sink Backend, e agent.StreamEvent)`.
- Update notify tests: replace `Event{` with `agent.StreamEvent{` in `event_test.go`, `event_extra_test.go`, `watch_test.go`, `watch_extra_test.go`, and drop the `Data:` field from the deleted comparison loops.

`opencodeSpec()` in `event_test.go` must gain a working `Parse`, or every notify test nil-panics (the fixtures are v1-shaped `sseBlock`/`sseBlockFor` payloads). Add a test-local decoder to `event_test.go` and set `Parse: parseTestEvent` in `opencodeSpec()`:

```go
func parseTestEvent(payload []byte) (agent.StreamEvent, bool) {
	var raw struct {
		Payload struct {
			Type       string `json:"type"`
			Properties struct {
				SessionID string `json:"sessionID"`
			} `json:"properties"`
		} `json:"payload"`
	}
	if json.Unmarshal(payload, &raw) != nil || raw.Payload.Type == "" {
		return agent.StreamEvent{}, false
	}
	return agent.StreamEvent{Type: raw.Payload.Type, SessionID: raw.Payload.Properties.SessionID}, true
}
```

- [ ] **Step 9: Run the affected packages**

Run: `go test ./internal/agent/... ./internal/notify/...`
Expected: PASS. Confirm `internal/notify` production code no longer contains a JSON envelope reference: `rg -n 'payload\.type|json\.Unmarshal' internal/notify/ --glob '!*_test.go'` returns nothing.

- [ ] **Step 10: Whole-repo check and commit**

Run: `make check`
Expected: fmt, lint, and tests all pass.

```bash
git add internal/agent internal/notify
git commit -m "refactor(notify): decode stream events in the agent profile"
```

---

### Task 2: Subagent filter and seeding (v1)

Suppress notifications for any session with a non-empty `parentID`, learned from lifecycle events and seeded at each stream open.

**Files:**
- Modify: `internal/agent/capabilities.go`, `internal/agent/opencode.go`
- Modify: `internal/notify/event.go`, `internal/notify/watch.go`
- Test: `internal/agent/opencode_test.go`, `internal/notify/event_test.go`, `internal/notify/watch_test.go`

**Interfaces:**
- Consumes: `agent.StreamEvent`, `parseSSEBlock(parse, lines)`, `watchOnce(..., spec)` from Task 1.
- Produces:
  - `agent.EventStreamSpec.SessionEvents []string`
  - `agent.EventStreamSpec.SessionListCommand string`
  - `func (t *Tracker) SeedChildren(ids []string)`

- [ ] **Step 1: Write the failing tracker tests**

Add to `internal/notify/event_test.go`. Use a spec whose `SessionEvents` includes `session.created`/`session.updated`:

```go
func TestTrackerSuppressesChildSessions(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionEvents = []string{"session.created", "session.updated"}
	tr := NewTracker(spec)

	// Child announced by session.created: its busy->idle turn must not notify.
	tr.Handle(agent.StreamEvent{Type: "session.created", SessionID: "ses_child", ParentID: "ses_root"})
	if n := tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "ses_child"}); n != nil {
		t.Fatalf("child busy notified: %+v", n)
	}
	if n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "ses_child"}); n != nil {
		t.Fatalf("child idle notified: %+v", n)
	}

	// Top-level in the same tracker still notifies.
	tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "ses_root"})
	n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "ses_root"})
	if n == nil || n.Trigger != TriggerDone {
		t.Fatalf("top-level idle did not notify: %+v", n)
	}
}

func TestTrackerSeededChildIsSuppressed(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionEvents = []string{"session.created", "session.updated"}
	tr := NewTracker(spec)
	tr.SeedChildren([]string{"ses_seeded", ""})

	tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "ses_seeded"})
	if n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "ses_seeded"}); n != nil {
		t.Fatalf("seeded child notified: %+v", n)
	}
}

func TestTrackerTopLevelSessionUpdatedStillBusy(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionEvents = []string{"session.created", "session.updated"}
	tr := NewTracker(spec)

	// session.updated is both a lifecycle event and a busy event; top-level must mark busy.
	tr.Handle(agent.StreamEvent{Type: "session.updated", SessionID: "ses_root"})
	n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "ses_root"})
	if n == nil || n.Trigger != TriggerDone {
		t.Fatalf("top-level session.updated did not mark busy: %+v", n)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/notify/ -run 'TestTrackerSuppressesChildSessions|TestTrackerSeededChildIsSuppressed|TestTrackerTopLevelSessionUpdatedStillBusy' -v`
Expected: FAIL / build error — `SessionEvents`, `SeedChildren` undefined.

- [ ] **Step 3: Add the spec fields and implement the tracker filter**

`internal/agent/capabilities.go` — add to `EventStreamSpec`:

```go
	// SessionEvents are lifecycle event types whose payload may identify a
	// session's parent. A non-empty parent ID marks the session as a subagent
	// (child) session; no notification ever fires for a child session.
	SessionEvents []string
	// SessionListCommand is a one-shot shell command printing a JSON array of
	// child session IDs, run each time a stream opens to seed the filter with
	// sessions created before the watcher (re)connected. Empty disables seeding.
	SessionListCommand string
```

`internal/notify/event.go` — add the `children` set, initialise it in `NewTracker`, add `SeedChildren`, and gate `Handle`:

```go
func (t *Tracker) Handle(e agent.StreamEvent) *Notification {
	if e.SessionID != "" && eventTypeIn(e.Type, t.spec.SessionEvents) && e.ParentID != "" {
		t.children[e.SessionID] = true
	}
	if t.children[e.SessionID] {
		return nil
	}
	// ... unchanged busy/awaiting/idle/error switch
}
```

`SeedChildren` marks each non-empty id (skip `""` so a malformed seed cannot suppress every event).

- [ ] **Step 4: Run the tracker tests to verify they pass**

Run: `go test ./internal/notify/ -run 'TestTracker' -v`
Expected: PASS, including the pre-existing tracker tests.

- [ ] **Step 5: Add the seeding test**

Add to `internal/notify/watch_test.go`, reusing the package's `streamStub`, `sseBlockFor`, `recordBackend`, and `msb.NewTestResult`:

```go
func TestWatchOnceSeedsChildrenFromListCommand(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionListCommand = "seed-cmd"
	stream := &streamStub{events: []msb.StreamEvent{
		{Kind: msb.StreamEventStdout, Data: sseBlockFor("message.part.updated", "ses_child")},
		{Kind: msb.StreamEventStdout, Data: sseBlockFor("session.idle", "ses_child")},
		{Kind: msb.StreamEventExited, ExitCode: 0},
	}}
	sb := msb.NewMockSandbox(msb.SandboxOpts{
		ShellOut:     map[string]msb.ShellResult{"seed-cmd": msb.NewTestResult(true, 0, `["ses_child"]`, "", nil)},
		StreamHandle_: stream,
	})
	backend := &recordBackend{}
	// The scripted StreamEventExited ends the stream with an error; the
	// assertion is on notifications, not the error.
	_ = watchOnce(context.Background(), sb, NewTracker(spec), backend, spec)
	if backend.count() != 0 {
		t.Fatalf("seeded child notified: %+v", backend.got)
	}
}
```

- [ ] **Step 6: Implement seeding in `watchOnce`**

In `internal/notify/watch.go`, right after `sb.ShellStream` succeeds and before the scan loop:

```go
	if spec.SessionListCommand != "" {
		if res, err := sb.Shell(ctx, spec.SessionListCommand); err == nil && res.Success() {
			var ids []string
			if json.Unmarshal([]byte(res.Stdout()), &ids) == nil {
				tracker.SeedChildren(ids)
			}
		}
	}
```

Best-effort: any error, non-success, or non-array body is ignored and the stream proceeds. Add the `encoding/json` import.

- [ ] **Step 7: Add the seed-failure test**

In the same file, assert a failing seed neither aborts the stream nor suppresses a top-level session:

```go
func TestWatchOnceSeedFailureDoesNotBlockNotifications(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionListCommand = "seed-cmd"
	stream := &streamStub{events: []msb.StreamEvent{
		{Kind: msb.StreamEventStdout, Data: sseBlockFor("message.part.updated", "ses_top")},
		{Kind: msb.StreamEventStdout, Data: sseBlockFor("session.idle", "ses_top")},
		{Kind: msb.StreamEventExited, ExitCode: 0},
	}}
	sb := msb.NewMockSandbox(msb.SandboxOpts{ShellErr: errors.New("boom"), StreamHandle_: stream})
	backend := &recordBackend{}
	_ = watchOnce(context.Background(), sb, NewTracker(spec), backend, spec)
	if backend.count() != 1 || backend.notification(0).Trigger != TriggerDone {
		t.Fatalf("expected one done notification despite seed failure, got %+v", backend.got)
	}
}
```

- [ ] **Step 8: Wire the v1 spec fields and test them**

In `internal/agent/opencode.go`, add to `opencodeProfile.EventStream()`:

```go
		SessionEvents:      []string{"session.created", "session.updated"},
		SessionListCommand: "curl -sf http://127.0.0.1:4096/session | jq -c '[.[] | select(.parentID != null) | .id]'",
```

Extend `TestOpencodeEventStream`'s `want` with those two fields.

- [ ] **Step 9: Run the affected packages**

Run: `go test ./internal/agent/... ./internal/notify/...`
Expected: PASS.

- [ ] **Step 10: Whole-repo check and commit**

Run: `make check`
Expected: pass.

```bash
git add internal/agent internal/notify
git commit -m "feat(notify): skip notifications for subagent sessions"
```

---

### Task 3: opencode2 event stream (v2)

**Files:**
- Modify: `internal/agent/opencode2.go`
- Modify: `internal/agent/capabilities_test.go`
- Modify: `internal/agent/decoders_internal_test.go`
- Test: `internal/agent/opencode2_test.go`

**Interfaces:**
- Consumes: `agent.StreamEvent`, `EventStreamSpec` fields from Tasks 1–2.
- Produces: `func parseOpencodeV2Event(payload []byte) (StreamEvent, bool)`; `opencode2Profile.EventStream()`.

- [ ] **Step 1: Write the failing v2 decoder test**

Add to `internal/agent/decoders_internal_test.go` (`package agent`, created in Task 1):

```go
func TestParseOpencodeV2Event(t *testing.T) {
	childCreated := `{"id":"evt_1","created":1,"type":"session.created","data":{"sessionID":"ses_child","parentID":"ses_root","title":"probe"}}`
	topLevelCreated := `{"type":"session.created","data":{"sessionID":"ses_root"}}`
	execStarted := `{"type":"session.execution.started","data":{"sessionID":"ses_1"}}`
	execSucceeded := `{"type":"session.execution.succeeded","data":{"sessionID":"ses_1"}}`
	formCreated := `{"type":"form.created","data":{"form":{"id":"frm_1","sessionID":"ses_1","title":"q"}}}`
	permissionAsked := `{"type":"permission.asked","data":{"sessionID":"ses_1","id":"per_1"}}`
	connected := `{"type":"server.connected","data":{}}`
	infoShape := `{"type":"session.updated","data":{"sessionID":"ses_9","info":{"parentID":"ses_root"}}}`

	cases := []struct {
		name string
		in   string
		want StreamEvent
		ok   bool
	}{
		{"child", childCreated, StreamEvent{Type: "session.created", SessionID: "ses_child", ParentID: "ses_root"}, true},
		{"top-level", topLevelCreated, StreamEvent{Type: "session.created", SessionID: "ses_root"}, true},
		{"exec started", execStarted, StreamEvent{Type: "session.execution.started", SessionID: "ses_1"}, true},
		{"exec succeeded", execSucceeded, StreamEvent{Type: "session.execution.succeeded", SessionID: "ses_1"}, true},
		{"form nests session id", formCreated, StreamEvent{Type: "form.created", SessionID: "ses_1"}, true},
		{"permission", permissionAsked, StreamEvent{Type: "permission.asked", SessionID: "ses_1"}, true},
		{"no session id", connected, StreamEvent{Type: "server.connected"}, true},
		{"info parent fallback", infoShape, StreamEvent{Type: "session.updated", SessionID: "ses_9", ParentID: "ses_root"}, true},
		{"undecodable", `not json`, StreamEvent{}, false},
		{"missing type", `{"data":{"sessionID":"ses_1"}}`, StreamEvent{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseOpencodeV2Event([]byte(tc.in))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/agent/ -run TestParseOpencodeV2Event -v`
Expected: FAIL / build error.

- [ ] **Step 3: Implement `parseOpencodeV2Event` and `opencode2Profile.EventStream()`**

In `internal/agent/opencode2.go`:

```go
func parseOpencodeV2Event(payload []byte) (StreamEvent, bool) {
	var raw struct {
		Type string `json:"type"`
		Data struct {
			SessionID string `json:"sessionID"`
			ParentID  string `json:"parentID"`
			Form      struct {
				SessionID string `json:"sessionID"`
			} `json:"form"`
			Info struct {
				ParentID string `json:"parentID"`
			} `json:"info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return StreamEvent{}, false
	}
	if raw.Type == "" {
		return StreamEvent{}, false
	}
	sessionID := raw.Data.SessionID
	if sessionID == "" {
		sessionID = raw.Data.Form.SessionID
	}
	parentID := raw.Data.ParentID
	if parentID == "" {
		parentID = raw.Data.Info.ParentID
	}
	return StreamEvent{Type: raw.Type, SessionID: sessionID, ParentID: parentID}, true
}

func (opencode2Profile) EventStream() EventStreamSpec {
	return EventStreamSpec{
		StreamCommand:      "curl -N -s " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/event",
		BusyEvents:         []string{"session.execution.started"},
		AwaitingInput:      []string{"permission.asked", "form.created"},
		IdleEvents:         []string{"session.execution.succeeded"},
		ErrorEvents:        []string{"session.error", "session.execution.failed"},
		SessionEvents:      []string{"session.created"},
		SessionListCommand: "curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/session | jq -c '[.data[] | select(.parentID != null) | .id]'",
		Parse:              parseOpencodeV2Event,
		Name:               opencode2Name,
	}
}
```

- [ ] **Step 4: Run the decoder test to verify it passes**

Run: `go test ./internal/agent/ -run TestParseOpencodeV2Event -v`
Expected: PASS.

- [ ] **Step 5: Update the capability assertion**

Replace `TestOnlyOpencodeImplementsEventStreamProvider` in `internal/agent/capabilities_test.go`:

```go
func TestAgentEventStreamProviders(t *testing.T) {
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		_, ok := agent.AsEventStreamProvider(a)
		want := name == "opencode" || name == "opencode2"
		if ok != want {
			t.Errorf("%s: AsEventStreamProvider = %v, want %v", name, ok, want)
		}
	}
}
```

- [ ] **Step 6: Add the opencode2 spec-contents test**

In `internal/agent/opencode2_test.go`, assert `EventStream()` fields (nil `Parse` before `DeepEqual`, assert non-nil separately) with exactly:

```go
StreamCommand:      "curl -N -s -u opencode:opencode http://127.0.0.1:4096/api/event",
BusyEvents:         []string{"session.execution.started"},
AwaitingInput:      []string{"permission.asked", "form.created"},
IdleEvents:         []string{"session.execution.succeeded"},
ErrorEvents:        []string{"session.error", "session.execution.failed"},
SessionEvents:      []string{"session.created"},
SessionListCommand: "curl -fsS -m 30 -u opencode:opencode http://127.0.0.1:4096/api/session | jq -c '[.data[] | select(.parentID != null) | .id]'",
Name:               "opencode2",
```

If `opencode2CurlFlags`/`opencode2CurlAuth`/`opencode2ServerURL` are unexported package consts, build the expected strings from them rather than hard-coding, so the test tracks the constants.

- [ ] **Step 7: Run the package**

Run: `go test ./internal/agent/...`
Expected: PASS.

- [ ] **Step 8: Whole-repo check, compile-all, and commit**

Run: `make check && make compile-all`
Expected: both green.

```bash
git add internal/agent
git commit -m "feat(agent): notify event stream for opencode2"
```

---

### Task 4: Docs and changelog

**Files:**
- Modify: `docs/configuration/notifications.md`, `docs/commands.md`, `CHANGELOG.md`

- [ ] **Step 1: Update the notifications page**

In `docs/configuration/notifications.md`, replace the sentence stating only the opencode agent exposes a session event stream with: `opencode` and `opencode2` are supported (the other agents have no daemon event stream), and add a sentence that only top-level sessions notify — subagent sessions are skipped because they carry a `parentID`.

- [ ] **Step 2: Update the `--notify` row**

In `docs/commands.md`, the `--notify` row says "Only applies to the opencode agent (the only agent with a session event stream)."; change it to name `opencode` and `opencode2`.

- [ ] **Step 3: Add the changelog line**

In `CHANGELOG.md` under `[Unreleased]`, add one line: notifications now work for the `opencode2` agent, and are skipped for subagent sessions.

- [ ] **Step 4: Verify docs and commit**

Run: `make check`
Expected: pass, including `ci/check-docs.sh`.

```bash
git add docs CHANGELOG.md
git commit -m "docs: notify supports opencode2 and skips subagent sessions"
```
