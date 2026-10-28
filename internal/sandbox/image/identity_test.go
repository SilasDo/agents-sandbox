package image

import (
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

func TestComputeImageIdentityNormalizesAbsentInputs(t *testing.T) {
	id := computeImageIdentity(nil, "", false)
	if id.ProjectDockerfileHash != identityNone {
		t.Errorf("ProjectDockerfileHash = %q, want %q", id.ProjectDockerfileHash, identityNone)
	}
	if id.AgentVersion != identityNone {
		t.Errorf("AgentVersion = %q, want %q", id.AgentVersion, identityNone)
	}
	if id.Docker != "false" {
		t.Errorf("Docker = %q, want false", id.Docker)
	}
}

func TestComputeImageIdentityRecordsInputs(t *testing.T) {
	id := computeImageIdentity([]byte("FROM ubuntu:24.04\n"), "1.2.3", true)
	if id.ProjectDockerfileHash == "" || id.ProjectDockerfileHash == identityNone {
		t.Errorf("ProjectDockerfileHash = %q, want a content hash", id.ProjectDockerfileHash)
	}
	if id.AgentVersion != "1.2.3" {
		t.Errorf("AgentVersion = %q, want 1.2.3", id.AgentVersion)
	}
	if id.Docker != "true" {
		t.Errorf("Docker = %q, want true", id.Docker)
	}
}

// identityLabels is the label map an image built from id carries. Production
// writes those labels through the identity build args and the Dockerfile's LABEL
// lines, so this is a test-side oracle only.
func identityLabels(id imageIdentity) map[string]string {
	return map[string]string{
		projectDockerfileHashLabelKey: id.ProjectDockerfileHash,
		agentVersionLabelKey:          id.AgentVersion,
		dockerLabelKey:                id.Docker,
	}
}

func TestEffectiveDindImpliedByManagedDindBase(t *testing.T) {
	project := []byte("FROM agents-sandbox/runner-base-dind:latest\n")
	if !effectiveDocker(project, dockerOff) {
		t.Error("expected docker to be implied by the managed dind base")
	}
	if effectiveDocker(nil, dockerOff) {
		t.Error("expected docker to be off without a flag or managed docker base")
	}
	if !effectiveDocker(nil, dockerOn) {
		t.Error("expected docker to be on when the flag is set")
	}
}

func TestEffectiveDockerContentSignals(t *testing.T) {
	finalMarker := []byte("FROM debian:trixie-slim\n# agents-sandbox:docker\nRUN echo hi\n")
	customBase := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	cases := []struct {
		name    string
		project []byte
		setting options.DockerSetting
		want    bool
	}{
		{"marker enables with no config", finalMarker, dockerOff, true},
		{
			"legacy marker enables with no config",
			[]byte("FROM debian:trixie-slim\n# agents-sandbox:dind\nRUN echo hi\n"),
			dockerOff,
			true,
		},
		{"managed docker base enables", []byte("FROM agents-sandbox/runner-base-docker\n"), dockerOff, true},
		{"managed dind base enables", []byte("FROM agents-sandbox/runner-base-dind\n"), dockerOff, true},
		{"no signal stays off", customBase, dockerOff, false},
		{"explicit off beats marker", finalMarker, options.DockerSetting{Enabled: false, Explicit: true}, false},
		{
			"explicit off beats managed base",
			[]byte("FROM agents-sandbox/runner-base-docker\n"),
			options.DockerSetting{Enabled: false, Explicit: true},
			false,
		},
		{"explicit on without content", customBase, options.DockerSetting{Enabled: true, Explicit: true}, true},
		{
			"earlier-stage marker does not enable",
			[]byte("FROM debian:trixie-slim AS x\n# agents-sandbox:docker\nFROM debian:trixie-slim\nRUN echo hi\n"),
			dockerOff,
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveDocker(tc.project, tc.setting); got != tc.want {
				t.Errorf("effectiveDocker = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildReasonsSkipsWhenDockerfileIDMatches(t *testing.T) {
	id := computeImageIdentity([]byte("FROM ubuntu:24.04\n"), "1.2.3", false)
	state := runnerImageState{Present: true, DockerfileID: "abc", Labels: identityLabels(id)}
	needsBuild, reasons := buildReasons(state, "abc", id)
	if needsBuild {
		t.Error("expected no build when the dockerfile-id matches")
	}
	if reasons != nil {
		t.Errorf("reasons = %v, want nil", reasons)
	}
}

func TestBuildReasonsMissingImage(t *testing.T) {
	needsBuild, reasons := buildReasons(runnerImageState{}, "abc", imageIdentity{})
	if !needsBuild {
		t.Fatal("expected a build when the runner image is absent")
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "not present") {
		t.Errorf("reasons = %v, want a not-present reason", reasons)
	}
}

func TestBuildReasonsNamesChangedComponents(t *testing.T) {
	existing := identityLabels(computeImageIdentity(nil, "", false))
	desired := computeImageIdentity([]byte("FROM ubuntu:24.04\n"), "2.0.0", true)
	state := runnerImageState{Present: true, DockerfileID: "old", Labels: existing}
	needsBuild, reasons := buildReasons(state, "new", desired)
	if !needsBuild {
		t.Fatal("expected a build when the identity differs")
	}
	joined := strings.Join(reasons, "|")
	for _, want := range []string{"project Dockerfile changed", "agent version changed", "docker-in-docker changed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("reasons %v missing %q", reasons, want)
		}
	}
}

func TestBuildReasonsFallsBackForLegacyImage(t *testing.T) {
	state := runnerImageState{
		Present:      true,
		DockerfileID: "old",
		Labels:       map[string]string{dockerfileIDLabelKey: "old"},
	}
	needsBuild, reasons := buildReasons(state, "new", imageIdentity{})
	if !needsBuild {
		t.Fatal("expected a build when the identity differs")
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "content changed") {
		t.Errorf("reasons = %v, want a generic content-change reason", reasons)
	}
}

func TestUserBuildArgsCarryIdentityComponents(t *testing.T) {
	id := imageIdentity{
		ProjectDockerfileHash: "hash",
		AgentVersion:          "1.2.3",
		Docker:                "false",
	}
	args := userBuildArgs(1000, 1000, agentOpencode(t).ImageSpec(), "1.2.3", "base", "id", false, id)
	for key, want := range map[string]string{
		projectDockerfileHashArg: "hash",
		agentVersionArg:          "1.2.3",
		dockerArg:                "false",
	} {
		if args[key] == nil || *args[key] != want {
			t.Errorf("build arg %s = %v, want %q", key, args[key], want)
		}
	}
}
