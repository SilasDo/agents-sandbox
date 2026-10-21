---
description: Design contract for a hard CI gate that fails when production code is unreachable from any main package — the `make deadcode` target over a pinned x/tools deadcode, a reason-bearing `//deadcode:ignore` marker we own (deadcode has no suppression feature) with file and declaration scope, a `ci/check-deadcode.sh` report parser because deadcode always exits 0, stale-marker enforcement, the linux-only GOOS constraint forced by the microsandbox SDK, and the evidence that `integration`/`msb` tag configurations are inert today.
read_if: Implementing or reviewing the dead-code CI gate, the `make deadcode` target, the `//deadcode:ignore` marker convention, `ci/check-deadcode.sh`, or the DEADCODE_VERSION pin.
---

# Dead-code gate

## Intent

`staticcheck`'s `unused` (U1000) counts test files as usage, so a production function whose only callers are in
`_test.go` looks alive and is invisible to every linter the repo runs. `golang.org/x/tools/cmd/deadcode` closes this: it
builds an RTA call graph from **main packages only** and reports everything else as unreachable, by default without even
loading test files. See `guides/dead-code-detection-go.md` for the survey and evidence.

This change turns that report into a **hard gate**. The contract is not "no test-only code exists" — intentional test
seams are legitimate and numerous. It is: *every function unreachable from production is either deleted, or carries an
explicit reason-bearing marker*. A function that is merely dead fails the build.

## Evidence this design is built on

All measured on this VM on 2026-10-21 with `golang.org/x/tools/cmd/deadcode@v0.52.0`, linux/amd64, module `github.com/inoio/agents-sandbox`.

- **deadcode always exits 0**, findings or not (`exit_code_with_dead_code=0`). The gate cannot read the exit code; it
  must collect the report and fail itself.
- **It has no suppression mechanism.** Its whole flag set is `-test`, `-tags`, `-filter`, `-generated`, `-whylive`,
  `-f`, `-json`, `-cpuprofile`, `-memprofile`. Grepping `deadcode.go` for `ignore`/`exclude`/`directive`/`suppress`
  returns only implementation comments. The one source-level opt-out it honours is Go's generated-code marker
  (`// Code generated … DO NOT EDIT.`), which is whole-file and not abusable as a per-function lever. **Any marker scheme
  is ours to define and enforce.**
- **`-tags integration` and `-tags msb` change nothing today.** All four configurations (none, `integration`, `msb`,
  `integration msb`) report exactly **246** functions. The reason: every file behind those tags is a `_test.go` file
  (`internal/sandbox/docker/docker_real_test.go`, `internal/sandbox/image/render_integration_test.go`,
  `cmd/agents-sandbox/cli_doctor_test.go`, `test/harness/smoke_msb_test.go`) and deadcode skips `_test.go` files by
  default. The tags are included anyway as future-proofing (see Decisions).
- **The baseline is 246 findings in 25 files**, and 227 of them are concentrated in ten test-support files:
  `internal/sandbox/msb/testmock.go` (107), `internal/termio/mock.go` (31), `internal/sandbox/docker/testmock.go` (24),
  `internal/configpaths/testmock.go` (24), `test/harness/pty.go` (20), `test/harness/harness.go` (7),
  `internal/sandbox/doctor/testmock.go` (7), `internal/testutil/testutil.go` (3), `internal/testutil/testgit.go` (3),
  `internal/testutil/mocks/testutil.go` (1). The remaining 19 are spread over 15 files, mostly production files holding
  one or two test seams or small dead helpers.
- **Cgo is required.** The FFI types above come from cgo-generated code: `CGO_ENABLED=0` fails identically
  (`undefined: CustomNetworkPolicy` and friends), and only `CGO_ENABLED=1` on the **host's own** GOOS produces a report.
- **A cross-GOOS run is not currently possible.** `GOOS=darwin` on linux fails to type-check the module before deadcode
  gets anywhere: the microsandbox SDK's `internal/ffi/restore.go` has `undefined: CustomNetworkPolicy` / `MountSpec` /
  `PortBindingOptions` / `VsockRouteOptions`, because those FFI types do not exist for the cross configuration.
  `GOOS=darwin go build ./...` fails identically, so this is a pre-existing module-level condition, not a deadcode
  problem. Combined with the cgo requirement, this means the analysis can only run for the host's own GOOS unless a cross
  C toolchain is supplied.
- **No `GOARCH` variants exist** in the module (`git ls-files '*.go' | rg '_(amd64|arm64|386|arm)\.go$'` is empty), so the
  report does not vary by architecture and `GOARCH` needs no pinning.
- Reference run cost: ~10 s for the whole module, linux, no tags.

## Contract

- **Hard gate, in `verify` and in CI.** Wired into `make verify` (not `make check`) and into a dedicated CI job that
  mirrors the `vulncheck` job's shape. Unlike `make mutation`, this **is** a gate: it fails the build.
