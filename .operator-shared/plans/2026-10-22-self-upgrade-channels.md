# Self-upgrade Release Channels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the launcher self-upgrade from either the stable release line or an opted-in canary prerelease line, and make a locally built `dev` binary able to exercise the real check and install.

**Architecture:** A new `upgrade.Channel` value selects how the newest release is resolved. The `stable` channel keeps GitHub's `releases/latest`; the `canary` channel lists releases and picks the newest *eligible* one (a final release, or a prerelease whose first identifier is `canary`). The download stops using `/releases/latest/download` and becomes tag-addressed, so a prerelease can be installed at all. The channel is configuration, read through the existing `Resolver` into `upgrade.Options` / `upgrade.UpgradeOptions`. A separate, undocumented environment lever lets an unparseable current version (empty, `dev`, or garbage) count as older than any release, capped at the minimum check interval so it cannot hammer the GitHub API. The release CI gains prerelease classification so a canary tag is never served by `/releases/latest` and never bumps the Homebrew formula.

**Tech Stack:** Go 1.26; `github.com/Masterminds/semver/v3`; `spf13/viper` + `spf13/cobra`; `net/http/httptest` for tests; GitHub Actions YAML.

**Spec:** `.operator-shared/specs/self-upgrade-channels.md`

## Global Constraints

- Config key `upgrade.channel`; values `stable` (default) and `canary`; env var `AGENTS_SANDBOX_UPGRADE_CHANNEL`.
- Canary eligibility: a release is offered on the canary channel when it is a final release, or a prerelease whose **first** semver prerelease identifier is `canary` (e.g. `1.9.0-canary.3`). Anything else (`1.9.0-alpha.1`, `1.9.0-rc.1`) is offered to no channel.
- Asset URL: `https://github.com/inoio/agents-sandbox/releases/download/v<version>/agents-sandbox-<os>-<arch>`. Release tags are always `v<version>` (the publish job triggers on `v*`), so the tag is `"v" + version`.
- Homebrew-managed installs never self-upgrade, on any channel.
- There is no downgrade path. No code reinstalls an older binary.
- The dev lever's env var is exactly `AGENTS_SANDBOX_UPGRADE_ANY` (any non-empty value enables it). It caps the effective check interval at `upgrade.MinInterval` (`time.Hour`); it never removes the throttle. It must not appear in `--help`, the docs, or a config key.
- The channel is configuration, never persisted. `LauncherState` (`last_checked`, `dismissed_versions`) is unchanged.
- Formatting and linting go through `make fmt` and `make lint` only — never `go fmt` or `go vet`.
- Docs linking: root-level docs link into `docs/` with rendered HTML links; pages under `docs/` link to each other with Jekyll `{% link %}`.
- One `CHANGELOG.md` line per user-visible change under `[Unreleased]`.

## Review Focus

- **A stable-channel install must never be offered a prerelease**, even when the release list contains one newer than the stable release. Pinned by `TestCheckStableNeverOffersPrerelease`.
- **A prerelease with a different identifier reaches no channel** — it is not a canary, and it is not a missing release either. Pinned by `TestCanaryEligible` and `TestLatestForChannelCanaryPicksNewest`.
- **A non-semver tag or a draft in the release listing is skipped without failing resolution.** Pinned by `TestLatestForChannelCanarySkipsUnusable`.
- **A 404 on the tag-addressed asset fails loudly** rather than leaving a half-installed binary. Pinned by `TestDownloadAssetNon200`.
- **An unparsable `upgrade.channel` from a config file fails the run loudly**, rather than silently falling back to stable. Pinned by `TestResolverRejectsInvalidUpgradeChannel`.

---

### Task 1: Tag-addressed release download

**Files:**
- Modify: `internal/upgrade/upgrade.go` (`downloadBase`, `updateExecutable`, `downloadAssetToDir`)
- Test: `internal/upgrade/upgrade_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `downloadAssetToDir(ctx context.Context, dir string, version string) (string, error)`; `downloadBase` is now `https://github.com/inoio/agents-sandbox/releases/download` (no `/latest`). `Update func(ctx context.Context, latest string) error` and `updateExecutable` keep their signatures and treat the argument as the version to fetch.

- [ ] **Step 1: Write the failing test**

Add to `internal/upgrade/upgrade_test.go`:

```go
func TestDownloadAssetUsesTaggedURL(t *testing.T) {
	asset := []byte("binary")
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write(asset)
	}))
	t.Cleanup(srv.Close)
	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })

	if _, err := downloadAssetToDir(context.Background(), t.TempDir(), "1.9.0-canary.3"); err != nil {
		t.Fatalf("download: %v", err)
	}
	want := fmt.Sprintf("/v1.9.0-canary.3/agents-sandbox-%s-%s", runtime.GOOS, runtime.GOARCH)
	if gotPath != want {
		t.Fatalf("download path = %q, want %q", gotPath, want)
	}
}
```

Add `"fmt"` and `"runtime"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/upgrade/ -run TestDownloadAssetUsesTaggedURL -v`
Expected: FAIL to compile — `downloadAssetToDir` takes 2 arguments, not 3.

- [ ] **Step 3: Re-address the download**

In `internal/upgrade/upgrade.go`, change `downloadBase` to drop `/latest`:

```go
	downloadBase = fmt.Sprintf("https://github.com/%s/releases/download", githubRepo)
```

