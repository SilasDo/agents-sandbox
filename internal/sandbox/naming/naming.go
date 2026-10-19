package naming

import "strings"

// Prefix is the canonical base name for all agents-sandbox naming
// conventions. Changing this value renames the tool across all namespaces,
// annotations, VM names, image references, sandbox names, and volume names.
const Prefix = "agents-sandbox"

// Sandbox and image name prefixes derived from Prefix.
const (
	VMPrefix    = Prefix + "-vm-"
	HomePrefix  = Prefix + "-home-"
	TaskPrefix  = Prefix + "-task-"
	ImagePrefix = Prefix + "/runner-"
)

// IsProjectSandbox reports whether name is an agents-sandbox project VM name.
func IsProjectSandbox(name string) bool {
	return strings.HasPrefix(name, VMPrefix)
}

// IsTaskSandbox reports whether name is an agents-sandbox task worker name.
func IsTaskSandbox(name string) bool {
	return strings.HasPrefix(name, TaskPrefix)
}

// IsLauncherSandbox reports whether name belongs to a sandbox agents-sandbox
// manages: a project VM or a task worker.
func IsLauncherSandbox(name string) bool {
	return IsProjectSandbox(name) || IsTaskSandbox(name)
}

// IsHomeVolume reports whether name is an agents-sandbox home volume name.
func IsHomeVolume(name string) bool {
	return strings.HasPrefix(name, HomePrefix)
}
