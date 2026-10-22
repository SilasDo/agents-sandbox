package naming

import "strings"

// Prefix is the canonical base name for all agents-sandbox naming
// conventions.
const Prefix = "agents-sandbox"

// DockerImagePrefix namespaces the runner images in the system Docker daemon,
// keeping them distinguishable from user images. The Docker tag is unchanged by
// the prefix-free runtime naming.
const DockerImagePrefix = Prefix + "/runner-"

// Legacy prefixes of the microsandbox artifacts created before the naming
// change. Lookups accept both shapes; the prefixes are an ownership marker only
// for the foreign ~/.microsandbox scan, which a user's own microsandbox shared.
const (
	LegacyVMPrefix   = Prefix + "-vm-"
	LegacyHomePrefix = Prefix + "-home-"
	LegacyTaskPrefix = Prefix + "-task-"
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
