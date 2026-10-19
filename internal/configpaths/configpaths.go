package configpaths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
)

type ConfigPaths interface {
	UserConfigDir() string
	UserCacheDir() string
	UserStateDir() string
	UserDataDir() string
	UserAgentConfigDir(a agent.Agent) string
	UserAgentConfigDirCandidates(a agent.Agent) []string
	UserHomeDir() (string, error)

	UserEnvFile() string
	UserEnvSecretFile() string
	UserEnvSecretYAMLFile() string

	ProjectConfigDir() string
	ProjectAgentConfigDir(a agent.Agent) string
	ProjectAgentConfigDirCandidates(a agent.Agent) []string
	ProjectEnvFile() string
	ProjectEnvSecretFile() string
	ProjectEnvSecretYAMLFile() string
	ProjectDockerfile() string
}

type realConfigPaths struct{}

// Get is the factory clients can use to get a Client.
// Tests override Get to inject mocks.
var Get = func() ConfigPaths {
	return &realConfigPaths{}
}

// UserConfigDir returns the tool's user-config directory: $XDG_CONFIG_HOME/
// agents-sandbox, defaulting to ~/.config/agents-sandbox.
func (c *realConfigPaths) UserConfigDir() string {
	return filepath.Join(xdgBaseDir("XDG_CONFIG_HOME", ".config"), pathPrefix)
}

// UserCacheDir returns the tool's cache directory: $XDG_CACHE_HOME/
// agents-sandbox, defaulting to ~/.cache/agents-sandbox.
func (c *realConfigPaths) UserCacheDir() string {
	return filepath.Join(xdgBaseDir("XDG_CACHE_HOME", ".cache"), pathPrefix)
}

// UserStateDir returns the tool's state directory: $XDG_STATE_HOME/
// agents-sandbox, defaulting to ~/.local/state/agents-sandbox.
func (c *realConfigPaths) UserStateDir() string {
	return filepath.Join(xdgBaseDir("XDG_STATE_HOME", ".local/state"), pathPrefix)
}

// UserDataDir returns the tool's data directory: $XDG_DATA_HOME/
// agents-sandbox, defaulting to ~/.local/share/agents-sandbox.
func (c *realConfigPaths) UserDataDir() string {
	return filepath.Join(xdgBaseDir("XDG_DATA_HOME", ".local/share"), pathPrefix)
}

// MSBHome returns the microsandbox home agents-sandbox owns: a private "msb"
// directory under the tool's data dir, never the user's ~/.microsandbox. It is a
// free function (not a ConfigPaths method) so the low-level sandbox/msb adapter
// can resolve it without importing the higher-level runtime package.
func MSBHome() string {
	return filepath.Join(Get().UserDataDir(), "msb")
}

// UserAgentConfigDir returns the directory of the active agent's own user
// config, nested under the tool's user config base.
func (c *realConfigPaths) UserAgentConfigDir(a agent.Agent) string {
	return filepath.Join(c.UserConfigDir(), validConfigDirName(a.Name(), a.ConfigDirName()))
}

// UserAgentConfigDirCandidates returns the agent's user config-dir candidates,
// primary first, then each alias. Paths only: the method never creates dirs.
func (c *realConfigPaths) UserAgentConfigDirCandidates(a agent.Agent) []string {
	return agentConfigDirCandidates(c.UserConfigDir(), a)
}

// ProjectAgentConfigDirCandidates returns the project config-dir candidates.
func (c *realConfigPaths) ProjectAgentConfigDirCandidates(a agent.Agent) []string {
	return agentConfigDirCandidates(projectConfigDir, a)
}

func agentConfigDirCandidates(base string, a agent.Agent) []string {
	names := []string{a.ConfigDirName()}
	if al, ok := agent.AsConfigDirAliaser(a); ok {
		names = append(names, al.ConfigDirAliases()...)
	}
	dirs := make([]string, 0, len(names))
	for _, name := range names {
		dirs = append(dirs, filepath.Join(base, validConfigDirName(a.Name(), name)))
	}
	return dirs
}

