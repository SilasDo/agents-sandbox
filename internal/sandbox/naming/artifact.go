package naming

import (
	"strconv"
	"strings"
)

// ArtifactInfo is the result of an artifact name parse operation. Task is true
// for a transient task sandbox, whose name ends in an all-digit nanos segment.
type ArtifactInfo struct {
	Slug   string
	Digest string
	Agent  string
	Task   bool
}

// ProjectVMName returns the prefix-free project VM name for a slug and agent.
func ProjectVMName(slug, agent string) string {
	return slug + "-" + agent
}

// LegacyProjectVMName returns the pre-change project VM name.
func LegacyProjectVMName(slug, agent string) string {
	return LegacyVMPrefix + slug + "-" + agent
}

// TaskSandboxName returns the prefix-free task sandbox name for a slug, agent
// and creation timestamp in nanoseconds.
func TaskSandboxName(slug, agent string, nanos int64) string {
	return slug + "-" + agent + "-" + strconv.FormatInt(nanos, 10)
}

// LegacyTaskSandboxName returns the pre-change task sandbox name.
func LegacyTaskSandboxName(slug, agent string, nanos int64) string {
	return LegacyTaskPrefix + slug + "-" + agent + "-" + strconv.FormatInt(nanos, 10)
}

// HomeVolumeName returns the prefix-free home volume name for a slug, agent and
// timestamp.
func HomeVolumeName(slug, agent, timestamp string) string {
	return slug + "-" + agent + "-" + timestamp
}

// LegacyHomeVolumeName returns the pre-change home volume name.
func LegacyHomeVolumeName(slug, agent, timestamp string) string {
	return LegacyHomePrefix + slug + "-" + agent + "-" + timestamp
}

// RunnerImageRef returns the prefix-free microsandbox runner image reference
// for a project slug and agent, e.g. "myproject-1mjusbm3wikhb0:opencode-latest".
// It is the builder counterpart to ParseImageTag.
func RunnerImageRef(slug, agent string) string {
	return slug + ":" + agent + "-latest"
}

// LegacyRunnerImageRef returns the pre-change microsandbox runner image
// reference.
func LegacyRunnerImageRef(slug, agent string) string {
	return DockerImageRef(slug, agent)
}

// DockerImageRef returns the Docker tag for a project slug and agent. It keeps
// the namespace prefix so runner images stay distinguishable in the system
// Docker daemon.
func DockerImageRef(slug, agent string) string {
	return DockerImagePrefix + slug + ":" + agent + "-latest"
}