Change `downloadAssetToDir` to take the version and build the tag-addressed URL (`<downloadBase>/v<version>/<asset>`, one `fmt.Sprintf` with the existing `asset` variable), and change `updateExecutable` to forward its `latest` argument:

```go
func updateExecutable(ctx context.Context, latest string) error {
	exe, err := executablePath()
	if err != nil {
		return err
	}
	assetPath, err := downloadAssetToDir(ctx, filepath.Dir(exe), latest)
	...
```

Add a doc note on `downloadAssetToDir` that the URL is tag-addressed so a prerelease can be installed, and that the tag is `"v" + version` by this repo's tagging convention.

- [ ] **Step 4: Update the existing download tests**

In `internal/upgrade/upgrade_test.go`: `TestDownloadAndReplace` calls `downloadAssetToDir(context.Background(), dir, "2.0.0")`. `TestDownloadAssetNon200` and `TestDownloadAssetNetworkError` each gain a version argument (`"2.0.0"`). Their `downloadBase` overrides stay as the bare server URL; the handlers' existing catch-all branch already serves the new path shape.

- [ ] **Step 5: Run the package tests**

Run: `go test ./internal/upgrade/ -v`
Expected: PASS, including the pre-existing `TestCheck*` and `TestUpgrade*` tests.

- [ ] **Step 6: Commit**

```bash
git add internal/upgrade/upgrade.go internal/upgrade/upgrade_test.go
git commit -m "fix: address the release download by tag instead of /latest"
```

---

### Task 2: Canary channel end to end

**Files:**
- Create: `internal/upgrade/channel.go`
- Create: `internal/upgrade/channel_test.go`
- Modify: `internal/upgrade/upgrade.go` (`Options`, `Check`, `Upgrade`, `LatestVersion` var)
- Modify: `internal/viperconfig/viperconfig.go` (`UpgradeConfig`, `keyUpgradeChannel`, `configEnvKeys`, `validateUpgrade`, `Resolver.UpgradeChannel`)
- Modify: `internal/viperconfig/viperconfig_test.go`
- Modify: `cmd/agents-sandbox/commands_cli.go` (`checkForUpgrade`)
- Modify: `cmd/agents-sandbox/commands.go` (`buildUpgradeCmd`)
- Modify: `cmd/agents-sandbox/constants.go` (`flagChannel`)
- Modify: `cmd/agents-sandbox/cli_upgrade_test.go`
- Test: `internal/upgrade/channel_test.go`, `internal/upgrade/upgrade_test.go`

**Interfaces:**
- Consumes: `downloadAssetToDir(ctx, dir, version)` from Task 1 — the canary install only works because the download is tag-addressed.
- Produces:
  - `type Channel string`; `ChannelStable Channel = "stable"`; `ChannelCanary Channel = "canary"`.
  - `func ParseChannel(s string) (Channel, error)`.
  - `func latestForChannel(ctx context.Context, ch Channel) (string, error)` — `ChannelStable` and the zero value `""` both mean stable.
  - `func canaryEligible(tag string) bool`.
  - `var LatestVersion func(ctx context.Context, ch Channel) (string, error) = latestForChannel`.
  - `upgrade.Options.Channel Channel`.
  - `type UpgradeOptions struct { CurrentVersion string; Channel Channel }`.
  - `func Upgrade(ctx context.Context, ui termio.UI, opts UpgradeOptions) error`.
  - `func (r *Resolver) UpgradeChannel() upgrade.Channel`.
  - `flagChannel = "channel"` in `cmd/agents-sandbox`.

- [ ] **Step 1: Write the failing resolution tests**

Create `internal/upgrade/channel_test.go`:

