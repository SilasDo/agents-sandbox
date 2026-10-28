package viperconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/yamlfmt"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/titanous/json5"
)

// ConfigLayers returns the launcher-config layers from lowest to highest
// precedence, matching NewResolver's merge order and its documented precedence
// (generic user dir, per-slug user dir when slug is non-empty, project dir,
// legacy env, env, flags). NewResolver merges the file layers from this slice,
// so the order shown to the user and the order actually merged cannot drift.
func ConfigLayers(slug string) []ConfigLayer {
	cfg := configpaths.Get()
	layers := []ConfigLayer{
		fileLayer(LayerNameGenericUserConfig, cfg.UserConfigDir()),
	}
	if slug != "" {
		layers = append(layers, fileLayer(LayerNamePerSlugUserConfig, cfg.UserSlugConfigDir(slug)))
	}
	layers = append(layers,
		fileLayer(LayerNameProjectConfig, cfg.ProjectConfigDir()),
		ConfigLayer{Name: LayerNameLegacyEnv, Path: "", Present: false},
		ConfigLayer{Name: LayerNameEnv, Path: "", Present: false},
		ConfigLayer{Name: LayerNameFlags, Path: "", Present: false},
	)
	return layers
}

// fileLayer reports the directory of a config-file layer and whether a config
// file exists there.
func fileLayer(name, dir string) ConfigLayer {
	_, _, present := configpaths.FindConfigFile(dir)
	return ConfigLayer{Name: name, Path: dir, Present: present}
}

// NewResolver builds a Resolver, loading config files, configuring the
// AGENTS_SANDBOX_ env prefix (with the deprecated OPENCODE_SANDBOX_ aliases as
// a lower-precedence fallback), binding config-backed flags on cmd, and
// validating. Config precedence (lowest to highest): generic user dir,
// per-slug user dir (when slug is non-empty), project dir, legacy env, env,
// flags. The file layers and their order come from ConfigLayers so the merged
// order matches the order ConfigLayers reports. cmd may be nil to skip flag
// binding.
func NewResolver(cmd *cobra.Command, slug string) (*Resolver, error) {
	v := viper.New()

	for _, layer := range ConfigLayers(slug) {
		// mergeDir with an empty path is a no-op, so the env and flag
		// layers need no guard here.
		if err := mergeDir(v, layer.Path); err != nil {
			return nil, err
		}
	}

	v.SetEnvPrefix(EnvPrefix)
	// Env-var keys are all top-level and dash-separated; only the nested
	// "network.profile" key uses a dot, which maps to "_" here. This is a
	// repo-wide replacement, so a future dotted top-level key (e.g.
	// "auto.stop") would collide with a dashed one ("auto-stop") and must be
	// avoided.
	v.SetEnvKeyReplacer(envKeyReplacer)
	v.AutomaticEnv()
	for _, key := range configEnvKeys {
		// Keys are non-empty constants, so binding cannot fail.
		v.MustBindEnv(key, envVarName(LegacyEnvPrefix, key))
	}

	if cmd != nil {
		bindConfigFlags(v, cmd)
	}

	// Non-flag defaults: host config provisioning is on unless opted out.
	v.SetDefault(keyProvisionHostConfig, true)

	if err := validate(v); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(
		mapstructure.ComposeDecodeHookFunc(
			durationDecodeHook(),
			mapstructure.StringToTimeDurationHookFunc(),
			// Split comma-separated strings (env vars, CLI StringSlice flags)
			// into []string fields such as network.egress-allow and
			// network.dns-servers, and into []Profile for network.profile. It
			// also leniently accepts a scalar string for those list fields in
			// config files.
			stringToSliceDecodeHook(),
		),
	)); err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	decodedMounts, err := mounts.DecodeMounts(v.Get("mounts"))
	if err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	cfg.Mounts = decodedMounts
	cfg.Notify = decodeNotify(v)

	homeLayers, hasHome, err := homeconfig.LoadLayers([]string{
		configpaths.Get().UserConfigDir(),
		configpaths.Get().ProjectConfigDir(),
	})
	if err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	cfg.Home = homeLayers
	cfg.hasHome = hasHome
	docker, dockerLegacy, dockerExplicit := resolveDockerAlias(v, cmd, cfg.Docker)
	cfg.Docker = docker
	cfg.dockerExplicit = dockerExplicit
	return &Resolver{cfg: cfg, legacyEnv: legacyEnvVarsInUse(), dockerLegacy: dockerLegacy}, nil
}

