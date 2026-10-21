package agent_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// opencode must expose a distinct WorktreeProvider capability, separate from
// DaemonProvider.
func TestOpencodeImplementsWorktreeProvider(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if _, ok := agent.AsWorktreeProvider(a); !ok {
		t.Fatal("opencode should implement WorktreeProvider")
	}
	if _, ok := agent.AsDaemonProvider(a); !ok {
		t.Fatal("opencode should implement DaemonProvider")
	}
}

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

// A session-list command without a parser cannot seed anything, and a nil
// parser used to make the notify watcher silently skip seeding. The pair is
// therefore built through NewSessionList, which rejects an incomplete one.
func TestNewSessionListRejectsIncompletePairs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		parse   func([]byte) []string
	}{
		{name: "empty command", command: "", parse: func([]byte) []string { return nil }},
		{name: "nil parser", command: "cmd", parse: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("NewSessionList did not panic on an incomplete pair")
				}
			}()
			agent.NewSessionList(tt.command, tt.parse)
		})
	}
}

func TestNewSessionListKeepsCommandAndParser(t *testing.T) {
	got := agent.NewSessionList("cmd", func(payload []byte) []string {
		return []string{"child:" + string(payload)}
	})
	if got.Command() != "cmd" {
		t.Errorf("Command() = %q, want %q", got.Command(), "cmd")
	}
	if children := got.Parse([]byte("raw")); len(children) != 1 || children[0] != "child:raw" {
		t.Errorf("Parse() = %v, want [child:raw]", children)
	}
}
