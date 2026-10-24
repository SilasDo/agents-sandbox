# Test build-cost reduction implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut the per-run `go test ./...` link cost by stopping the 36 MB microsandbox native bundle from being linked into test binaries that cannot reach it.

**Architecture:** The bundle enters a test binary only through the `msb` package. The isolation contract currently makes every test package install the fail-fast default for *every acyclic seam family*, so any package that calls `testutil/mocks.InitFailFastMocks` — even a leaf — links `msb` and the embedded `.so`. Refine the contract's expected set to the families a package's **code and its test files can actually reach**, then migrate each `TestMain` to install exactly that set. Packages whose production code imports `msb` keep the bundle (unavoidable); leaf packages drop it and shrink from ~47 MB to ~13 MB.

**Tech Stack:** Go; `internal/testutil/isolation_contract_test.go` (AST + `go/build` import-graph derivation); the fail-fast seams `InstallFailFast*`.

**Spec:** `specs/test-isolation.md` owns the contract; `guides/test-runtime-cost.md` holds the measurements that motivate the change. Implementation is on `perf/test-runtime-cost` (a nested worktree of the bare `/workspace`), which also carries the cheap timeout/network fixes.

## Context and decisions

- Measured motivation: run mode re-links every test binary each invocation (the `-o $WORK/...` path defeats the link cache); each link processes a ~44-55 MB binary whose bulk is the SDK's embedded native library. A leaf binary that installs only `docker.InstallFailFastGet()` is ~13-14 MB and links in ~1.3-1.7 s versus ~2.4 s at ~47 MB (`guides/test-runtime-cost.md`).
- The contract's current rule (`acyclicSeamFamilies`) over-installs: it installs a family whenever installing it would not cycle, regardless of whether the package can reach it. This is what drags `msb` into every binary.
- Decision: require a family only when it is **reachable** — the package's non-test transitive imports include the family's package, or a `*_test.go` file imports it directly. Keep the cycle guard (a family that transitively imports the package cannot be imported without a cycle and stays excluded).
- The over-install is also a latent safety net: a leaf test that calls a seam directly currently panics via the blanket install. The new derivation covers that case by counting direct test imports, so a test that calls a seam must import it; the derivation is what keeps the guarantee exact rather than incidental.
- Non-goal: making `go test` itself reuse the cached link. The work directory path is part of the link action key; that is Go's behavior, not ours to change.

## Global Constraints

- `make check` must pass (fmt, lint, test, compile-all, docs-linkcheck, docs-linkcheck-test, deadcode-test); `make compile-all` compiles the `integration`/`msb`-tagged files.
- The dead-code gate: if the wrapper `InitFailFastMocks` ends up with no non-test caller after migration, remove it and its guard test (`TestInitFailFastMocksInstallsEveryFamily`) rather than leaving a `//deadcode:ignore` on a genuinely dead helper.
- Update `specs/test-isolation.md` in the same change: the contract prose, the per-package exception list, and the three enforcement-test descriptions must describe the reachability rule, not the acyclic rule.
- No production (`*.go` non-test) file changes; this is a test-support change, so no CHANGELOG or user-docs line is required.
- Add a `[Unreleased]` line to `CHANGELOG.md` only if the reviewers ask for one; the change is not user-facing.

## Review Focus

- A package whose `TestMain` still installs a family it cannot reach → the bundle stays linked; the win is lost for that package.
- A `TestMain` that drops a family its code does reach → a test silently reaches the real implementation; the full suite is the detector.
- The exception list in `test-isolation.md` describes the old rule → the next contributor reasons from a stale contract.

---

### Task 1: Derive the required seam set from reachability

**Files:**
- Modify: `internal/testutil/isolation_contract_test.go`
- Test: `internal/testutil/isolation_contract_test.go` (add a focused table test)

**Interfaces:**
- Consumes: `seamFamilies`, `transitiveModuleImports` (non-test transitive module-local imports), and a new per-package set of test-only direct module imports.
- Produces: `requiredSeamFamilies(pkgDir string, transitive, testImports map[string]map[string]bool) []string`, replacing `acyclicSeamFamilies` at its one call site in `TestEveryTestPackageInstallsItsAcyclicFailFastMocks`.

- [ ] **Step 1: Write the failing test first**

Add a table test over `requiredSeamFamilies` with synthetic inputs:

- a leaf package that transitively imports no family package and whose tests import no family package → wants `[]`;
- the same leaf with `msb` in its test imports → wants `["msb"]`;
- a package whose non-test imports transitively include `msb` and `configpaths` → wants `["configpaths", "msb"]`;
- a family whose package transitively imports the package under test (a cycle) → excluded even though the test imports it.

Run: `go test ./internal/testutil/ -run TestRequiredSeamFamilies`
Expected: FAIL (the function does not exist yet).

- [ ] **Step 2: Implement `requiredSeamFamilies`**

```go
// requiredSeamFamilies returns the families whose fail-fast a package's tests
// must install: a family is required when the package can reach its seam —
// its non-test imports include the family package, or a test file imports it —
// and importing the family would not cycle.
func requiredSeamFamilies(pkgDir string, transitive, testImports map[string]map[string]bool) []string {
	var families []string
	for _, family := range seamFamilies {
		if family.dir == pkgDir || !transitive[family.dir][pkgDir] {
			if transitive[pkgDir][family.dir] || testImports[pkgDir][family.dir] {
				families = append(families, family.name)
			}
		}
	}
	sort.Strings(families)
	return families
}
```