// findHashSuffix finds the start index of a 14-character base36 hash suffix
// in the name remainder (e.g. "agents-sandbox-1mjusbm3wikhb0" -> returns 6, pointing
// at the '1' in the 14-char hash). Returns -1 when no such suffix is found.
func findHashSuffix(name string) int {
	for i := 1; i < len(name)-13; i++ {
		if name[i-1] != '-' {
			continue
		}
		ok := true
		for j := range 14 {
			c := name[i+j]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// ParseImageTag extracts the slug, digest, and agent from a microsandbox runner
// image reference under either scheme. Examples:
//
//	"myproject-1mjusbm3wikhb0:opencode-latest"
//	→ slug="myproject-1mjusbm3wikhb0", agent="opencode"
//
//	"agents-sandbox/runner-myproject:xYz1234AbCdEfGh"
//	→ slug="myproject", digest="xYz1234AbCdEfGh"
//
// A prefix-free reference is recognised by its slug being a single path
// segment, so a registry/repository reference (a foreign image) parses empty.
func ParseImageTag(name string) ArtifactInfo {
	if strings.HasPrefix(name, DockerImagePrefix) {
		return parseImageRemainder(name[len(DockerImagePrefix):], true)
	}
	return parseImageRemainder(name, false)
}

func parseImageRemainder(remainder string, prefixed bool) ArtifactInfo {
	before, after, ok := strings.CutLast(remainder, ":")
	if !ok {
		if !prefixed && !looksLikeProjectSlug(remainder) {
			return ArtifactInfo{Slug: "", Digest: "", Agent: "", Task: false}
		}
		return ArtifactInfo{Slug: remainder, Digest: "", Agent: "", Task: false}
	}
	tag := after
	slug := before
	if !prefixed && !looksLikeProjectSlug(slug) {
		return ArtifactInfo{Slug: "", Digest: "", Agent: "", Task: false}
	}
	if agent, ok := strings.CutSuffix(tag, "-latest"); ok {
		return ArtifactInfo{Slug: slug, Agent: agent, Digest: "", Task: false}
	}
	if tag != "" && tag != "latest" {
		return ArtifactInfo{Slug: slug, Digest: tag, Agent: "", Task: false}
	}
	return ArtifactInfo{Slug: slug, Digest: "", Agent: "", Task: false}
}

// looksLikeProjectSlug reports whether s could be a project slug. A slug is a
// single path segment (no registry/repository slash), which is what separates a
// prefix-free runner ref from a foreign image reference.
func looksLikeProjectSlug(s string) bool {
	return s != "" && !strings.Contains(s, "/")
}

// isBase36Hash reports whether s is a 14-character lowercase-alphanumeric hash
// such as the ones embedded in project slugs.
func isBase36Hash(s string) bool {
	if len(s) != 14 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// allDigits reports whether s is a non-empty run of ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ParseVMName extracts the slug, optional agent, and task flag from a sandbox
// name under either scheme. Examples:
//
//	"myproject-1mjusbm3wikhb0-opencode"
//	→ slug="myproject-1mjusbm3wikhb0", agent="opencode"
//
//	"agents-sandbox-vm-projectname-1mjusbm3wikhb0"
//	→ slug="projectname-1mjusbm3wikhb0"
//
//	"myproject-1mjusbm3wikhb0-opencode-1729000000000000000"
//	→ slug="myproject-1mjusbm3wikhb0", agent="opencode", task=true
func ParseVMName(name string) ArtifactInfo {
	task := false
	remainder := name
	switch {
	case strings.HasPrefix(name, LegacyTaskPrefix):
		task = true
		remainder = name[len(LegacyTaskPrefix):]
	case strings.HasPrefix(name, LegacyVMPrefix):
		remainder = name[len(LegacyVMPrefix):]
	case strings.HasPrefix(name, LegacyHomePrefix), strings.HasPrefix(name, DockerImagePrefix):
		return ArtifactInfo{Slug: "", Digest: "", Agent: "", Task: false}
	}
	hashStart := findHashSuffix(remainder)
	if hashStart == -1 {
		return ArtifactInfo{Slug: remainder, Task: task, Digest: "", Agent: ""}
	}
	folderName := remainder[:hashStart-1]
	hash := remainder[hashStart : hashStart+14]
	slug := folderName + "-" + hash
	rest := remainder[hashStart+14:]
	if len(rest) < 2 || rest[0] != '-' {
		return ArtifactInfo{Slug: slug, Task: task, Digest: "", Agent: ""}
	}
	rest = rest[1:]
	parts := strings.Split(rest, "-")
	if len(parts) > 1 && allDigits(parts[len(parts)-1]) {
		return ArtifactInfo{Slug: slug, Agent: strings.Join(parts[:len(parts)-1], "-"), Task: true, Digest: ""}
	}
	return ArtifactInfo{Slug: slug, Agent: rest, Task: task, Digest: ""}
}

// ParseHomeVolumeName extracts the slug, optional digest, and agent from a home
// volume name under either scheme. Examples:
//
//	"myproject-1mjusbm3wikhb0-claude-code-20260812T123456"
//	→ slug="myproject-1mjusbm3wikhb0", agent="claude-code"
//
//	"agents-sandbox-home-myproject-1mjusbm3wikhb0-20260812T123456"
//	→ slug="myproject-1mjusbm3wikhb0"
func ParseHomeVolumeName(name string) ArtifactInfo {
	switch {
	case strings.HasPrefix(name, LegacyHomePrefix):
		return parseHomeVolumeRemainder(name[len(LegacyHomePrefix):], true)
	case strings.HasPrefix(name, LegacyVMPrefix), strings.HasPrefix(name, LegacyTaskPrefix),
		strings.HasPrefix(name, DockerImagePrefix):
		return ArtifactInfo{Slug: "", Digest: "", Agent: "", Task: false}
	default:
		return parseHomeVolumeRemainder(name, false)
	}
}

func parseHomeVolumeRemainder(remainder string, prefixed bool) ArtifactInfo {
	if remainder == "" {
		return ArtifactInfo{Slug: "", Digest: "", Agent: "", Task: false}
	}
	if hashStart := findHashSuffix(remainder); hashStart != -1 {
		folderName := remainder[:hashStart-1]
		hash := remainder[hashStart : hashStart+14]
		slug := folderName + "-" + hash
		tail := strings.TrimPrefix(remainder[hashStart+14:], "-")
		if tail == "" {
			return ArtifactInfo{Slug: slug, Digest: "", Agent: "", Task: false}
		}
		parts := strings.Split(tail, "-")
		last := parts[len(parts)-1]
		if isHomeTimestamp(last) {
			return ArtifactInfo{Slug: slug, Agent: strings.Join(parts[:len(parts)-1], "-"), Digest: "", Task: false}
		}
		return ArtifactInfo{Slug: slug, Digest: last, Agent: "", Task: false}
	}
	// A prefix-free home volume always embeds the project-slug hash; without it
	// the name is not ours.
	if !prefixed {
		return ArtifactInfo{Slug: "", Digest: "", Agent: "", Task: false}
	}
	// No embedded hash: the positional split still recognises the legacy digest
	// names.
	parts := strings.Split(remainder, "-")
	if len(parts) < 2 {
		return ArtifactInfo{Slug: remainder, Digest: "", Agent: "", Task: false}
	}
	last := parts[len(parts)-1]
	if isHomeTimestamp(last) {
		agent := ""
		slugParts := parts[:len(parts)-1]
		if len(parts) >= 3 && !isBase36Hash(parts[len(parts)-2]) {
			agent = parts[len(parts)-2]
			slugParts = parts[:len(parts)-2]
		}
		return ArtifactInfo{Slug: strings.Join(slugParts, "-"), Agent: agent, Digest: "", Task: false}
	}
	return ArtifactInfo{
		Slug:   strings.Join(parts[:len(parts)-1], "-"),
		Digest: last,
		Agent:  "",
		Task:   false,
	}
}

// isHomeTimestamp reports whether s looks like a UTC home-volume timestamp
// (YYYYMMDDTHHmmss, 15 chars with 'T' at index 8).
func isHomeTimestamp(s string) bool {
	if len(s) != 15 || s[8] != 'T' || s[0] < '2' || s[0] > '3' {
		return false
	}
	for i, c := range s {
		if i == 8 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ArtifactFor dispatches to the appropriate legacy parser based on the name
// prefix.
func ArtifactFor(name string) ArtifactInfo {
	switch {
	case strings.HasPrefix(name, DockerImagePrefix):
		return ParseImageTag(name)
	case strings.HasPrefix(name, LegacyTaskPrefix):
		remainder := name[len(LegacyTaskPrefix):]
		parts := strings.Split(remainder, "-")
		if len(parts) < 2 {
			return ArtifactInfo{Slug: remainder, Digest: "", Agent: "", Task: false}
		}
		return ArtifactInfo{Slug: strings.Join(parts[:len(parts)-1], "-"), Digest: "", Agent: "", Task: false}
	case strings.HasPrefix(name, LegacyVMPrefix):
		return ParseVMName(name)
	case strings.HasPrefix(name, LegacyHomePrefix):
		return ParseHomeVolumeName(name)
	}
	return ArtifactInfo{Slug: "", Digest: "", Agent: "", Task: false}
}
