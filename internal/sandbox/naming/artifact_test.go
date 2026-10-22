package naming

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// fixtureSlug is a realistic project slug: a folder name joined to a 14-char
// base36 hash, the only shape findHashSuffix recognises.
const fixtureSlug = "myproj-1mjusbm3wikhb0"

func TestTaskSandboxNameIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1mjusbm3wikhb0-opencode-1729000000000000000",
		TaskSandboxName(fixtureSlug, "opencode", 1729000000000000000))
}

func TestHomeVolumeNameIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1mjusbm3wikhb0-opencode-20260812T123456",
		HomeVolumeName(fixtureSlug, "opencode", "20260812T123456"))
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
		ParseVMName(projectVMName(slug, "opencode")))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "opencode"},
		ParseVMName(legacyProjectVMName(slug, "opencode")))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "claude-code", Task: true},
		ParseVMName(TaskSandboxName(slug, "claude-code", 1729000000000000000)))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "opencode"},
		ParseHomeVolumeName(HomeVolumeName(slug, "opencode", "20260812T123456")))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "opencode"},
		ParseHomeVolumeName(legacyHomeVolumeName(slug, "opencode", "20260812T123456")))
}

// TestParseNamesWithLegacyPrefixFolder pins the prefix-collision fix: a
// prefix-free artifact whose folder begins with a legacy prefix (a folder
// literally named "agents-sandbox-vm"/"-task"/"-home") must parse prefix-free,
// not have the legacy prefix stripped.
func TestParseNamesWithLegacyPrefixFolder(t *testing.T) {
	taskFolder := "agents-sandbox-task-1mjusbm3wikhb0"
	require.Equal(t, ArtifactInfo{Slug: taskFolder, Agent: "opencode"},
		ParseVMName(projectVMName(taskFolder, "opencode")))

	vmFolder := "agents-sandbox-vm-1mjusbm3wikhb0"
	require.Equal(t, ArtifactInfo{Slug: vmFolder, Agent: "opencode"},
		ParseVMName(projectVMName(vmFolder, "opencode")))

	homeFolder := "agents-sandbox-home-1mjusbm3wikhb0"
	require.Equal(t, ArtifactInfo{Slug: homeFolder, Agent: "opencode"},
		ParseVMName(projectVMName(homeFolder, "opencode")))
	require.Equal(t, ArtifactInfo{Slug: homeFolder, Agent: "opencode"},
		ParseHomeVolumeName(HomeVolumeName(homeFolder, "opencode", "20260812T123456")))

	// A genuine legacy name still parses.
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"},
		ParseVMName(legacyProjectVMName(fixtureSlug, "opencode")))
}

func TestParseHomeVolumeNameHyphenatedAgent(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "claude-code"},
		ParseHomeVolumeName("myproj-1mjusbm3wikhb0-claude-code-20260812T123456"))
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "claude-code"},
		ParseHomeVolumeName("agents-sandbox-home-myproj-1mjusbm3wikhb0-claude-code-20260812T123456"))
}

// TestParseHomeVolumeNameLegacyDigest pins the legacy digest format when both
// the project hash and the digest are lowercase 14-char base36 runs, the only
// shape findHashSuffix cannot tell apart by segment shape: it anchors on the
// digest, and the parser must step back to the project hash so prune can still
// match the saved volume to its project.
func TestParseHomeVolumeNameLegacyDigest(t *testing.T) {
	const digest = "4e8oeqohbmalok"
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Digest: digest},
		ParseHomeVolumeName("agents-sandbox-home-myproj-1mjusbm3wikhb0-"+digest))

	// A legacy digest name still parses when the folder's own last segment is
	// hash-shaped.
	hashShapedFolder := "go-1234567890abcd-1mjusbm3wikhb0"
	require.Equal(t, ArtifactInfo{Slug: hashShapedFolder, Digest: digest},
		ParseHomeVolumeName("agents-sandbox-home-go-1234567890abcd-1mjusbm3wikhb0-"+digest))
}

func TestParseImageTagBothSchemes(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"},
		ParseImageTag("myproj-1mjusbm3wikhb0:opencode-latest"))
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"},
		ParseImageTag("agents-sandbox/runner-myproj-1mjusbm3wikhb0:opencode-latest"))
	require.Equal(t, ArtifactInfo{}, ParseImageTag("docker.io/library/foo:1"))
	// A foreign reference without a tag is not ours either.
	require.Equal(t, ArtifactInfo{}, ParseImageTag("docker.io/library/foo"))
}

// TestParseHomeVolumeNameEdgeCases pins the small branches of the home-volume
// parser: an empty input, a hash with no tail, a hash-anchored prefix-free
// volume whose folder begins with a legacy VM prefix, and a legacy prefixed
// name carrying no hash at all.
func TestParseHomeVolumeNameEdgeCases(t *testing.T) {
	require.Equal(t, ArtifactInfo{}, ParseHomeVolumeName(""))
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug}, ParseHomeVolumeName(fixtureSlug))
	require.Equal(t, ArtifactInfo{Slug: "agents-sandbox-vm-1mjusbm3wikhb0", Agent: "opencode"},
		ParseHomeVolumeName("agents-sandbox-vm-1mjusbm3wikhb0-opencode-20260812T123456"))
	require.Equal(t, ArtifactInfo{Slug: "single"}, ParseHomeVolumeName("agents-sandbox-home-single"))
}

func TestAllDigits(t *testing.T) {
	require.False(t, allDigits(""))
	require.False(t, allDigits("12a"))
	require.True(t, allDigits("12"))
}