```go
package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveReleases serves the release-list API from a JSON body.
func serveReleases(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	orig := releasesURL
	releasesURL = srv.URL + "/releases"
	t.Cleanup(func() { releasesURL = orig })
}

func TestParseChannel(t *testing.T) {
	tests := []struct {
		in      string
		want    Channel
		wantErr bool
	}{
		{in: "stable", want: ChannelStable},
		{in: "canary", want: ChannelCanary},
		{in: "CANARY", want: ChannelCanary},
		{in: " canary ", want: ChannelCanary},
		{in: "bogus", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range tests {
		got, err := ParseChannel(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseChannel(%q): expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseChannel(%q): unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseChannel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCanaryEligible(t *testing.T) {
	tests := map[string]bool{
		"v1.9.0":               true,
		"1.9.0":                true,
		"v1.9.0-canary.1":      true,
		"v1.9.0-canary.10":     true,
		"v2.0.0-alpha.1":       false,
		"v1.9.0-rc.1":          false,
		"v1.9.0-beta":          false,
		"nightly":              false,
		"v":                    false,
	}
	for tag, want := range tests {
		if got := canaryEligible(tag); got != want {
			t.Errorf("canaryEligible(%q) = %v, want %v", tag, got, want)
		}
	}
}

func TestLatestForChannelCanaryPicksNewest(t *testing.T) {
	serveReleases(t, `[
		{"tag_name":"v2.0.0-alpha.1","draft":false},
		{"tag_name":"v1.9.0-canary.2","draft":false},
		{"tag_name":"v1.9.0-canary.10","draft":false},
		{"tag_name":"v1.8.0","draft":false},
		{"tag_name":"v1.9.0-canary.11","draft":true}
	]`)
	got, err := latestForChannel(context.Background(), ChannelCanary)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.9.0-canary.10" {
		t.Fatalf("latestForChannel(canary) = %q, want 1.9.0-canary.10", got)
	}
}

func TestLatestForChannelCanarySkipsUnusable(t *testing.T) {
	serveReleases(t, `[
		{"tag_name":"not-a-version","draft":false},
		{"tag_name":"v1.9.0-canary.1","draft":false}
	]`)
	got, err := latestForChannel(context.Background(), ChannelCanary)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.9.0-canary.1" {
		t.Fatalf("got %q, want 1.9.0-canary.1", got)
	}
}

func TestLatestForChannelCanaryNoEligible(t *testing.T) {
	serveReleases(t, `[{"tag_name":"v2.0.0-alpha.1","draft":false}]`)
	if _, err := latestForChannel(context.Background(), ChannelCanary); err == nil {
		t.Fatal("expected an error when no release is canary-eligible")
	}
}

func TestLatestForChannelCanaryRollsForwardToRelease(t *testing.T) {
	// Once the real release ships it outranks its own canaries, so the canary
	// pool moves onto it with no special rule.
	serveReleases(t, `[
		{"tag_name":"v1.9.0-canary.3","draft":false},
		{"tag_name":"v1.9.0","draft":false}
	]`)
	got, err := latestForChannel(context.Background(), ChannelCanary)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.9.0" {
		t.Fatalf("got %q, want 1.9.0", got)
	}
}

func TestLatestForChannelStableUsesLatest(t *testing.T) {
	serveLatest(t, "1.8.0", nil)
	serveReleases(t, `[{"tag_name":"v1.9.0-canary.1","draft":false}]`)
	for _, ch := range []Channel{ChannelStable, ""} {
		got, err := latestForChannel(context.Background(), ch)
		if err != nil {
			t.Fatalf("channel %q: unexpected error: %v", ch, err)
		}
		if got != "1.8.0" {
			t.Fatalf("channel %q = %q, want 1.8.0 (the stable release)", ch, got)
		}
	}
}
```

`slices` is imported only if used; drop it from the import list if no assertion needs it.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/upgrade/ -run 'TestParseChannel|TestCanaryEligible|TestLatestForChannel' -v`
Expected: FAIL to compile — `Channel`, `releasesURL`, `canaryEligible`, `latestForChannel` are undefined.

- [ ] **Step 3: Implement resolution in `internal/upgrade/channel.go`**

```go
// Channel selects which releases the launcher is willing to offer itself.
type Channel string

const (
	// ChannelStable follows GitHub's "latest" release, which never serves a
	// prerelease.
	ChannelStable Channel = "stable"
	// ChannelCanary follows the newest release eligible for the canary pool.
	ChannelCanary Channel = "canary"
)

// canaryIdentifier is the semver prerelease identifier a canary tag carries.
const canaryIdentifier = "canary"
```

`ParseChannel(s string) (Channel, error)`: lowercase + trim, accept the two constants, otherwise `fmt.Errorf("invalid upgrade channel %q (want stable or canary)", s)` — mirroring `ParseMode`.

`releasesURL = fmt.Sprintf("https://api.github.com/repos/%s/releases", githubRepo)` as a package var beside `latestURL` so tests can point it at an `httptest` server.

`latestForChannel(ctx, ch)` returns `latestCanaryRelease(ctx)` when `ch == ChannelCanary`, otherwise `latestRelease(ctx)` — so the zero value `""` and `ChannelStable` both take the stable path.

`canaryEligible(tag string) bool`: parse `strings.TrimPrefix(tag, "v")` with `semver.NewVersion` (return `false` on error); `true` when `v.Prerelease() == ""`; otherwise `true` only when the text before the first `.` of the prerelease equals `canaryIdentifier` (`strings.Cut`).

`latestCanaryRelease(ctx) (string, error)`: `GET releasesURL` with `?per_page=100`, the `Accept: application/json` and `User-Agent: agents-sandbox` headers and the same 10s timeout as `latestRelease`; non-200 is an error; decode into

```go
// githubRelease is the slice of a GitHub release object that channel
// resolution needs.
type githubRelease struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
}
```

Skip `Draft` and non-`canaryEligible` entries; among the rest take the maximum by semver and return its tag with the leading `v` stripped. Return `errors.New("no canary release found")` when nothing is eligible. Add a comment that the newest *eligible* release is expected within the first page because releases are returned newest-creation-first and canary numbers increase monotonically.

- [ ] **Step 4: Run the resolution tests**

Run: `go test ./internal/upgrade/ -run 'TestParseChannel|TestCanaryEligible|TestLatestForChannel' -v`
Expected: PASS.

- [ ] **Step 5: Write the failing channel-wiring tests**

Add to `internal/upgrade/upgrade_test.go`:

```go
func TestCheckCanaryOffersPrerelease(t *testing.T) {
	serveReleases(t, `[{"tag_name":"v1.9.0-canary.1","draft":false}]`)
	opts := mockOptions(t, "1.8.0", ModeNotify)
	opts.Channel = ChannelCanary
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasUpdate || res.Latest != "1.9.0-canary.1" {
		t.Fatalf("expected a canary offer, got %+v", res)
	}
}

