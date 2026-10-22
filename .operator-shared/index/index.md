---
description: Main codebase map for agents-sandbox, a Go CLI that launches coding agents (opencode, opencode2, pi, Claude Code) inside a microsandbox VM.
read_if: Navigating the codebase.
---

# Shared Project Index

## Architecture

- `agents-sandbox` is a Go CLI (module `github.com/inoio/agents-sandbox`, Go 1.26) that isolates coding agents (opencode, opencode2, pi, Claude Code) inside a dedicated microsandbox VM per project/agent.
- It uses `msb` (the microsandbox CLI / SDK) and Docker to build/reuse the VM, and Viper + Cobra for config and CLI.
- Layout: `cmd/agents-sandbox/` is the Cobra CLI; `internal/` holds the packages, dominated by `internal/sandbox/` (see its subindex). `docs/` is a Jekyll (just-the-docs) site; `ci/builder/`, `Formula/`, `.github/` handle cross-compile, Homebrew, and CI.
- Target platforms: Linux (KVM) and macOS (Apple Silicon); platform-specific code lives in `_darwin.go`/`_linux.go` files.
- Heavily test-driven; each `internal/` package pairs source files with `*_test.go` (and often `main_test.go`/`testmock.go`/`coverage_*.go`) for mocks and integration seams.

## Project Index