- [ ] **Step 3: Compute `testImports` from the `_test.go` files**

Add `testModuleImports(t, root, modulePath, packages) map[string]map[string]bool` next to `transitiveModuleImports`: for each package directory, parse the `*_test.go` files with `go/parser` (the existing `directModuleImports` uses `go/build`, which ignores `_test.go`), keep the module-local imports, and **drop `internal/testutil/mocks`** so the wrapper's own imports do not make every family "reachable".

- [ ] **Step 4: Switch the contract test to the new derivation**

In `TestEveryTestPackageInstallsItsAcyclicFailFastMocks`: build `testImports`, replace `acyclicSeamFamilies(...)` with `requiredSeamFamilies(...)`, and rename the test to `TestEveryTestPackageInstallsItsRequiredFailFastMocks` (update the doc comment: the expected set is the reachable families, not the acyclic ones).

Run: `go test ./internal/testutil/ -run 'TestRequiredSeamFamilies|TestEveryTestPackageInstallsItsRequiredFailFastMocks'`
Expected: the table test passes; the contract test now FAILS with a per-package list of over-installed families (the migration list for Task 2).

- [ ] **Step 5: Commit**

```bash
git add internal/testutil/isolation_contract_test.go
git commit -m "test(isolation): require only reachable seam families"
```

---

### Task 2: Migrate every `TestMain` to its required set

**Files:**
- Modify: every `internal/**/main_test.go` (and `cmd/agents-sandbox/main_test.go`) that installs more than its required set
- Modify: `internal/testutil/mocks/testutil.go`, `internal/testutil/isolation_contract_test.go` (if the wrapper becomes unused)

**Interfaces:**
- Consumes: the per-package required lists the contract test prints.
- Produces: `main_test.go` files that call only the required `InstallFailFast*` functions, called directly before `m.Run()`.

- [ ] **Step 1: Read the contract test's failure list**

Run: `go test ./internal/testutil/ -run TestEveryTestPackageInstallsItsRequiredFailFastMocks`
Expected: one `…: fail-fast mocks installed = […], want […]` line per over-installing package.

- [ ] **Step 2: Replace the wrapper with explicit installers, package by package**

For each reported package, in `main_test.go` replace `termio.InitFailFastMocks(m)` (or `mocks.InitFailFastMocks(m)`) with the `want` set as direct calls, then `m.Run()`:

```go
func TestMain(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	m.Run()
}
```

Keep the import local name already used in the file. Do not reorder or merge `TestMain` bodies beyond the installer block.

- [ ] **Step 3: Re-run until the contract test passes**

Run: `go test ./internal/testutil/ -run TestEveryTestPackageInstallsItsRequiredFailFastMocks`
Expected: PASS.

- [ ] **Step 4: Run the full suite to catch a dropped-and-needed family**

Run: `make test`
Expected: PASS. A package that now reaches a real implementation panics with the seam's message; add that family back to the package's installer block (and, if the derivation missed it, fix Task 1).

- [ ] **Step 5: Remove the wrapper if it is now dead**

Run: `rg -n 'InitFailFastMocks' --glob '!**/*_test.go' internal`
If the only remaining definition is in `internal/testutil/mocks`, delete `InitFailFastMocks` and `TestInitFailFastMocksInstallsEveryFamily`, and drop `internal/testutil/mocks` from the import graph where it was used only for the wrapper. If at least one package still requires all five families, keep the wrapper for that package and leave `TestInitFailFastMocksInstallsEveryFamily` in place.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "test(isolation): install only reachable seam families"
```

---

### Task 3: Update the contract spec and verify the win

**Files:**
- Modify: `specs/test-isolation.md`
- Modify: `.operator-shared/catalog.md` (entry `Read If`/`Description` if the scope moved)

- [ ] **Step 1: Rewrite the contract prose**

In `specs/test-isolation.md` replace the "every seam family its tests can import without a test import cycle" phrasing with the reachability rule; update the three enforcement-test descriptions (`TestEveryTestPackageInstallsItsRequiredFailFastMocks`, and the wrapper guard if it survives) and the per-package exception list to state each package's *required* set.

- [ ] **Step 2: Verify the binary-size and link-count win**

Run:
```bash
for p in internal/humanize internal/termio internal/git internal/sandbox/vm; do
  go test -c -o /tmp/x.test "./$p" && printf '%-24s %s\n' "$p" "$(du -h /tmp/x.test | cut -f1)"
done
```
Expected: the leaf packages drop from ~47 MB to ~13-14 MB; `internal/sandbox/vm` (which reaches `msb`) stays ~54 MB.

- [ ] **Step 3: Run the full gate**

Run: `make check && make compile-all`
Expected: both pass. `make check` includes the dead-code gate, which fails if the wrapper was left dead.

- [ ] **Step 4: Measure end to end**

Run: `time make test` three times, warm cache.
Expected: a lower wall time than `guides/test-runtime-cost.md` records for the same machine; record the before/after in the PR body.

- [ ] **Step 5: Commit**

```bash
git add specs/test-isolation.md .operator-shared/
git commit -m "docs(isolation): contract for reachable seam families"
```

---

## Post-implementation notes

Filled in by the implementer when the work lands, so the next session sees what happened; delete this plan when done (its durable truth moves into `specs/test-isolation.md`).
