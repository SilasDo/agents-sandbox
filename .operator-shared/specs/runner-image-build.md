---
description: Contract for deciding which agent version is baked into the runner image, when it is (re)built, and when the Docker layer cache is bypassed.
read_if: Changing image build/caching, BuildOptions, `--rebuild`, `--agent-version`, agent upgrade, or the volume subcommands' image handling.
---
# Runner image build and cache contract

How the rendered Dockerfile is arranged into stages (so layer caching works as assumed here) is owned by
`specs/image-composition.md`.

## Build decision vs. cache bypass

`EnsureImageWithClient` makes two independent decisions from one input, `BuildOptions.Force`:

- **Build at all:** `needsBuild = Force || !imageHasDockerfileID(...)`. The `dockerfile-id` label is the source
  signature (rendered Dockerfile + resolved agent version + every build-context file with its name, mode, and content;
  the files travel via the build context rather than the Dockerfile text, so each is folded into the signature for an
  edit to invalidate a warm image — see `specs/image-composition.md`); a mismatch means the rendered recipe changed and
  triggers a build. It is a
  signature of the declared inputs, not a content identity: it deliberately excludes host-dependent inputs (the
  resolved base digest and the dev user's uid/gid), so a label match does not imply equal image bytes.
- **Bypass cache:** `Force` is passed through to `NoCache` on the Docker build.

So `Force` means **clean rebuild**: rebuild even when the identity matches, with `NoCache=true`. It is set only by an
explicit user `--rebuild` on `build`, `run`, or `shell`.

A build triggered by a stale identity (changed project Dockerfile, agent version, or base) reuses Docker's layer cache. This
is safe because each change is visible to Docker's cache key: the agent version is rendered as the agent stage's install `ARG`
default (and also passed as a build arg), so it is part of the Dockerfile content and invalidates the agent install layer, and
the `dockerfile-id` label lives after the agent install. An accepted agent upgrade is a version change, not a force: it
rebuilds the install layer while keeping earlier layers cached. The node and docker stages render their pinned versions the
same way (see `specs/image-composition.md`).

## Load decision

`EnsureImageWithClient` returns an `ImageInfo{Tag, Digest, DockerfileID, Env}`. `Digest` is Docker's image ID, i.e. the
same content identity microsandbox computes for an image it holds (`config.digest = sha256(config_bytes)`), and
`DockerfileID` is the source signature above. Loading into microsandbox is deferred to a separate `EnsureLoaded` call,
so reconnecting to a running VM never pays the import cost.

`EnsureLoaded(ctx, client, info, ui)` decides from the msb cache only, never by inspecting host Docker:

- msb image missing → load
- msb image present with a digest → skip iff it equals `info.Digest`, otherwise load
- msb image present with no digest (legacy) → fall back to comparing the `dockerfile-id` label against `info.DockerfileID`

A digest mismatch is never overridden by a matching `dockerfile-id` label — the label is a source signature and can
match while the bytes differ (base tag move + `--rebuild`). A load always reuses the same reference (it never removes
the cached image first), because an existing VM may still reference it. The build/load split is deliberate: `run`/`shell`
build eagerly via `EnsureImage` but load lazily, only when a VM is actually created or recreated.

## Version selection

`resolveBuildVersion` (run/shell) picks the version to bake with this precedence:

1. **User-provided agent** (`agent-source=user`) → recorded version, no check; any `--agent-version` pin is **ignored** (the tool
   does not own the agent, so a pin cannot change what is baked).
2. **`--agent-version <v>` pin** → `v`, no upgrade check.
3. **No upgrade checker** → recorded version, no check.
4. **No recorded baseline** → empty (resolved to latest at build time).
5. Otherwise → upgrade check; may prompt to rebuild with a newer release.

`--agent-version` is available on `build`, `run`, and `shell`. `--rebuild` is orthogonal: it only forces a clean rebuild of
whichever version is selected and does **not** suppress the upgrade check. `build` has no upgrade check because it always resolves a
fresh version (pinned or latest) rather than reusing the baked one.

### Latest-version sources

Each profile's `LatestVersion` (the upgrade-check source) must mirror the source that profile's upstream installer
actually resolves, so the pinned version is always one the installer can fetch:

- `opencode` → GitHub `releases/latest`; `pi` → `pi.dev`; `claude-code` → npm `latest` dist-tag.
- `opencode2` → the opencode update API's `latest` channel (`https://opencode.ai/update/api/latest/cli/npm`), the same
  endpoint `https://opencode.ai/v2/install` queries when unpinned. Do **not** read an npm dist-tag here: the deprecated
  `@opencode-ai/cli` `beta` tag resolves to a version whose platform tarball ships `bin/opencode2`, which the v2
  installer (expecting `bin/opencode`) cannot install, so the runner-image build fails at the installer's `mv` step.
  The `0.0.0-beta-<build>` numeric-comparison path in `newerOpenCode2Than` is retained only to compare against a
  previously baked pre-GA snapshot.

## Callers

- `build` / `image build` → `image.Build` → `Force = -r/--rebuild`.
- `run` / `shell` → `PrepareSandbox` → `Force = opts.Rebuild` (`-r/--rebuild`). An accepted upgrade (`shallUpgrade`) is
  **not** a force; it triggers the build via the changed version identity.
- `volume migrate` / `volume reset` / `volume edit` → `image.EnsureImage` → `Force = false`. These subcommands do **not**
  expose `--rebuild`; they ensure a current image but never force a clean rebuild.

## Invariants

- `BuildOptions.Force` is the only input that bypasses the layer cache; a stale-image rebuild never does.
- No volume subcommand exposes `--rebuild`.
- The internal build helper parameter that maps to `NoCache` is named `noCache`, not `force`, so the cache meaning stays
  explicit at the build call.