- `README.md` — Overview, comparison table, boundary/tradeoffs.
- `STYLEGUIDE.md` — Documentation styleguide for user-facing docs.
- `AGENTS.md` — Authoritative agent instructions (dev workflow, toolchain, lint/test commands).
- `GLOSSARY.md` — Project terminology.
- `CHANGELOG.md`, `ROADMAP.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `LICENSE.md`, `SECURITY.md` — Project metadata and policy.
- `Makefile` — fmt/lint/test/build targets.
- `go.mod`, `go.sum` — Module deps (cobra, viper, go-git, msb SDK, docker/moby, testify).
- `opencode.jsonc` — opencode config for this repo.
- `.golangci.yml`, `.gitignore` — Lint config and ignore rules.
- Tooling dirs (`.idea/`, `.run/`, `.opencode/`, `.pi/`, `.superpowers/`, `.junie/`, `docs/vendor/`) — IDE/tooling config; omitted.

### `cmd/agents-sandbox/` — Cobra CLI entry and command implementations

- `main.go` — Entry point; wires termio UI and calls `execute`.
- `cli.go` — Cobra root, `execute`, injection seams (msb/docker mocks).
- `launcher_store.go` — Adapter binding the root-state transaction API to the `internal/upgrade` store seam (turns a contended non-blocking lock into an error so the check defers rather than reading a false empty state).
- `commands.go` — Root/tree/version/upgrade commands and wiring.
- `commands_cli.go` — run/shell/stop/kill commands.
- `commands_system.go` — doctor/list/config/home/agent/build/dockerfile/image/volume/sandbox/prune commands, including `config paths` (the config layers in precedence order, driven by `viperconfig.ConfigLayers`).
- `commands_inspect.go` — `volume inspect`/`image inspect`/`sandbox inspect` command builders.
- `commands_storage.go` — `df` command and the `storage` group alias (`storage df`/`storage prune`).
- `list_output.go`, `inspect_output.go`, `prune_output.go`, `storage_output.go` — shared table/JSON/quiet list rendering, single-object inspect rendering, prune report JSON, and the msb-shaped `df` report.
- `cli_storage_test.go` — CLI tests for `df` (table/JSON/verbose/quiet/degradation) and the `storage` group.
- `constants.go`, `tree.go` — command name/flag constants and `tree` output.
- `cli_*_test.go` — Extensive CLI integration tests (one per command/area).

### `internal/` — Core packages (non-sandbox)

- `agent/` — Agent (opencode/opencode2/pi/claudecode) version resolution, manifests, image selection.
- `atomicwrite/` — Atomic file writes (unique temp file + rename, 0600/0700); shared by the persistent state/config writers (`state.WriteState`, `state.WriteRootState`, `vm.saveUpgradeState`, `upgrade.saveState`).
- `configfile/` — Comment/order-preserving launcher-config editor (`Config`, `Parse`, `LoadDir`, `WriteAtomic`); shared by `configmigration` and the setup wizard. *(Guided-onboarding work; not on `main` yet.)*
- `configmerge/` — Merge config with precedence.
- `configmigration/` — **See [configmigration subindex](configmigration.md)** (native agent config migration, guided onboarding, network plan). *(Guided-onboarding work; not on `main` yet.)*
- `configpaths/` — Config/home path resolution and guards.
- `filewalk/` — Best-effort recursive file walk shared by the tool.
- `git/` — Git helpers (go-git wrapper) and project identity: `Identity`/`ResolveIdentity` with the repo slug (origin-derived; keys the per-slug user config dir and the grouping label) and the checkout slug (git common dir; keys state/VM/volume/locks/image tag), plus the bind-root rule (#144).
- `homeconfig/` — Home config provisioning.
- `humanize/` — Human-readable formatting.
- `notify/` — Desktop notifications (backend, dedup, events, watch).
- `sandbox/` — **See [sandbox subindex](sandbox.md)** (VM lifecycle, docker, image, msb, volumes, pruning, sessions, state).
- `sysinfo/` — Platform system info (`_darwin.go`/`_linux.go`).
- `termio/` — Terminal UI (printer, table, prompt, spinner, ansi, levels).
- `testutil/` — Test helpers (mocks, testgit) and the test-isolation contract test (`isolation_contract_test.go`).
- `upgrade/` — Self-upgrade (incl. Homebrew).
- `viperconfig/` — Viper-based launcher config, incl. `ConfigLayers` (the six-layer precedence list that drives both `NewResolver`'s merge order and the `config paths` display, so the two cannot drift).
- `yamlfmt/` — YAML formatting.

### `test/harness/` — `msb`-tagged harness smoke tests

- `harness.go` — Harness table (per-harness quit key, network override, normal-buffer and reacts-only flags), child-env allowlist, daemon-timeout/ANSI/alt-screen helpers.
- `pty.go` — pty driver running a command under a sized pseudo-terminal and capturing its output.
- `smoke_msb_test.go` — The `msb`-tagged test: platform/KVM preflight, then shared root + binary build, isolated-env docker preflight, per-harness image prebuild, then starts each harness (`agents-sandbox run --agent <name>`) in a real microsandbox VM and asserts TUI + clean exit (taps the quit key until the process goes).
- `harness_test.go`, `pty_test.go` — Untagged unit tests (table covers every registered agent, env allowlist, pty round-trip/exit/tail).
- `main_test.go` — Inert `TestMain` installing the fail-fast seam mocks.

### `docs/` — Jekyll documentation site (just-the-docs)

- `_config.yml`, `Gemfile`, `_includes/*.html` — Site config and templates.
- `*.md` — Pages (introduction, install, commands, configuration, how-it-works, sandboxes, switch, troubleshooting, recipes, runner-image, manage-config, branch-sessions, deprecations).
- `configuration/` — Per-feature docs (agent, launcher, mounts, networking, secrets, notifications, home-provisioning, self-upgrade).
- `diagrams/*.puml` — PlantUML C4 and lifecycle diagrams.
- `vendor/` — Gitignored gem bundle; omit.

### `ci/builder/` — Cross-compile builder image

- `Dockerfile`, `stubs/*.tbd` — Builder with macOS linker stubs.
- `ci/check-docs.sh` — Docs build check script.

### `Formula/` — Homebrew formula

- `agents-sandbox.rb` — Formula for macOS install.

### `.github/` — CI and issue templates

- `workflows/ci.yml`, `workflows/pages.yml` — CI and GitHub Pages workflows.
- `ISSUE_TEMPLATE/*.yml`, `PULL_REQUEST_TEMPLATE.md`, `dependabot.yml` — Templates and dependabot config.

### `.agents-sandbox/` — Development VM

- `Dockerfile` — Dev environment image (go, msb, docker, etc.).
- `config.yaml`, `*.sh`, `opencode/` — Dev VM provisioning/scripts.

### `.agents/` — Repo engineering skills (untracked tooling)

- `skills/` — Matt Pocock engineering skills (ask-matt, code-review, tdd, brainstorming, etc.); directory-only.