- **Pinned, no global install.** `DEADCODE_VERSION` is a `Makefile` constant, invoked as
  `go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION)`, exactly like `GOVULNCHECK_VERSION`/`vulncheck`. As with
  `GOMUTANTS_VERSION`, dependabot (`gomod`, `github-actions`) does not see it, so it is bumped by hand.
- **The exit code is not the signal.** `ci/check-deadcode.sh` runs deadcode with `-json`, applies the marker and
  configuration filters, prints the remaining findings in the compiler-diagnostic form deadcode uses by default, and
  exits non-zero when any survive. The Makefile target is a thin wrapper over that script, in the style of
  `ci/check-docs.sh` + `ci/test-check-docs.sh`; the script gets a matching `ci/test-check-deadcode.sh`.
- **`//deadcode:ignore reason="…"` is our contract, in two scopes.**
  - *Declaration scope* — in the contiguous comment block immediately preceding the declaration deadcode reported, or as
    a trailing comment on the declaration line (the repo has one-line methods such as
    `internal/termio/termio.go` `OutToVerboseRedirect.Out`, which has no doc comment). Covers exactly that function or
    method.
  - *File scope* — in the comment block preceding the `package` clause. Covers every finding in the file. This exists for
    the test-support files above (227 findings over 10 files), where a marker per function would be pure noise.
  - The `reason` is **required and non-empty** in both scopes. An empty or missing reason is a failure, not a default.
- **Stale markers fail too.** A marker whose declaration or file is *not* reported as dead is an error. This is the
  mechanism's self-defence: markers cannot accumulate as decoration after the code they justified is deleted or becomes
  live. Enforced by `ci/check-deadcode.sh`, in the spirit of the deprecation-register regression tests.
- **Configuration matrix.** deadcode analyses one configuration per run. The gate runs one run per configuration in a
  matrix over the build tags (`''`, `integration`, `msb`, `integration msb`) and fails if **any** configuration reports an
  unmarked finding. `GOOS` is the host's own (see Decisions); `GOARCH` is left at the host default because the module has
  no architecture-specific files.
- **`make deadcode` is available on demand** with the same knobs as the other audit targets, so a developer can inspect
  before pushing: `PKG` to scope, and the tags/GOOS matrix overridable.

## Decisions

- **Marker, not a register.** A checked-in `pkg.Function  reason` file would have been cheaper to implement (a `jq`
  filter, no source parsing) but puts the reason away from the code and rots as entries outlive their functions. The
  marker keeps the reason next to the thing it excuses and survives refactors. The cost, accepted: we own a small source
  parser in `ci/`. Recorded decision of the maintainer.
- **`verify`, not `check`.** `check` is the fast local loop (`fmt lint test compile-all docs-linkcheck docs-linkcheck-test`);
  `verify` is the read-only CI-parity target and already carries `vulncheck`, the closest analogue. This works because the
  gate analyses the host's own GOOS, so it is runnable on every supported development host (see Decisions).
- **GOOS: the host's own, decided.** CI (`ubuntu-latest`) analyses linux; a macOS developer analyses darwin natively. No
  `GOOS` pin and no cross C toolchain. Accepted consequence: the two platforms' finding sets need not agree, and the
  darwin baseline is unverified because it cannot be computed from the linux dev VM — a Mac run may surface a few extra
  markers for whoever runs it to contribute. `GOARCH` stays at the host default (no architecture-specific files exist).
- **The CI job blocks PRs, not releases, decided.** `deadcode` is **not** added to `release`'s
  `needs: [lint, test, integration]`, exactly like `vulncheck`. A release tag can therefore publish past a failing
  dead-code gate; this matches the existing deliberate budget choice for `vulncheck`. The gate's enforcement point is the
  pull request.
- **Resolved by the builder:** the `ci/` script takes deadcode's JSON through an injectable seam (stdin or an env
  override) so `ci/test-check-deadcode.sh` feeds canned JSON plus a fixture source tree for marker resolution, and never
  invokes the real pinned tool. The `make deadcode` recipe passes `PKG`/`TAGS` through to the script as environment, not
  as positional arguments — the shape sketch's bare `ci/check-deadcode.sh` recipe is schematic.
- **Resolved by the builder:** a `PKG`-scoped run is an inspection aid, not the gate. It prints findings, skips
  stale-marker enforcement (which needs the whole module to know what is justified), and says so in its output.