func TestCheckStableNeverOffersPrerelease(t *testing.T) {
	serveLatest(t, "1.8.0", nil)
	serveReleases(t, `[{"tag_name":"v1.9.0-canary.1","draft":false}]`)
	opts := mockOptions(t, "1.7.0", ModeNotify)
	opts.Channel = ChannelStable
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasUpdate || res.Latest != "1.8.0" {
		t.Fatalf("stable must resolve only the latest release, got %+v", res)
	}
}
```

- [ ] **Step 6: Run them to verify they fail**

Run: `go test ./internal/upgrade/ -run 'TestCheckCanary|TestCheckStableNever' -v`
Expected: FAIL — `Options` has no field `Channel`.

- [ ] **Step 7: Thread the channel through `internal/upgrade/upgrade.go`**

- Add `Channel Channel` to `Options` (with a comment that the zero value means stable).
- In `Check`, replace `latest, err := latestRelease(ctx)` with `latest, err := latestForChannel(ctx, opts.Channel)`.
- Add `UpgradeOptions` and change `Upgrade`:

```go
// UpgradeOptions configures Upgrade.
//
//exhaustruct:ignore
type UpgradeOptions struct {
	CurrentVersion string
	Channel        Channel
}

func Upgrade(ctx context.Context, ui termio.UI, opts UpgradeOptions) error
```

`Upgrade` uses `opts.CurrentVersion` in place of the old `current` parameter and calls `LatestVersion(ctx, opts.Channel)`.
- Change the injection point:

```go
// LatestVersion returns the newest release version for the channel (leading "v"
// stripped) by querying the GitHub releases API.
var LatestVersion = latestForChannel
```

- [ ] **Step 8: Update the direct `Upgrade` tests**

In `internal/upgrade/upgrade_test.go`, `TestUpgrade`'s subtests call `Upgrade(context.Background(), &termio.Mock{}, "dev")` etc. — each becomes `Upgrade(ctx, ui, UpgradeOptions{CurrentVersion: ...})`, and each `LatestVersion` stub becomes a two-argument func (`func(context.Context, Channel) (string, error)`) returning the same version. `TestUpgradeIsNewerError` likewise.

- [ ] **Step 9: Run the package tests**

Run: `go test ./internal/upgrade/ -v`
Expected: PASS.

- [ ] **Step 10: Write the failing viperconfig tests**

Add to `internal/viperconfig/viperconfig_test.go`:

```go
func TestResolverUpgradeChannel(t *testing.T) {
	if got := NewResolverWithConfig(Config{}).UpgradeChannel(); got != upgrade.ChannelStable {
		t.Fatalf("UpgradeChannel default = %q, want stable", got)
	}
	r := NewResolverWithConfig(Config{Upgrade: UpgradeConfig{Channel: "canary"}})
	if got := r.UpgradeChannel(); got != upgrade.ChannelCanary {
		t.Fatalf("UpgradeChannel = %q, want canary", got)
	}
	// Unparsable values are possible via NewResolverWithConfig, which skips
	// validation, and fall back to stable.
	if got := NewResolverWithConfig(Config{Upgrade: UpgradeConfig{Channel: "bogus"}}).UpgradeChannel(); got != upgrade.ChannelStable {
		t.Fatalf("UpgradeChannel(bogus) = %q, want stable", got)
	}
}

func TestResolverUpgradeChannelEnvVar(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_UPGRADE_CHANNEL", "canary")
	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := r.UpgradeChannel(); got != upgrade.ChannelCanary {
		t.Fatalf("UpgradeChannel = %q, want canary from AGENTS_SANDBOX_UPGRADE_CHANNEL", got)
	}
}

func TestResolverRejectsInvalidUpgradeChannel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_UPGRADE_CHANNEL", "bogus")
	if _, err := NewResolver(nil, ""); err == nil {
		t.Fatal("expected error for invalid upgrade channel")
	}
}
```

- [ ] **Step 11: Run them to verify they fail**

Run: `go test ./internal/viperconfig/ -run TestResolverUpgradeChannel -v && go test ./internal/viperconfig/ -run 'TestResolverRejectsInvalidUpgradeChannel' -v`
Expected: FAIL — `UpgradeConfig` has no field `Channel`, `Resolver` has no method `UpgradeChannel`.

- [ ] **Step 12: Implement the config surface**

In `internal/viperconfig/viperconfig.go`:

- `UpgradeConfig` gains `Channel string \`mapstructure:"channel"\``.
- Add `keyUpgradeChannel = "upgrade.channel"` beside `keyUpgradeMode`/`keyUpgradeInterval`.
- Add `keyUpgradeChannel` to `configEnvKeys` right after `keyUpgradeInterval` (this is what binds the legacy `OPENCODE_SANDBOX_UPGRADE_CHANNEL` alias and includes it in the deprecated-prefix scan).
- Extend `validateUpgrade` with a `v.IsSet(keyUpgradeChannel)` branch calling `upgrade.ParseChannel`.
- Add, mirroring `UpgradeMode`:

```go
// UpgradeChannel returns the configured release channel, defaulting to stable.
// An unparsable value (possible via NewResolverWithConfig, which skips
// validation) also falls back to stable.
func (r *Resolver) UpgradeChannel() upgrade.Channel
```

- [ ] **Step 13: Run the viperconfig tests**

Run: `go test ./internal/viperconfig/ -v`
Expected: PASS.

- [ ] **Step 14: Write the failing cmd tests**

In `cmd/agents-sandbox/cli_upgrade_test.go` add `flagChannel`-driven and resolver-driven tests. Stub `upgrade.LatestVersion` to record the channel:

```go
func TestUpgradeChannelSelection(t *testing.T) {
	origVersion, origLatest, origUpdate := version, upgrade.LatestVersion, upgrade.Update
	t.Cleanup(func() { version, upgrade.LatestVersion, upgrade.Update = origVersion, origLatest, origUpdate })
	version = "1.0.0"
	upgrade.Update = func(context.Context, string) error { t.Fatal("Update should not be called"); return nil }

	t.Run("flag selects canary", func(t *testing.T) {
		var got upgrade.Channel
		upgrade.LatestVersion = func(_ context.Context, ch upgrade.Channel) (string, error) {
			got = ch
			return "1.0.0", nil
		}
		updateCmd, _ := setupUpgradeTestFixtures(t)
		if err := updateCmd.Flags().Set(flagChannel, "canary"); err != nil {
			t.Fatal(err)
		}
		if err := updateCmd.RunE(updateCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != upgrade.ChannelCanary {
			t.Fatalf("resolved channel = %q, want canary", got)
		}
	})

	t.Run("configuration selects canary", func(t *testing.T) {
		var got upgrade.Channel
		upgrade.LatestVersion = func(_ context.Context, ch upgrade.Channel) (string, error) {
			got = ch
			return "1.0.0", nil
		}
		updateCmd, _ := setupUpgradeTestFixtures(t)
		r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{
			Upgrade: launcherconfig.UpgradeConfig{Channel: "canary"},
		})
		updateCmd.SetContext(context.WithValue(context.Background(), (*launcherConfigKey)(nil), r))
		if err := updateCmd.RunE(updateCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != upgrade.ChannelCanary {
			t.Fatalf("resolved channel = %q, want canary from config", got)
		}
	})

	t.Run("invalid flag is rejected", func(t *testing.T) {
		upgrade.LatestVersion = func(context.Context, upgrade.Channel) (string, error) {
			t.Fatal("LatestVersion must not be consulted for an invalid channel")
			return "", nil
		}
		updateCmd, _ := setupUpgradeTestFixtures(t)
		if err := updateCmd.Flags().Set(flagChannel, "bogus"); err != nil {
			t.Fatal(err)
		}
		if err := updateCmd.RunE(updateCmd, nil); err == nil {
			t.Fatal("expected an error for an invalid --channel value")
		}
	})
}
```

Also extend `TestCheckForUpgrade` with a subtest asserting `Check` receives the resolver's channel:

```go
	t.Run("passes the configured channel", func(t *testing.T) {
		var got upgrade.Channel
		upgradeCheck = func(_ context.Context, opts upgrade.Options) (upgrade.Result, error) {
			got = opts.Channel
			return upgrade.Result{}, nil
		}
		r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{
			Upgrade: launcherconfig.UpgradeConfig{Channel: "canary"},
		})
		if _, err := checkForUpgrade(context.Background(), r, &termio.Mock{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != upgrade.ChannelCanary {
			t.Fatalf("Check channel = %q, want canary", got)
		}
	})
```

The two existing `upgrade.LatestVersion` stubs in `TestUpgrade` gain the `Channel` parameter. The `TestCheckForUpgrade` subtests' `upgradeCheck` stubs keep their `func(context.Context, upgrade.Options)` signature — a new field on `Options` does not change it.

- [ ] **Step 15: Run them to verify they fail**

Run: `go test ./cmd/agents-sandbox/ -run 'TestUpgradeChannelSelection|TestCheckForUpgrade' -v`
Expected: FAIL to compile — `flagChannel` undefined, `upgrade.Options` has no `Channel`.

- [ ] **Step 16: Implement the cmd wiring**

`cmd/agents-sandbox/constants.go`: add `flagChannel = "channel"` beside `flagAgentVersion`.

`cmd/agents-sandbox/commands_cli.go` — `checkForUpgrade` passes the channel:

```go
		Channel:        r.UpgradeChannel(),
```

`cmd/agents-sandbox/commands.go` — `buildUpgradeCmd` resolves the channel from the context-stored resolver, lets `--channel` override it, and registers the flag:

```go
func buildUpgradeCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdUpgrade,
		Args:  cobra.NoArgs,
		Short: "Check for and install the latest release",
		RunE: func(cmd *cobra.Command, _ []string) error {
			channel := upgrade.ChannelStable
			if r := resolverFromContext(cmd.Context()); r != nil {
				channel = r.UpgradeChannel()
			}
			if cmd.Flags().Changed(flagChannel) {
				name, err := cmd.Flags().GetString(flagChannel)
				if err != nil {
					return err
				}
				parsed, err := upgrade.ParseChannel(name)
				if err != nil {
					return err
				}
				channel = parsed
			}
			return upgrade.Upgrade(cmd.Context(), ui, upgrade.UpgradeOptions{
				CurrentVersion: version,
				Channel:        channel,
			})
		},
	}
	cmd.Flags().String(flagChannel, "", "release channel to upgrade from (stable or canary)")
	return cmd
}
```

- [ ] **Step 17: Run the cmd tests**

Run: `go test ./cmd/agents-sandbox/ -run 'TestUpgrade|TestCheckForUpgrade' -v`
Expected: PASS.

- [ ] **Step 18: Run the whole suite and lint**

Run: `make fmt && make lint && go test ./...`
Expected: PASS. Run `make lint` again after any fix-up.

- [ ] **Step 19: Commit**

```bash
git add internal/upgrade internal/viperconfig cmd/agents-sandbox
git commit -m "feat: add an upgrade.channel setting for opted-in canary releases"
```