// resolveDockerAlias folds the deprecated dind alias into the docker setting.
// The canonical docker value wins whenever it is explicitly set; otherwise a
// deprecated dind value (config key or AGENTS_SANDBOX_DIND) is used. It returns
// the effective value, the deprecated source name (if any), and whether a
// canonical or dind source is present (making the value explicit, so it
// overrides any project-Dockerfile content signal).
func resolveDockerAlias(v *viper.Viper, cmd *cobra.Command, value bool) (bool, string, bool) {
	if dockerExplicit(v, cmd) {
		return value, "", true
	}
	if !v.IsSet(KeyDind) {
		// value is always false here: any non-empty docker source makes
		// dockerExplicit true above.
		return value, "", false // gomutants:disable RETURN_FALSE reason="value is always false when docker is not explicit"
	}
	return v.GetBool(KeyDind), dockerLegacySource(v), true
}

// dockerExplicit reports whether the canonical docker setting is explicitly set
// via env, config file, or the --docker flag, so it takes precedence over the
// deprecated dind alias. The deprecated OPENCODE_SANDBOX_ prefix counts too:
// NewResolver still reads it, so it must also be surfaced as explicit rather
// than ignored by the project-Dockerfile decision.
func dockerExplicit(v *viper.Viper, cmd *cobra.Command) bool {
	if os.Getenv(envVarName(EnvPrefix, KeyDocker)) != "" {
		return true
	}
	if os.Getenv(envVarName(LegacyEnvPrefix, KeyDocker)) != "" {
		return true
	}
	if v.InConfig(KeyDocker) {
		return true
	}
	if cmd != nil {
		if flag := findFlag(cmd, KeyDocker); flag != nil && flag.Changed {
			return true
		}
	}
	return false
}

// dockerLegacySource names where the deprecated dind value came from.
func dockerLegacySource(v *viper.Viper) string {
	if env := envVarName(EnvPrefix, KeyDind); os.Getenv(env) != "" {
		return env
	}
	if v.InConfig(KeyDind) {
		return "the dind config key"
	}
	return "dind"
}

// NewResolverWithConfig builds a Resolver from an explicit Config. It is
// used by callers (notably cmd tests) that need a resolver with known values
// without touching config files or env.
//
//deadcode:ignore reason="construction seam for cmd tests; see doc comment"
func NewResolverWithConfig(cfg Config) *Resolver {
	return &Resolver{cfg: cfg, legacyEnv: nil, dockerLegacy: ""}
}

func mergeDir(v *viper.Viper, dir string) error {
	path, ext, ok := configpaths.FindConfigFile(dir)
	if !ok {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read launcher config %s: %w", path, err)
	}
	ct := configType(ext)
	if ext == configpaths.ExtJSON5 || ext == configpaths.ExtJSONC {
		var m map[string]any
		if unmarshalErr := json5.Unmarshal(data, &m); unmarshalErr != nil {
			return fmt.Errorf("parse launcher config %s: %w", path, unmarshalErr)
		}
		data, err = json.Marshal(m)
		if err != nil {
			return fmt.Errorf("normalize launcher config %s: %w", path, err)
		}
		// configType already reports both JSON5 and JSONC as JSON.
	}
	v.SetConfigType(ct)
	if err := v.MergeConfig(bytes.NewReader(data)); err != nil {
		if ct == ctYAML {
			return yamlfmt.WrapErr(path, err)
		}
		return fmt.Errorf("load launcher config %s: %w", path, err)
	}
	return nil
}

func configType(ext string) string {
	switch strings.ToLower(ext) {
	case ".yaml", ".yml":
		return ctYAML
	case ".json", configpaths.ExtJSONC, configpaths.ExtJSON5:
		return ctJSON
	}
	return ""
}
