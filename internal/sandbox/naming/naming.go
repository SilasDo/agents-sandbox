package naming

import "strings"

// Prefix is the canonical base name for all agents-sandbox naming
// conventions.
const Prefix = "agents-sandbox"

// DockerImagePrefix namespaces the runner images in the system Docker daemon,
// keeping them distinguishable from user images.
const DockerImagePrefix = Prefix + "/runner-"

// Legacy microsandbox/runtime prefixes. Artifacts created before the naming
// change still carry them, so lookups must accept both shapes; only the foreign
// ~/.microsandbox scan relies on them as an ownership marker.
const (
	LegacyVMPrefix   = Prefix + "-vm-"
	LegacyHomePrefix = Prefix + "-home-"
	LegacyTaskPrefix = Prefix + "-task-"
)

// Deprecated aliases for the legacy prefixes, kept while callers migrate to the
// scheme-aware constructors and parsers.
const (
	VMPrefix    = LegacyVMPrefix
	HomePrefix  = LegacyHomePrefix
	TaskPrefix  = LegacyTaskPrefix
	ImagePrefix = DockerImagePrefix
)

// IsLegacyProjectSandbox reports whether name is a prefixed project VM name.
func IsLegacyProjectSandbox(name string) bool {
	return strings.HasPrefix(name, LegacyVMPrefix)
}

// IsLegacyTaskSandbox reports whether name is a prefixed task worker name.
func IsLegacyTaskSandbox(name string) bool {
	return strings.HasPrefix(name, LegacyTaskPrefix)
}

// IsLegacyLauncherSandbox reports whether name is a prefixed sandbox
// agents-sandbox manages: a project VM or a task worker.
func IsLegacyLauncherSandbox(name string) bool {
	return IsLegacyProjectSandbox(name) || IsLegacyTaskSandbox(name)
}

// IsLegacyHomeVolume reports whether name is a prefixed home volume name.
func IsLegacyHomeVolume(name string) bool {
	return strings.HasPrefix(name, LegacyHomePrefix)
}

// IsProjectSandbox is a deprecated alias for IsLegacyProjectSandbox.
func IsProjectSandbox(name string) bool { return IsLegacyProjectSandbox(name) }

// IsTaskSandbox reports whether a sandbox with the given name and labels is a
// transient task sandbox. The task label is authoritative; the trailing
// all-digit nanos segment is the name-level fallback for a sandbox created
// before the label existed.
func IsTaskSandbox(name string, labels map[string]string) bool {
	if labels[LabelTask] != "" {
		return true
	}
	return ParseVMName(name).Task
}

// IsLauncherSandbox is a deprecated alias for IsLegacyLauncherSandbox.
func IsLauncherSandbox(name string) bool { return IsLegacyLauncherSandbox(name) }

// IsHomeVolume is a deprecated alias for IsLegacyHomeVolume.
func IsHomeVolume(name string) bool { return IsLegacyHomeVolume(name) }