---

### Task 3: The dev-build upgrade lever

**Files:**
- Modify: `internal/upgrade/upgrade.go` (`Options`, `UpgradeOptions`, `Check`, `Upgrade`, new `newerThan`/`isParseable`)
- Modify: `cmd/agents-sandbox/commands_cli.go` (env read helper, `checkForUpgrade`)
- Modify: `cmd/agents-sandbox/commands.go` (`buildUpgradeCmd`)
- Test: `internal/upgrade/upgrade_test.go`, `cmd/agents-sandbox/cli_upgrade_test.go`

**Interfaces:**
- Consumes: `upgrade.Options` and `upgrade.UpgradeOptions` from Task 2.
- Produces: `Options.AllowAnyVersion bool`; `UpgradeOptions.AllowAnyVersion bool`; `func newerThan(current, latest string, allowAnyVersion bool) (bool, error)`; `func isParseable(s string) bool`; in `cmd/agents-sandbox`: `const upgradeAnyVersionEnv = "AGENTS_SANDBOX_UPGRADE_ANY"` and `func upgradeAnyVersionEnabled() bool`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/upgrade/upgrade_test.go`:

```go
func TestNewerThan(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		latest   string
		allowAny bool
		want     bool
		wantErr  bool
	}{
		{name: "normal newer", current: "1.0.0", latest: "1.1.0", want: true},
		{name: "normal not newer", current: "1.1.0", latest: "1.1.0"},
		{name: "dev ignored without the lever", current: "dev", latest: "1.1.0", wantErr: true},
		{name: "empty ignored without the lever", current: "", latest: "1.1.0", wantErr: true},
		{name: "dev offered with the lever", current: "dev", latest: "1.1.0", allowAny: true, want: true},
		{name: "empty offered with the lever", current: "", latest: "1.1.0", allowAny: true, want: true},
		{name: "garbage offered with the lever", current: "not-a-version", latest: "1.1.0", allowAny: true, want: true},
		{name: "unparsable latest still errors", current: "dev", latest: "nope", allowAny: true, wantErr: true},
		{name: "parseable current is unaffected by the lever", current: "1.1.0", latest: "1.1.0", allowAny: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := newerThan(tc.current, tc.latest, tc.allowAny)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("newerThan(%q,%q,%v) = %v, want %v", tc.current, tc.latest, tc.allowAny, got, tc.want)
			}
		})
	}
}

func TestCheckDevLeverOffersRelease(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	opts := mockOptions(t, "dev", ModeNotify)
	opts.AllowAnyVersion = true
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasUpdate || res.Latest != "2.0.0" {
		t.Fatalf("expected the dev lever to offer 2.0.0, got %+v", res)
	}
}

func TestCheckDevLeverShortensInterval(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	opts := mockOptions(t, "dev", ModeNotify)
	opts.AllowAnyVersion = true
	opts.Interval = DefaultInterval
	// Checked two hours ago: within the default window, outside the capped one.
	opts.Store = &fakeStore{state: LauncherState{LastChecked: time.Now().Add(-2 * time.Hour)}}
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasUpdate {
		t.Fatalf("the dev lever must cap the interval at MinInterval, got %+v", res)
	}
}

