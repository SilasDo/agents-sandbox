package agent

import (
	"context"
	"path/filepath"
	"strings"
)

// codexName is the canonical registry name of the Codex CLI agent.
const codexName = "codex"

// codexConfigFileName is the Codex CLI's native config file under ~/.codex.
const codexConfigFileName = "config.toml"

type codexProfile struct{}

func (codexProfile) Name() string          { return codexName }
func (codexProfile) ConfigDirName() string { return "codex" }

// ImageSpec installs the static musl build from the Codex GitHub release. The
// release asset names use the same architecture names as `uname -m`.
func (codexProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(codexName),
		AgentEnv:   nil,
		InstallCommand: `asset="codex-$(uname -m)-unknown-linux-musl" && ` +
			`curl -fsSL "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/${asset}.tar.gz" | tar -xz -C /tmp && ` +
			`install -m 0755 "/tmp/$asset" /opt/agents-sandbox/bin/codex && rm "/tmp/$asset"`,
		RequiresNode: false,
	}
}

func (codexProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestCodexVersion(ctx)
}
func (codexProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

// SnippetPattern matches TOML snippets, which merge into config.toml as TOML.
func (codexProfile) SnippetPattern() string { return "config*.toml" }
func (codexProfile) VMConfigPath(home string) string {
	return filepath.Join(home, ".codex", codexConfigFileName)
}
func (codexProfile) ConfigFileNames() []string { return []string{codexConfigFileName} }

// ProvisionRules copies only config.toml. auth.json is left out: a ChatGPT
// login holds rotating refresh tokens that a copy would invalidate on the host.
func (codexProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: ".codex", Patterns: []string{codexConfigFileName}},
	}
}

// AttachCommand disables the startup update check: the sandbox pins the baked
// version and offers upgrades itself.
func (codexProfile) AttachCommand(_ string, args []string) string {
	parts := []string{"codex", "-c", "check_for_update_on_startup=false"}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

func (codexProfile) VersionCmd() string { return "codex --version" }
func (codexProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
