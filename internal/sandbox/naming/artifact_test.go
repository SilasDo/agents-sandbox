package naming

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// fixtureSlug is a realistic project slug: a folder name joined to a 14-char
// base36 hash, the only shape findHashSuffix recognises.
const fixtureSlug = "myproj-1mjusbm3wikhb0"

func TestProjectVMNameIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1mjusbm3wikhb0-opencode", ProjectVMName(fixtureSlug, "opencode"))
	require.Equal(t, "agents-sandbox-vm-myproj-1mjusbm3wikhb0-opencode",
		LegacyProjectVMName(fixtureSlug, "opencode"))
}

func TestTaskSandboxNameIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1mjusbm3wikhb0-opencode-1729000000000000000",
		TaskSandboxName(fixtureSlug, "opencode", 1729000000000000000))
	require.Equal(t, "agents-sandbox-task-myproj-1mjusbm3wikhb0-opencode-1729000000000000000",
		LegacyTaskSandboxName(fixtureSlug, "opencode", 1729000000000000000))
}

func TestHomeVolumeNameIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1mjusbm3wikhb0-opencode-20260812T123456",
		HomeVolumeName(fixtureSlug, "opencode", "20260812T123456"))
	require.Equal(t, "agents-sandbox-home-myproj-1mjusbm3wikhb0-opencode-20260812T123456",
		LegacyHomeVolumeName(fixtureSlug, "opencode", "20260812T123456"))
}

func TestRunnerImageRefIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1mjusbm3wikhb0:opencode-latest", RunnerImageRef(fixtureSlug, "opencode"))
	require.Equal(t, "agents-sandbox/runner-myproj-1mjusbm3wikhb0:opencode-latest",
		DockerImageRef(fixtureSlug, "opencode"))
}

func TestParseVMNameBothSchemes(t *testing.T) {
	cases := []struct {
		name string
		want ArtifactInfo
	}{
		{"myproj-1mjusbm3wikhb0-opencode", ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"}},
		{"agents-sandbox-vm-myproj-1mjusbm3wikhb0-opencode", ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"}},
		{
			"myproj-1mjusbm3wikhb0-opencode-1729000000000000000",
			ArtifactInfo{Slug: fixtureSlug, Agent: "opencode", Task: true},
		},
		{"myproj-1mjusbm3wikhb0-claude-code", ArtifactInfo{Slug: fixtureSlug, Agent: "claude-code"}},
		{
			"myproj-1mjusbm3wikhb0-claude-code-1729000000000000000",
			ArtifactInfo{Slug: fixtureSlug, Agent: "claude-code", Task: true},
		},
		{
			"agents-sandbox-task-myproj-1mjusbm3wikhb0-opencode-1729000000000000000",
			ArtifactInfo{Slug: fixtureSlug, Agent: "opencode", Task: true},
		},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, ParseVMName(tc.name), tc.name)
	}
}

// TestParseNamesWithHashShapedFolderSegment pins the anchor against a folder
// whose own last segment is 14 alphanumerics: the real slug hash is the
// rightmost segment-bounded run, so the folder segment must not be taken as the
// hash (which would fold the hash and agent into the slug).
func TestParseNamesWithHashShapedFolderSegment(t *testing.T) {
	slug := "go-1234567890abcd-1mjusbm3wikhb0"
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "opencode"},
		ParseVMName(ProjectVMName(slug, "opencode")))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "opencode"},
		ParseVMName(LegacyProjectVMName(slug, "opencode")))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "claude-code", Task: true},
		ParseVMName(TaskSandboxName(slug, "claude-code", 1729000000000000000)))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "opencode"},
		ParseHomeVolumeName(HomeVolumeName(slug, "opencode", "20260812T123456")))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "opencode"},
		ParseHomeVolumeName(LegacyHomeVolumeName(slug, "opencode", "20260812T123456")))
}

func TestParseHomeVolumeNameHyphenatedAgent(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "claude-code"},
		ParseHomeVolumeName("myproj-1mjusbm3wikhb0-claude-code-20260812T123456"))
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "claude-code"},
		ParseHomeVolumeName("agents-sandbox-home-myproj-1mjusbm3wikhb0-claude-code-20260812T123456"))
}

func TestParseImageTagBothSchemes(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"},
		ParseImageTag("myproj-1mjusbm3wikhb0:opencode-latest"))
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"},
		ParseImageTag("agents-sandbox/runner-myproj-1mjusbm3wikhb0:opencode-latest"))
	require.Equal(t, ArtifactInfo{}, ParseImageTag("docker.io/library/foo:1"))
}