func TestUpgradeDevLeverAllowed(t *testing.T) {
	origLatest, origUpdate := LatestVersion, Update
	LatestVersion = func(context.Context, Channel) (string, error) { return "2.0.0", nil }
	var installed string
	Update = func(_ context.Context, latest string) error { installed = latest; return nil }
	t.Cleanup(func() { LatestVersion, Update = origLatest, origUpdate })

	if err := Upgrade(context.Background(), &termio.Mock{}, UpgradeOptions{CurrentVersion: "dev", AllowAnyVersion: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if installed != "2.0.0" {
		t.Fatalf("installed = %q, want 2.0.0", installed)
	}
}
```

Add to `cmd/agents-sandbox/cli_upgrade_test.go`:

```go
func TestUpgradeAnyVersionLever(t *testing.T) {
	origVersion, origLatest := version, upgrade.LatestVersion
	t.Cleanup(func() { version, upgrade.LatestVersion = origVersion, origLatest })

	t.Run("dev build with the lever set is upgradable", func(t *testing.T) {
		t.Setenv(upgradeAnyVersionEnv, "1")
		version = devVersion
		upgrade.LatestVersion = func(context.Context, upgrade.Channel) (string, error) { return "2.0.0", nil }
		origUpdate := upgrade.Update
		upgrade.Update = func(context.Context, string) error { return nil }
		t.Cleanup(func() { upgrade.Update = origUpdate })

		updateCmd, _ := setupUpgradeTestFixtures(t)
		if err := updateCmd.RunE(updateCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("dev build without the lever is rejected", func(t *testing.T) {
		t.Setenv(upgradeAnyVersionEnv, "")
		version = devVersion
		upgrade.LatestVersion = func(context.Context, upgrade.Channel) (string, error) {
			t.Fatal("LatestVersion must not be consulted without the lever")
			return "", nil
		}
		updateCmd, _ := setupUpgradeTestFixtures(t)
		if err := updateCmd.RunE(updateCmd, nil); err == nil {
			t.Fatal("expected a dev build to be rejected")
		}
	})
}
```

Also add `t.Setenv(upgradeAnyVersionEnv, "")` to the `dev build is rejected` subtest in the existing `TestUpgrade` (cmd), so a lever left in the developer's environment cannot flip it.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/upgrade/ -run 'TestNewerThan|TestCheckDevLever|TestUpgradeDevLever' -v`
Expected: FAIL to compile — `newerThan` undefined, `AllowAnyVersion` unknown field.

- [ ] **Step 3: Implement the lever in `internal/upgrade/upgrade.go`**

```go
// isParseable reports whether s is a semantic version, with or without a
// leading "v".
func isParseable(s string) bool {
	_, err := semver.NewVersion(strings.TrimPrefix(s, "v"))
	return err == nil
}

// newerThan reports whether latest should be offered over current. With
// allowAnyVersion an unparseable current version (empty, "dev", or garbage)
// counts as older than any parseable release. That is the undocumented dev
// lever: it is what lets a locally built binary exercise the real check and
// install instead of being skipped.
func newerThan(current, latest string, allowAnyVersion bool) (bool, error) {
	if allowAnyVersion && !isParseable(current) {
		if !isParseable(latest) {
			return false, fmt.Errorf("parse latest version %q", latest)
		}
		return true, nil
	}
	return isNewer(current, latest)
}
```

- Add `AllowAnyVersion bool` to `Options` and to `UpgradeOptions`.
- `Check`: gate the dev/empty short-circuit on the lever —

```go
	if !opts.AllowAnyVersion && (opts.CurrentVersion == "" || opts.CurrentVersion == devVersion) {
		return Result{}, nil
	}
```

— and cap the interval right after it is resolved and validated (keep the sub-minimum error, so a genuine misconfiguration still fails):

```go
	// The dev lever reduces the check window to the floor rather than removing
	// the throttle: the canary channel costs two GitHub calls per check, and an
	// unthrottled check would hit the unauthenticated rate limit.
	if opts.AllowAnyVersion && interval > MinInterval {
		interval = MinInterval
	}
```

- `Check` and `Upgrade`: replace the `isNewer(...)` call with `newerThan(current, latest, allowAnyVersion)`.
- `Upgrade`: gate the dev-build rejection the same way as `Check`.

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/upgrade/ -v`
Expected: PASS.

- [ ] **Step 5: Implement the cmd-side env read**

In `cmd/agents-sandbox/commands_cli.go`, beside `checkForUpgrade`:

```go
// upgradeAnyVersionEnv is the undocumented lever that lets a locally built dev
// binary exercise the real upgrade path: with it set, an unparseable current
// version (empty, "dev", or garbage) counts as older than any release. Any
// non-empty value enables it. It is deliberately absent from --help and docs.
const upgradeAnyVersionEnv = "AGENTS_SANDBOX_UPGRADE_ANY"

// upgradeAnyVersionEnabled reports whether the dev-build upgrade lever is set.
func upgradeAnyVersionEnabled() bool {
	return os.Getenv(upgradeAnyVersionEnv) != ""
}
```

`checkForUpgrade` passes `AllowAnyVersion: upgradeAnyVersionEnabled()`, and `buildUpgradeCmd` passes the same field in its `upgrade.UpgradeOptions`.

- [ ] **Step 6: Run the cmd tests**

Run: `go test ./cmd/agents-sandbox/ -run 'TestUpgrade' -v`
Expected: PASS.

- [ ] **Step 7: Run the whole suite and lint**

Run: `make fmt && make lint && go test ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/upgrade cmd/agents-sandbox
git commit -m "feat: add an undocumented lever to upgrade a dev build"
```

---

### Task 4: Publish prerelease tags as prereleases

**Files:**
- Modify: `.github/workflows/ci.yml` (the `publish` job)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: an `IS_PRERELEASE` environment value inside the `publish` job, set from the tag name.

- [ ] **Step 1: Make the change**

In `.github/workflows/ci.yml`, in the `publish` job, add a step before `Create GitHub Release`:

```yaml
      - name: Classify release tag
        run: |
          set -euo pipefail
          # Release tags are v<semver> (the workflow triggers on v*), so a
          # hyphen marks a semver prerelease part, e.g. v1.9.0-canary.1. A
          # prerelease must not be served by /releases/latest (that is the
          # stable channel's only source of truth) and must not bump the
          # Homebrew formula (the tap serves full releases only).
          if [[ "$GITHUB_REF_NAME" == *-* ]]; then
            echo "IS_PRERELEASE=true" >> "$GITHUB_ENV"
          else
            echo "IS_PRERELEASE=false" >> "$GITHUB_ENV"
          fi
```

Add `prerelease: ${{ env.IS_PRERELEASE }}` to the `Create GitHub Release` step's `with:` block.

Add `if: env.IS_PRERELEASE != 'true'` to both `Bump Homebrew formula` and `Open Homebrew formula bump PR`.

- [ ] **Step 2: Verify the workflow still parses and carries the change**

Run:
```bash
yq -e '.' .github/workflows/ci.yml > /dev/null && \
yq -e '.jobs.publish.steps[] | select(.name=="Classify release tag") | .run' .github/workflows/ci.yml > /dev/null && \
yq -e '.jobs.publish.steps[] | select(.name=="Create GitHub Release") | .with.prerelease' .github/workflows/ci.yml && \
yq -e '[.jobs.publish.steps[] | select(.name=="Bump Homebrew formula" or .name=="Open Homebrew formula bump PR") | .if] | length == 2' .github/workflows/ci.yml
```
Expected: the third command prints `${{ env.IS_PRERELEASE }}`; the fourth prints `true`; nothing errors.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: publish prerelease tags as GitHub prereleases"
```

---

### Task 5: Docs, changelog, and spec status

**Files:**
- Modify: `docs/configuration/self-upgrade.md`
- Modify: `docs/install/installation.md`
- Modify: `CHANGELOG.md` (`[Unreleased]`)

**Interfaces:**
- Consumes: the config key and env var from Task 2; the lever from Task 3 (documented only as absent).
- Produces: nothing consumed by other tasks.

- [ ] **Step 1: Document the channel**

In `docs/configuration/self-upgrade.md`, add a `## Release Channels` section after the mode table. Cover: `upgrade.channel` with values `stable` (default) and `canary` and the `AGENTS_SANDBOX_UPGRADE_CHANNEL` env var; that `canary` follows the newest release eligible for that channel, i.e. the newest canary prerelease (`1.9.0-canary.3`) or, once it ships, the real release; that `canary` installs roll onto the final release automatically; that setting the channel back to `stable` changes nothing until the next release ships, and the immediate way out is to reinstall the stable binary per the install page (link it with `{% link install/installation.md %}`); and that Homebrew installs ignore the setting because the tap serves full releases only. Add a pointer to the install page for the canary install command. Do not mention the dev lever.

- [ ] **Step 2: Document the canary install**

In `docs/install/installation.md`, add a short bullet after the release-binary instructions: to install a canary, download the asset from that prerelease's tag URL (`https://github.com/inoio/agents-sandbox/releases/download/v<version>/agents-sandbox-<os>-<arch>`) and set `upgrade.channel: canary` (or `AGENTS_SANDBOX_UPGRADE_CHANNEL=canary`) so future upgrades follow the canary line. Note that Homebrew cannot install canaries.

- [ ] **Step 3: Add the changelog entries**

Under `## [Unreleased]` → `### Added`:

```markdown
- Self-upgrade: a new `upgrade.channel` setting (`stable` default, `canary`) lets an install opt into canary
  prereleases. The canary channel follows the newest release eligible for it — the newest canary prerelease, and then
  the real release once it ships, so testers roll forward automatically. `agents-sandbox upgrade` accepts a one-shot
  `--channel` override. Homebrew installs are unaffected. Set it back to `stable` to leave the canary line at the next
  release.
```

Under `### Fixed`:

```markdown
- Release CI: a `v*-canary.N` tag is now published as a GitHub prerelease and no longer bumps the Homebrew formula, so a
  canary is never served by `/releases/latest` to stable installs. The self-upgrade download is addressed by the
  resolved release tag instead of `/latest`, so a prerelease can be installed.
```

Do not mention `AGENTS_SANDBOX_UPGRADE_ANY`.

- [ ] **Step 4: Verify docs and the suite**

Run: `make check`
Expected: PASS, including `docs-linkcheck`.

- [ ] **Step 5: Commit**

```bash
git add docs/configuration/self-upgrade.md docs/install/installation.md CHANGELOG.md
git commit -m "docs: document the upgrade channel setting"
```

---

## Post-implementation notes

Deviations from this plan's text, recorded after the work landed on `feat/upgrade-channels` (PR #271).

- **Task 1's call-site inventory was incomplete.** The plan said `downloadAssetToDir` is called from exactly four places,
  all in `upgrade_test.go`. `internal/upgrade/upgrade_extra_test.go` (tracked) also calls it and `updateExecutable`, so it
  had to change for the package to compile. It contains no `Upgrade`/`LatestVersion`/`Check` calls, so Tasks 2–3 did not
  touch it.
- **`canaryEligible(tag string) bool` became `canaryVersion(tag string) (*semver.Version, bool)`** (commit `914bec5`).
  The plan's predicate forced the max loop to re-parse each eligible tag, leaving an unreachable error `continue` that
  `codecov/patch` counted against the branch. Returning the parsed version removes the second parse. Eligibility
  semantics are unchanged.
- **`buildUpgradeCmd` binds `--channel` with `pflag`'s `StringVar`** rather than reading it back with
  `Flags().GetString`, for the same reason: the `GetString` error branch is unreachable once the flag is registered as a
  string two lines later. Behaviour is identical.
- **Task 5's file list omitted two reference pages.** The docs rule in `AGENTS.md` (keep `docs/` in sync) also required
  `docs/configuration/launcher.md`'s config-key table, validation list, and env-var table, and `docs/commands.md`'s
  `### upgrade` section.
- **`codecov/patch` is the practical coverage gate, and it is not documented.** It is not a required status check on
  `main`, there is no `codecov.yml` in the repo, and no brain document states a coverage goal — yet it caught five
  branches the feature's whole-branch review had explicitly accepted as non-blocking. See
  `specs/test-coverage.md`.
- **Task 4's snippet was corrected after PR review** (commit `2c56ef1`). The body prescribed above tests for a hyphen
  anywhere in the tag, which misclassifies a final release whose *build metadata* contains one: for `v1.9.0+build-42`,
  GitHub would publish it as a prerelease, `/releases/latest` would hide it from every stable install, and both Homebrew
  steps would be skipped — while the launcher's own semver parser treats the same tag as final, and the spec requires
  classification by the semver prerelease part. The step now strips build metadata (`version="${version%%+*}"`) before
  the hyphen test, so `IS_PRERELEASE` is true exactly when the tag's semver prerelease part is non-empty. The snippet
  above is left as the plan's original text.
