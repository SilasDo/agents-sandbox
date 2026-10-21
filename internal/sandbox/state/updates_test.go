package state

import (
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestAgentCheckLivesUnderUpdates(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := writeRootState(RootState{Updates: Updates{Agents: map[string]AgentCheckState{
		"opencode": {LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRootState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Updates.Agents["opencode"].LatestVersion != "2.0.0" {
		t.Fatalf("Updates.Agents = %+v", got.Updates.Agents)
	}
}

func TestRootStateMarshalsUpdatesKey(t *testing.T) {
	rs := &RootState{}
	rs.SetLauncherUpdate(LauncherUpdateState{DismissedVersions: []string{"1.5.2"}})
	rs.SetAgentCheck("opencode", AgentCheckState{LatestVersion: "2.0.0"})
	data, err := marshalRootState(*rs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"updates:", "launcher:", "dismissed_versions:", "agents:", "last_checked:"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("marshalled root state missing %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "\nagents:") { // no top-level agents key
		t.Fatalf("root state still has a top-level agents: key:\n%s", data)
	}
}

func TestRootStateOmitsEmptyUpdates(t *testing.T) {
	data, err := marshalRootState(RootState{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "updates:") {
		t.Fatalf("empty root state should omit updates:\n%s", data)
	}
}

func TestLauncherUpdateAccessorsRoundTrip(t *testing.T) {
	rs := &RootState{}
	if !rs.LauncherUpdate().IsZero() {
		t.Fatal("zero RootState should have a zero LauncherUpdate")
	}
	rs.SetLauncherUpdate(LauncherUpdateState{DismissedVersions: []string{"2.0.0"}})
	if got := rs.LauncherUpdate().DismissedVersions; len(got) != 1 || got[0] != "2.0.0" {
		t.Fatalf("LauncherUpdate = %+v", got)
	}
}
