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

// TestFindHashSuffixBoundaries pins every bound of the right-to-left scan: the
// loop's lower limit, the '-'-preceded guard, the 14-char window, and both
// edges of the base36 predicate. Each case distinguishes the original from a
// loop/boundary or operand-removal mutation.
func TestFindHashSuffixBoundaries(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		// The hash is the whole name body at index 1 (loop lower bound 1).
		{"-abcdefghijklmn", 1},
		// Index 1 is a valid run, but not preceded by '-', so it must not anchor.
		{"0abcdefghijklmn", -1},
		// End-anchored run after a leading segment; the 14-char window is exact.
		{"a-abcdefghijklmn", 2},
		{"myproj-1mjusbm3wikhb0", 7},
		// A 14-char run containing '{' (outside a-z) is not a hash.
		{"x-abcdefghijklm{", -1},
		// A run ending in 'z' or '9' is still base36.
		{"x-abcdefghijklmz", 2},
		{"x-abcdefghijklm9", 2},
		// No run at all.
		{"short", -1},
		{"", -1},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, findHashSuffix(tc.name), tc.name)
	}
}

func TestIsBase36HashBoundaries(t *testing.T) {
	require.False(t, isBase36Hash(""))
	require.False(t, isBase36Hash("abc"))
	require.False(t, isBase36Hash("abcdefghijklmA"))
	require.False(t, isBase36Hash("abcdefghijklm{"))
	require.False(t, isBase36Hash("abcdefghijklm/"))
	require.True(t, isBase36Hash("abcdefghijklmz"))
	require.True(t, isBase36Hash("abcdefghijklm0"))
	require.True(t, isBase36Hash("1mjusbm3wikhb0"))
}

func TestIsHomeTimestampBoundaries(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"20260812T123456", true},
		{"20260101T120009", true},
		{"30000000T000000", true},
		// s[0] outside '2'..'3'.
		{"40000000T123456", false},
		{"10000000T123456", false},
		// Non-digit in the time/date body.
		{"20260101T12/000", false},
		{"20260101T12:000", false},
		{"20260101X120000", false},
		// Wrong length.
		{"20260101T12345", false},
		{"20260101T1234567", false},
		{"", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, isHomeTimestamp(tc.name), tc.name)
	}
}

// TestParseImageRemainder pins the slug/agent/digest split and the foreign-ref
// rejection on both the colon and no-colon paths, for the prefixed and
// prefix-free forms.
func TestParseImageRemainder(t *testing.T) {
	require.Equal(t, ArtifactInfo{}, parseImageRemainder("docker.io/library/foo", false))
	require.Equal(t, ArtifactInfo{}, parseImageRemainder("docker.io/library/foo:1", false))
	require.Equal(t, ArtifactInfo{Slug: "slug", Agent: "opencode"},
		parseImageRemainder("slug:opencode-latest", false))
	require.Equal(t, ArtifactInfo{Slug: "slug"}, parseImageRemainder("slug:", false))
	require.Equal(t, ArtifactInfo{Slug: "slug"}, parseImageRemainder("slug:latest", false))
	require.Equal(t, ArtifactInfo{Slug: "slug", Digest: "deadbeef"},
		parseImageRemainder("slug:deadbeef", false))
	require.Equal(t, ArtifactInfo{Slug: "anything"}, parseImageRemainder("anything", true))
	require.Equal(t, ArtifactInfo{Slug: "docker.io/foo"}, parseImageRemainder("docker.io/foo", true))
}

// TestParseHomeVolumeNameBoundaries pins the no-hash positional split: the
// legacy timestamp tail, the optional preceding agent segment (and its
// base36-hash exclusion), and the digest fallback.
func TestParseHomeVolumeNameBoundaries(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: "proj", Agent: "agent"},
		ParseHomeVolumeName("agents-sandbox-home-proj-agent-20260812T123456"))
	require.Equal(t, ArtifactInfo{Slug: "proj"},
		ParseHomeVolumeName("agents-sandbox-home-proj-20260812T123456"))
	require.Equal(t, ArtifactInfo{Slug: "proj", Digest: "deadbeef"},
		ParseHomeVolumeName("agents-sandbox-home-proj-deadbeef"))
	// A hash-anchored name with an empty tail.
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug}, ParseHomeVolumeName(fixtureSlug))
}