- **The baseline is resolved, not merely marked.** The gate must be green from the commit that introduces it, or it is
  never switched on. Triaging the 19 scattered findings showed 7 are intentional test seams (`agent.Register`,
  `viperconfig.NewResolverWithConfig`, `docker.RealClient`, `image.WithMockAgentVersion`,
  `image.WithMockAgentVersionResolver`, `vm.SetDaemonShellFunc`, `vm.SetRuntimeHomeMigrationOffer`) and will carry a
  reason-bearing marker, while **12 are genuinely dead and are deleted**: `homeconfig.MergeManifests`,
  `homeconfig.BuildHomeFiles`, `homeconfig.DescribeManifest` (superseded by the production `BuildHomeFilesWithModes` /
  `DescribeLayers` / `LoadLayers`), `runtime.InspectRuntime` (leftover of the retired version-matching recovery),
  `upgrade.IsNewer` (a wrapper existing only for its own test), `termio.OutToVerboseRedirect` and its `Out`/`Outf`
  (duplicated by `cmd`'s live `autoPruneOutToVerboseRedirect`), `imageIdentity.values`, `reprovision.AgentConfigPath`,
  `state.RootTxn.Exists`, `volume.VolumeAction.String` (the live formatter is `actionLabel`), and
  `notify.Tracker.HandleEventType` (the live entry point is `Tracker.Handle`). The ten test-support files holding the
  other 227 findings get one file-scope marker each. Marking a genuinely dead function to silence the gate is the
  rubber stamp the anti-goals forbid. See `plans/2026-10-21-dead-code-gate.md` for the per-symbol disposition and the
  tests that must be retargeted rather than deleted.
- **Resolved by the builder:** markers are additive, so if the BuildKit plan (issue #15) removes `internal/sandbox/docker`
  first, the 24 `docker/testmock.go` markers disappear with the package and the totals in this document are stale while the
  mechanism is unaffected.
- **Tag configurations included despite being inert.** The user asked for `integration` and `msb`; the evidence says they
  add nothing today. They are kept because the failure mode they guard against is silent: the first non-`_test.go` file
  to land behind `//go:build integration` or `//go:build msb` would otherwise be analysed by nobody. The cost is ~10 s × 4.

## Goals

- A PR fails when a function becomes unreachable from production and carries no reason-bearing marker.
- Test seams stay expressible without per-function ceremony, and with a machine-checked reason.
- Markers cannot outlive their justification.

## Non-goals

- Covering darwin/arm64 analysis.
- Finding dead *code* that is reachable but unexercised by tests (that is coverage's job), or dead *types*, *fields*,
  *constants*, and unused imports (staticcheck's job, and it does those correctly).
- Gating on the mutation-testing model (a score, a threshold).

## Anti-goals

- A gate that fails on day one because the baseline was not cleared, and is therefore disabled by the first person who
  hits it.
- Markers as a rubber stamp: a `reason` that says nothing ("test", "unused"), or file-scope markers applied to production
  files to silence an inconvenient finding.
- The gate silently weakening when `ci/check-deadcode.sh` fails to parse deadcode's output — an empty finding set must be
  distinguishable from a failed analysis. A non-zero deadcode exit, a non-JSON payload, or a missing `-json` key is an
  error, never "no findings".
- Making `make verify` unusable on a supported development host.

## Constraints

- Follow `AGENTS.md`: idiomatic Go for any helper, `jq` for the JSON (never brittle text parsing), `make fmt`/`make lint`,
  `make check` before the final commit.
- `CHANGELOG.md` `[Unreleased]`, `CONTRIBUTING.md` (Testing), and `.agents/skills/release/NON-GO-DEPS.md` (the
  `DEADCODE_VERSION` row, alongside the existing `gomutants`/`govulncheck` pins) stay in sync.
- The private/CI script follows the `ci/check-docs.sh` precedent: a POSIX-sh script plus a `ci/test-check-*.sh` test, both
  wired into `verify` (the existing pair is wired into `check` via `docs-linkcheck-test`).
- `GOMUTANTS_VERSION`'s lesson applies: the pin is not visible to dependabot, so it must be documented where the other
  hand-bumped pins are.

## Shape

```make
DEADCODE_VERSION = v0.52.0

# Hard gate: production code unreachable from any main package must be deleted or
# carry a //deadcode:ignore marker with a reason. Never exits from deadcode itself
# (it always exits 0) — ci/check-deadcode.sh parses the report and fails.
#   make deadcode                 # default configuration
#   make deadcode PKG=./internal/foo
#   make deadcode TAGS='integration msb'
deadcode:
	PKG='$(PKG)' TAGS='$(TAGS)' ci/check-deadcode.sh

verify: validate-fmt lint test test-race compile-all vulncheck deadcode docs-linkcheck docs-linkcheck-test
```

CI gains a job mirroring `vulncheck`, deliberately absent from `release`'s `needs:`:

```yaml
  deadcode:
    name: deadcode
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: .go-version
          cache: true
      - run: make deadcode
```

## Open items

- Whether the marker regex/handling should live in `ci/check-deadcode.sh` (awk, matching `check-docs.sh`) or in a small Go
  helper the script calls. The declaration-scope walk-back over the preceding comment block is the fiddly part; shell is
  cheaper to wire, Go is cheaper to test.
- Exact `//deadcode:ignore` vs `//dead-code:ignore` spelling, and whether a `reason=` value is delimited by double quotes
  only (mirroring `// gomutants:disable <TYPE> reason="..."`).
