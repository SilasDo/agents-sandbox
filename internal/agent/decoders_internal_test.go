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
		{
			"child parent",
			childCreated,
			StreamEvent{Type: "session.created", SessionID: "ses_child", ParentID: "ses_parent"},
			true,
		},
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