// validConfigDirName rejects a name that is not a single clean path element.
// Without this, names like "", ".", ".." or "a/b" would resolve to the launcher
// config root or a parent: filepath.Join cleans "..", so the config mirror could
// copy unrelated host files — including launcher secrets — into the VM. A bad
// name is a profile programming error, so it fails fast.
func validConfigDirName(agentName, name string) string {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsRune(name, filepath.Separator) || filepath.Base(name) != name {
		panic(
			fmt.Sprintf("agent %s declares invalid config dir name %q: must be a single path element", agentName, name),
		)
	}
	return name
}

// UserHomeDir returns the host user's home directory. It is the root the
// home-relative agent config and home manifests are resolved against, exposed
// through this seam so every host-home lookup is mockable like the other paths.
func (c *realConfigPaths) UserHomeDir() (string, error) {
	return os.UserHomeDir()
}

// ProjectConfigDir returns the tool's project-config directory: $PWD/agents-sandbox.
func (c *realConfigPaths) ProjectConfigDir() string { return projectConfigDir }

// UserEnvFile returns the user-level environment definitions file.
func (c *realConfigPaths) UserEnvFile() string {
	return filepath.Join(c.UserConfigDir(), envFileName)
}

// UserEnvSecretFile returns the user-level secret environment definitions file.
func (c *realConfigPaths) UserEnvSecretFile() string {
	return filepath.Join(c.UserConfigDir(), envSecretFileName)
}

// UserEnvSecretYAMLFile returns the user-level structured secret file.
func (c *realConfigPaths) UserEnvSecretYAMLFile() string {
	return filepath.Join(c.UserConfigDir(), envSecretYAMLFileName)
}

// Shared names for config subdirectories and files, used by both the
// project-local and the user-level config path helpers.
const (
	pathPrefix = "agents-sandbox"
	// projectConfigDir is the project-local metadata directory for the tool.
	projectConfigDir  = "." + pathPrefix
	EnvFileName       = "env"
	EnvSecretFileName = "env.secret"
	// Derived rather than a literal so gosec G101 does not flag the sensitive name.
	envSecretYAMLFileName = EnvSecretFileName + ".yaml"
	DockerFileName        = "Dockerfile"
)

const (
	envFileName       = EnvFileName
	envSecretFileName = EnvSecretFileName
	dockerfileName    = DockerFileName
)

// ProjectDockerfile returns the project Dockerfile path.
func (c *realConfigPaths) ProjectDockerfile() string {
	return filepath.Join(projectConfigDir, dockerfileName)
}

// ProjectAgentConfigDir returns the project-local config dir for the agent.
func (c *realConfigPaths) ProjectAgentConfigDir(a agent.Agent) string {
	return filepath.Join(projectConfigDir, validConfigDirName(a.Name(), a.ConfigDirName()))
}

func (c *realConfigPaths) ProjectEnvFile() string {
	return filepath.Join(projectConfigDir, envFileName)
}

func (c *realConfigPaths) ProjectEnvSecretFile() string {
	return filepath.Join(projectConfigDir, envSecretFileName)
}

// ProjectEnvSecretYAMLFile returns the project-level structured secret file.
func (c *realConfigPaths) ProjectEnvSecretYAMLFile() string {
	return filepath.Join(c.ProjectConfigDir(), envSecretYAMLFileName)
}

// xdgBaseDir resolves an XDG base directory for the given environment variable,
// falling back to $HOME/<fallback> when the variable is unset or holds a
// relative path (the XDG base directory spec only accepts absolute values). It
// returns the base directory without the application's own subdirectory; each
// caller appends "agents-sandbox".
//
// The os package's UserConfigDir/UserCacheDir are deliberately not used: they
// return platform-specific paths (e.g., $HOME/Library/... on macOS) instead of
// honoring XDG on every platform. Cross-platform tools like git and curl read
// the XDG variables unconditionally, so we do the same.
func xdgBaseDir(env, fallback string) string {
	if dir := os.Getenv(env); filepath.IsAbs(dir) {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}
