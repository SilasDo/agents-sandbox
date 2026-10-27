# Content-Addressed Runner-Image References Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Key the microsandbox runner-image cache by image content (`<slug>:<agent>.<contentid>`) instead of a constant name/reference, so a project's image variants coexist in msb and toggling `--docker` (or any other content change) reimports nothing after the first load of each variant.

**Architecture:** `naming` gains a content-addressed reference constructor and a `ContentID` field the parser round-trips, while the Docker tag stays a stable name. `image.EnsureImageWithClient` sets `Ref` from the built image's content id; `EnsureLoaded` reuses whichever of the content ref, the prefix-free name ref, or the legacy prefixed ref already holds the digest, else loads under the content ref. `pruning` retains a live project's references while a kept VM holds them or they were used within the prune age, and reclaims a retired project's outright.

**Tech Stack:** Go 1.26, microsandbox Go SDK v0.7.8, `stretchr/testify`, table-driven tests.

**Spec:** `.operator-shared/specs/image-ref-content-addressing.md` (issue [#322](https://github.com/inoio/agents-sandbox/issues/322)).

## Global Constraints

- Reference shape (exact): msb content ref `<slug>:<agent>.<contentid>`; `<contentid>` is `strings.TrimPrefix(dockerImageID, "sha256:")` truncated to its first **16** characters (fewer if the id is shorter). No `:` or `sha256:` may appear in `<contentid>`.
- Docker tag is unchanged: `agents-sandbox/runner-<slug>:<agent>-latest` (`naming.DockerImageRef`). `internal/sandbox/image/tags.go` and `vm/adopt.go` are untouched.
- `naming` stays dependency-free (only `strings`) — it must not import `image`, `state`, `msb`, or `options`.
- `naming.RunnerImageRef(slug, agent)` keeps its current value `<slug>:<agent>-latest`; it becomes the *name* (legacy/new-scheme) form, not the produced msb ref.
- Agent names are `[a-z0-9-]` (`opencode`, `opencode2`, `pi`, `claude-code`); `.` is the agent/content separator and is unambiguous.
- Every added/changed production line must be executed by a test (`specs/test-coverage.md`). Run `make fmt && make lint && make test` (and `make compile-all`) before a task is done.
- Use `make fmt`/`golangci-lint fmt` and `make lint`; never `go fmt`/`go vet`. The `exhaustruct_v5` linter will flag every `ArtifactInfo{...}` literal once the new field lands — update each.
- Worktree: `fix/image-content-refs`, based on `release/0.6`. The `git commit` steps apply there. After landing, forward-port to `main` (main already has the `keepImage` `-latest`-suffix simplification, so that line is a no-op there).
- Add a `CHANGELOG.md` `[Unreleased]` line; keep `docs/` in sync (`specs/first-run-guidance.md` / `STYLEGUIDE.md`).

## Review Focus

Inputs/conditions the spec implies that a reasonable person would expect to keep working; each is pinned by the task that owns it.

1. A project whose msb cache was written by 0.6.0 (prefix-free name ref `<slug>:<agent>-latest`) or by pre-0.6 (prefixed ref): both are reused when the content matches, so the upgrade reimports nothing. → Task 2.
2. A hyphenated agent (`claude-code`) with a content suffix parses back to agent `claude-code`, not `code` or `claude-code.<id>`. → Task 1.
3. A content id that is all-digits or a value that could be confused with the legacy `Digest` form: `<slug>:<agent>.<id>` always parses to `Agent`+`ContentID`, never to `Digest`. → Task 1.
4. A content ref held by a **stopped-but-kept** project VM is not removed by `image remove` or `image prune`. → Task 2 (in-use guard) / Task 3.
5. Repeated runs with identical content resolve to the same reference (determinism), so no spurious reimport; a variant untouched for longer than the prune age is reclaimed. → Task 2 / Task 3.

---

### Task 1: `naming` — content-addressed image reference and `ContentID` round-trip

**Files:**
- Modify: `internal/sandbox/naming/artifact.go`
- Test: `internal/sandbox/naming/artifact_test.go`, `internal/sandbox/naming/naming_test.go`, `internal/sandbox/naming/fuzz_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (package `naming`):
  - `ArtifactInfo` gains field `ContentID string`.
  - `ContentImageRef(slug, agent, contentID string) string` → `slug + ":" + agent + "." + contentID`.
  - `ParseImageTag(name string) ArtifactInfo`:
    - `<slug>:<agent>.<contentid>` (and the prefixed `agents-sandbox/runner-` form) → `{Slug, Agent, ContentID}` with `Digest` empty.
    - `<slug>:<agent>-latest` → `{Slug, Agent}` (unchanged).
    - any other tag → `{Slug, Digest: tag}` (unchanged).
  - `RunnerImageRef(slug, agent string) string` unchanged (name form).

- [ ] **Step 1: Write the failing tests**

Add to `artifact_test.go`:

```go
func TestContentImageRefRoundTrips(t *testing.T) {
	ref := ContentImageRef("myproj-1abc1234defg5", "claude-code", "9f2c1a7b4d6e0f31")
	require.Equal(t, "myproj-1abc1234defg5:claude-code.9f2c1a7b4d6e0f31", ref)
	require.Equal(t, ArtifactInfo{Slug: "myproj-1abc1234defg5", Agent: "claude-code", ContentID: "9f2c1a7b4d6e0f31"},
		ParseImageTag(ref))
}

func TestParseImageTagContentVsNameVsDigest(t *testing.T) {
	cases := []struct{ ref string; want ArtifactInfo }{
		{"myproj-1abc1234defg5:opencode-latest",
			ArtifactInfo{Slug: "myproj-1abc1234defg5", Agent: "opencode"}},
		{"myproj-1abc1234defg5:opencode.9f2c1a7b4d6e0f31",
			ArtifactInfo{Slug: "myproj-1abc1234defg5", Agent: "opencode", ContentID: "9f2c1a7b4d6e0f31"}},
		{"myproj-1abc1234defg5:opencode.0000000000000000",
			ArtifactInfo{Slug: "myproj-1abc1234defg5", Agent: "opencode", ContentID: "0000000000000000"}},
		{"agents-sandbox/runner-myproj-1abc1234defg5:opencode.9f2c1a7b4d6e0f31",
			ArtifactInfo{Slug: "myproj-1abc1234defg5", Agent: "opencode", ContentID: "9f2c1a7b4d6e0f31"}},
		{"myproj-1abc1234defg5:xYz1234AbCdEfGh",
			ArtifactInfo{Slug: "myproj-1abc1234defg5", Digest: "xYz1234AbCdEfGh"}},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, ParseImageTag(tc.ref), tc.ref)
	}
}
```

Extend `fuzz_test.go` with a round-trip fuzz for the content form:

```go
func FuzzContentImageRefRoundTrip(f *testing.F) {
	f.Add("myproject", "opencode", "9f2c1a7b4d6e0f31")
	f.Add("x", "claude-code", "0000000000000000")
	f.Fuzz(func(t *testing.T, slug, agent, contentID string) {
		if !validArtifactPart(slug) || !validArtifactPart(agent) || contentID == "" || !validContentID(contentID) {
			t.Skip()
		}
		info := ParseImageTag(ContentImageRef(slug, agent, contentID))
		if info.Slug != slug || info.Agent != agent || info.ContentID != contentID {
			t.Fatalf("ParseImageTag(ContentImageRef(%q,%q,%q)) = %+v", slug, agent, contentID, info)
		}
	})
}
```

`validContentID` is a test helper allowing lowercase hex only (`[0-9a-f]`, 1..16 chars); add it next to `validArtifactPart`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/naming/...`
Expected: FAIL / compile error (`ContentImageRef` undefined, `ContentID` field missing).

- [ ] **Step 3: Implement**

Add `ContentID string` to `ArtifactInfo`. Add `ContentImageRef`. In `parseImageRemainder`, after the `-latest` `CutSuffix` attempt and before the `Digest` fallback, split the tag on the first `.`:

```go
if agent, contentID, ok := strings.Cut(tag, "."); ok && contentID != "" {
	return ArtifactInfo{Slug: slug, Agent: agent, ContentID: contentID, Digest: "", Task: false}
}
```

Update every `ArtifactInfo{...}` literal in `artifact.go` to include `ContentID` (explicit value in the new branch, `""` elsewhere). Run `make lint` and add `ContentID` to the literals it flags in other packages' tests.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/naming/... && go test ./internal/sandbox/naming/... -run Fuzz -fuzz FuzzContentImageRefRoundTrip -fuzztime 10s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/naming
git commit -m "feat(naming): add content-addressed runner image reference and ContentID parse"
```

---

### Task 2: `image` — content ref, load-candidate set, in-use guard

**Files:**
- Modify: `internal/sandbox/image/build.go` (`ImageInfo`, `EnsureImageWithClient`, `EnsureLoaded`)
- Test: `internal/sandbox/image/image_test.go`, `internal/sandbox/image/refs_test.go`, `internal/sandbox/image/references_test.go`

**Interfaces:**
- Consumes: Task 1's `naming.ContentImageRef`, `naming.ParseImageTag` (`ContentID`), `naming.RunnerImageRef`.
- Produces (package `image`):
  - `imageContentID(imageID string) string` — `strings.TrimPrefix(imageID, "sha256:")` truncated to 16 chars.
  - `nameRefFromDockerTag(dockerTag string) string` — `naming.RunnerImageRef(info.Slug, info.Agent)` from `naming.ParseImageTag(dockerTag)` when both are non-empty, else `""`.
  - `imageRefCandidates(info ImageInfo) []string` — non-empty, de-duplicated, ordered `[info.Ref, nameRefFromDockerTag(info.DockerTag), info.DockerTag]`.
  - `EnsureImageWithClient` sets `Ref = naming.ContentImageRef(projectSlug, a.Name(), imageContentID(imageDigest))`, falling back to `naming.RunnerImageRef(projectSlug, a.Name())` when the content id is empty; `DockerTag` unchanged.
  - `EnsureLoaded` reuses the first candidate whose cache content matches (`cachedImageMatches`), setting `info.Ref` to it; else loads under `info.Ref` (or `info.DockerTag` when empty).

- [ ] **Step 1: Write the failing tests**

```go
func TestImageContentIDTrimsAndTruncates(t *testing.T) {
	require.Equal(t, "9f2c1a7b4d6e0f31",
		imageContentID("sha256:9f2c1a7b4d6e0f3199999999999999999999999999999999999999999999999999"))
	require.Equal(t, "", imageContentID(""))
}

func TestEnsureImageWithClientSetsContentRef(t *testing.T) {
	// Docker ImageInspect returns ID "sha256:9f2c1a7b4d6e0f31…".
	info, err := EnsureImageWithClient(ctx, a, projectDockerfile, "myproj-1abc1234defg5", BuildOptions{}, ui)
	require.NoError(t, err)
	require.Equal(t, "myproj-1abc1234defg5:opencode.9f2c1a7b4d6e0f31", info.Ref)
	require.Equal(t, naming.DockerImageRef("myproj-1abc1234defg5", "opencode"), info.DockerTag)
}

func TestEnsureLoadedReusesContentRef(t *testing.T) {
	// ImageGet(contentRef)=ok, ImageInspect(contentRef).Digest == info.Digest.
	// Expect no ImageSave/ImageLoad and info.Ref == contentRef.
}

func TestEnsureLoadedReusesNameRefForMigration(t *testing.T) {
	// 0.6.0 cache: ImageGet(contentRef)=not-found, ImageGet(<slug>:<agent>-latest)=ok
	// with matching digest. Expect no load and info.Ref == "<slug>:<agent>-latest".
}

func TestEnsureLoadedReusesLegacyPrefixedRefForMigration(t *testing.T) {
	// ImageGet(contentRef)=not-found, ImageGet(nameRef)=not-found,
	// ImageGet(DockerTag)=ok with matching digest. Expect no load, info.Ref == DockerTag.
}

func TestEnsureLoadedLoadsUnderContentRefWhenNoCandidateMatches(t *testing.T) {
	// Every candidate absent. Expect ImageLoad(contentRef, …).
}

func TestInUseImageReferencesProtectsContentRefHeldByKeptSandbox(t *testing.T) {
	// One kept sandbox with Config().Image == "<slug>:<agent>.<id>".
	// Expect InUseImageReferences to map that ref to the sandbox's key.
}
```

Also extend the existing `TestEnsureLoadedDecision` to assert `EnsureLoaded` still never inspects the host Docker client (the reference is `docker.Get()`; the test's msb-only mocks stay).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/image/...`
Expected: FAIL / compile error (`imageContentID` undefined) and assertions on the old `Ref` value.

- [ ] **Step 3: Implement**

Add the two helpers and `imageRefCandidates`. In `EnsureImageWithClient`, replace `Ref: naming.RunnerImageRef(projectSlug, a.Name())` with the content ref (fall back to the name ref when `imageContentID(imageDigest) == ""`). In `EnsureLoaded`, replace the `legacyRef := info.DockerTag` special case with a loop over `imageRefCandidates(info)`: for each candidate, if `mclient.ImageGet(ctx, candidate) == nil && cachedImageMatches(ctx, mclient, candidate, info)` then set `info.Ref = candidate` and return; if none match, `loadReason = "cached content differs from the Docker image"` when at least one candidate existed but mismatched, else `"not present in microsandbox cache"`, and load under `info.Ref` (or `info.DockerTag` when empty). Keep `cachedImageMatches` unchanged.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/image/... ./internal/sandbox/vm/... ./internal/sandbox/volume/...`
Expected: PASS (fix any test fixture that asserted the old name ref as `Ref`).

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image
git commit -m "feat(image): content-address the msb runner ref and widen the load candidates"
```

---

### Task 3: `pruning` — retain live variants by liveness and age

**Files:**
- Modify: `internal/sandbox/pruning/images.go`, `internal/sandbox/pruning/state.go`
- Test: `internal/sandbox/pruning/images_test.go`, `internal/sandbox/pruning/state_test.go`

**Interfaces:**
- Consumes: Task 1's `naming.ParseImageTag` (`Agent`, `ContentID`), `msb.ImageHandle.LastUsedAt`/`CreatedAt`.
- Produces (package `pruning`):
  - `PruneState` gains field `Age time.Duration`; `buildPruneState` sets `result.Age = age`.
  - `imageWithinAge(h msb.ImageHandle, age time.Duration) bool` — true iff `age > 0` and the image's `LastUsedAt()` (falling back to `CreatedAt()` when zero) is within `age`.
  - `keepImage(...) bool` rule: `keptImageRefs[ref]` → keep; else parse `(slug, agent)`, both live (`ToKeep`, not `ToPrune`) and `agent != ""` and `imageWithinAge` → keep; else drop. The `strings.HasSuffix(ref, "-latest")` test is removed (drop the now-unused `strings` import if it becomes unused).

- [ ] **Step 1: Write the failing tests**

```go
func TestKeepImageKeepsRefHeldByKeptSandboxRegardlessOfAge(t *testing.T) {
	// pruneState.Age = 1h; a content ref held in keptImageRefs, LastUsedAt 48h ago.
	// Expect keepImage == true.
}

func TestKeepImageKeepsRecentVariantOfLiveKey(t *testing.T) {
	// live key; content ref created 1m ago; Age = 1h. Expect true.
}

func TestKeepImageDropsAgedVariantOfLiveKey(t *testing.T) {
	// live key; content ref LastUsedAt 48h ago; Age = 24h. Expect false.
}

func TestKeepImageDropsRefsOfDeadKey(t *testing.T) {
	// key absent from ToKeep and ToPrune (dangling). Expect false even when fresh.
}

func TestKeepImageHasNoRecencyProtectionWhenAgeZero(t *testing.T) {
	// live key; fresh ref; Age = 0; not in keptImageRefs. Expect false.
}

func TestPruneImagesDryRunReportsAgedVariants(t *testing.T) {
	// two content refs for a live key + its kept-VM ref; age prunes the older
	// variant; dry-run increments MSBImagesPruned and lists it in Details without
	// calling ImageRemove.
}

func TestBuildPruneStateRecordsAge(t *testing.T) {
	// buildPruneState(ctx, 24h).Age == 24h.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/pruning/...`
Expected: FAIL / compile error (`PruneState.Age` undefined, `imageWithinAge` undefined).

- [ ] **Step 3: Implement**

Add `Age` to `PruneState` and set it in `buildPruneState`. Add `imageWithinAge`. Rewrite `keepImage` per the rule above. In `PruneImages`, the `StaleEntry.Digest` for a removed content ref should carry the parsed `ContentID` when `Digest` is empty (so the report names which variant went).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/pruning/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/pruning
git commit -m "feat(pruning): retain a live project's image variants by liveness and age"
```

---

### Task 4: Docs, CHANGELOG, spec amendments, full check

**Files:**
- Modify: `CHANGELOG.md`, `.operator-shared/specs/artifact-naming.md`, `.operator-shared/specs/runner-image-build.md`, `docs/runner-image.md`, `docs/commands.md`, `docs/how-it-works.md`
- Test: none (documentation); `ci/check-docs.sh` enforces links.

**Interfaces:**
- Consumes: the shapes and rules fixed in Tasks 1–3.
- Produces: nothing new.

- [ ] **Step 1: Amend the affected specs**

- `artifact-naming.md`: state that the produced msb runner ref is now content-addressed (`<slug>:<agent>.<contentid>`); `<slug>:<agent>-latest` becomes the *name/legacy* form (still read). Keep the Docker prefix unchanged.
- `runner-image-build.md`: in "Load decision", replace the single legacy fallback with the candidate set (content ref, prefix-free name ref derived from the Docker tag, prefixed Docker-tag ref) and note that a candidate is reused only on a content match; change the retained-reference rule to "a live project's refs held by a kept VM, or used within the prune age".

- [ ] **Step 2: Update the user docs**

- `docs/runner-image.md`: the reference block (lines ~52–55) gains a third line for the content ref and explains that each distinct content is imported once and variants coexist; the "Content-verified load" section (lines ~85–94) lists the three candidates; "Pruning images" (lines ~96–118) states a live project's recent variants are retained (by VM use or the age window) and a retired project's are reclaimed; the "same runner-image tag" sentence at ~129 now says the new content ref is loaded without removing the old cached manifest.
- `docs/commands.md`: the `image remove` accepted-reference wording now covers the content ref alongside the prefix-free and prefixed refs.
- `docs/how-it-works.md`: the runner-image reference sentence matches the new shape.

- [ ] **Step 3: Add the CHANGELOG line**

Add under `[Unreleased]` a Fix entry: image variants no longer overwrite each other in the microsandbox cache, so toggling `--docker` (or any other content change) reimports nothing after the first load; pruning retains a live project's recent variants and reclaims the rest (issue #322).

- [ ] **Step 4: Run the full check**

Run: `make fmt && make lint && make test && make compile-all && ci/check-docs.sh`
Expected: all green. (`make compile-all` covers the tagged integration binaries touched by `ImageInfo`/`Ref` changes.)

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md docs .operator-shared
git commit -m "docs: record content-addressed runner image references"
```

---

## Self-review notes

- **Spec coverage:** reference shape + content id (Task 1/2); stable Docker tag (Task 2, unchanged); load decision + three candidates + name-ref derivation (Task 2); parse/ownership round-trip (Task 1, plus the in-use-guard test in Task 2); retention by liveness + age (Task 3); spec amendments and docs/CHANGELOG (Task 4).
- **Deliberate non-changes:** `state.ImageReferences` keeps protecting the name and prefixed refs; content refs are protected by the live-sandbox scan (`InUseImageReferences` unions `config.Image`). No `ImageInfo` field is added — the name ref is derived from `DockerTag`.
- **Left to the builder:** the exact `LastUsedAt`/`CreatedAt` fallback wording and the `imageWithinAge` boundary (`<` vs `<=`); the content-id length (16 pinned) and the report detail field. `LastUsedAt` is msb's "last referenced by a sandbox" (`sdk/go@v0.7.8/image.go`), which is the intended reuse-recency signal; the unit tests pin the logic with the mock, not msb's field semantics.
- **Known remaining cost:** the Docker rebuild and the VM recreate on a variant switch are unchanged (out of scope per the spec).
- **Execution method:** not yet chosen — ask before starting.
