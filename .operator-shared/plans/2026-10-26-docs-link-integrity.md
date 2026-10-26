# Documentation Link Integrity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `ci/check-docs.sh` fail when a documentation fragment landing on the published site matches no heading, and route the launcher's doc links through a leaf package so the checker can extract them.

**Architecture:** A dependency-free `internal/docslink` package composes the launcher's site links as `SectionURL(page, anchor)`. `ci/check-docs.sh` gains a kramdown `basic_generate_id` mirror plus fragment resolution for four sources: rendered-HTML links, `docs/` in-page anchors, `docs/` `{% link page.md %}#anchor` cross-page anchors, and Go source `docslink.SectionURL(...)` / literal-URL links. No Jekyll build in `make check`.

**Tech Stack:** Bash (the checker + its fixture harness), Go (leaf package, doctor, image build), awk (the slug mirror).

**Spec:** `.operator-shared/specs/docs-link-integrity.md`

## Global Constraints

- Checker stays Ruby-free: `make check` must not run Jekyll.
- The published anchors are unchanged: `microsandbox-runtime`, `kvm-unavailable`, `docker-not-running`, `not-apple-silicon`, `the-base-image-cannot-be-found`.
- Slug mirror = kramdown 2.5.2 `basic_generate_id` on the heading's raw source text: strip leading `[^a-zA-Z]+`, delete everything outside `[A-Za-z0-9 -]`, spaces→`-`, lowercase, empty→`section`, repeated slug→`-1`/`-2`/… .
- Go module path is `github.com/inoio/agents-sandbox`.
- `make fmt` / `make lint` only — never `go fmt` / `go vet`. Run the linter after at most 3 edits.
- The checker scans Go **test** sources too, so any `docslink.SectionURL(page, anchor)` written in a test must use a real page/anchor pair.

## Review Focus

- A heading with inline markdown, backticks, or punctuation — the mirror must slug the raw source text, matching kramdown (`## \`agents-sandbox doctor\` fails` → `agents-sandbox-doctor-fails`). Owning task: Task 3.
- A `#` line inside a fenced code block (e.g. a shell comment) — must not be taken for a heading. Owning task: Task 3.
- Duplicate headings on a page — the second gets `-1`, the third `-2`. Owning task: Task 3.
- A `SectionURL` whose page is not `troubleshooting` — extraction must honour the page argument, not assume the doctor page. Owning task: Task 5.
- A `{% link %}` fragment on a non-`.md` target (a generated diagram SVG) — must not be slug-checked. Owning task: Task 4.

---

### Task 1: `internal/docslink` leaf package

**Files:**
- Create: `internal/docslink/docslink.go`
- Test: `internal/docslink/docslink_test.go`
- Create: `internal/docslink/main_test.go` — required by `specs/test-isolation.md`: every test package installs the fail-fast mocks via `TestMain`.

**Interfaces:**
- Produces: `docslink.BaseURL` (`const string`), `docslink.SectionURL(page, anchor string) string`.

- [ ] **Step 1: Write the failing test**

```go
package docslink

import "testing"

func TestSectionURLComposesPageAndAnchor(t *testing.T) {
	got := SectionURL("troubleshooting", "docker-not-running")
	want := BaseURL + "/troubleshooting.html#docker-not-running"
	if got != want {
		t.Errorf("SectionURL() = %q, want %q", got, want)
	}
}

func TestSectionURLUsesTheGivenPage(t *testing.T) {
	got := SectionURL("runner-image", "the-base-image-contract")
	want := BaseURL + "/runner-image.html#the-base-image-contract"
	if got != want {
		t.Errorf("SectionURL() = %q, want %q", got, want)
	}
}
```

Both anchors are real headings, so `ci/check-docs.sh` stays green once Task 5 lands.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/docslink/ -v`
Expected: FAIL — package `docslink` / `SectionURL` undefined.

- [ ] **Step 3: Implement the package**

`internal/docslink/docslink.go`:

```go
// Package docslink composes links to the published documentation site.
package docslink

// BaseURL is the root of the published documentation site.
const BaseURL = "https://inoio.github.io/agents-sandbox"

// SectionURL returns the URL of a heading on a published docs page. page is the
// source page name without its .md suffix (e.g. "troubleshooting").
func SectionURL(page, anchor string) string {
	return BaseURL + "/" + page + ".html#" + anchor
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/docslink/ -v`
Expected: PASS.

- [ ] **Step 5: Add the fail-fast `TestMain`**

Mirror `internal/humanize/main_test.go` (the minimal leaf precedent) in `internal/docslink/main_test.go`:

```go
package docslink

import (
	"testing"

	termio "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	termio.InitFailFastMocks(m)
}
```

Then run `go test ./internal/testutil/ ./internal/docslink/` — the isolation-contract test must accept the new package.

- [ ] **Step 6: Lint and commit**

```bash
make lint
git add internal/docslink/docslink.go internal/docslink/docslink_test.go internal/docslink/main_test.go
git commit -m "feat(docslink): add leaf package composing docs site links"
```

---

### Task 2: Route the launcher's doc links through `docslink`

**Files:**
- Modify: `internal/sandbox/doctor/doctor.go:21-28,96`
- Modify: `internal/sandbox/doctor/doctor_linux.go:37,45`
- Modify: `internal/sandbox/doctor/doctor_darwin.go:24,32`
- Modify: `internal/sandbox/image/build.go:237-256`
- Modify: `internal/docslink/main_test.go` — `doctor` now imports `docslink`, so `testutil/mocks` (which imports `doctor`) cycles; install the seam families directly, mirroring `internal/notify/main_test.go` (see `specs/test-isolation.md`).
- Modify: `.operator-shared/specs/test-isolation.md` — add `docslink` to the direct-install list with its reason.

**Interfaces:**
- Consumes: `docslink.SectionURL(page, anchor string) string` from Task 1.

- [ ] **Step 1: Rewire `doctor.go`**

Delete the `docsBaseURL` const and the `troubleshootingURL` function; import `"github.com/inoio/agents-sandbox/internal/docslink"`; replace the `checkMsb` call site with `docslink.SectionURL("troubleshooting", "microsandbox-runtime")`.

- [ ] **Step 2: Rewire `doctor_linux.go` and `doctor_darwin.go`**

Replace `troubleshootingURL("kvm-unavailable")`, `troubleshootingURL("docker-not-running")`, `troubleshootingURL("not-apple-silicon")` with `docslink.SectionURL("troubleshooting", "<anchor>")`; add the `docslink` import.

- [ ] **Step 3: Rewire `image/build.go`**

Delete the `docsBaseURL` const, update the comment above it, and replace `docsBaseURL+"/troubleshooting.html#the-base-image-cannot-be-found"` with `docslink.SectionURL("troubleshooting", "the-base-image-cannot-be-found")`; add the `docslink` import.

- [ ] **Step 4: Break the new test-import cycle**

`doctor` now imports `docslink`, and `testutil/mocks` imports `doctor`, so `internal/docslink/main_test.go` can no longer call `termio.InitFailFastMocks(m)`. Mirror `internal/notify/main_test.go`: install the families the wrapper installs except `doctor`, and update `specs/test-isolation.md`'s direct-install list with the `docslink` entry and its reason.

- [ ] **Step 5: Verify no stragglers and the existing tests still hold**

Run: `rg 'docsBaseURL|troubleshootingURL' internal`
Expected: no matches.

Run: `go test ./internal/sandbox/doctor/... ./internal/sandbox/image/...`
Expected: PASS — the tests assert the `troubleshooting.html#<anchor>` fragments, which are unchanged.

Run: `go test ./internal/testutil/ ./internal/docslink/`
Expected: PASS — the isolation-contract test accepts `docslink`'s direct-install `TestMain`.

- [ ] **Step 6: Lint and commit**

```bash
make lint
git add internal/sandbox/doctor internal/sandbox/image internal/docslink/main_test.go .operator-shared/specs/test-isolation.md
git commit -m "refactor: compose doctor and build-error docs links via docslink"
```

---

### Task 3: Checker slug machinery + rendered-HTML fragments

**Files:**
- Modify: `ci/check-docs.sh` (add helpers near `link_targets`; rewrite the `root` `"$SITE_BASE"/*` case in `handle_target:126-141`)
- Modify: `ci/test-check-docs.sh` (rewrite the `root anchored html link` case at `:95-103`)

**Interfaces:**
- Produces (for Tasks 4–5): `page_slugs FILE` (prints one kramdown id per heading, in order), `check_fragment REL LINENO PAGE_FILE ANCHOR LABEL`.

- [ ] **Step 1: Write the failing tests**

In `ci/test-check-docs.sh`, replace the `root anchored html link` block (`:95-98`) with (the separate `root anchored broken html link` case at `:100-103` stays as-is):

```bash
d="$(new_fixture)"
printf '\n[Install](https://inoio.github.io/agents-sandbox/install.html#install)\n' >>"$d/README.md"
expect "root anchored html link resolves" 0 "all internal links are valid" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n[Install](https://inoio.github.io/agents-sandbox/install.html#the-base-image)\n' >>"$d/README.md"
expect "root anchored html link dead fragment" 1 "fragment 'the-base-image' matches no heading in docs/install.md" "$d"
rm -rf "$d"
```

- [ ] **Step 2: Run to verify they fail**

Run: `ci/test-check-docs.sh`
Expected: `root anchored html link dead fragment` FAILS (exit 0, wanted 1).

- [ ] **Step 3: Add the slug helpers**

Add to `ci/check-docs.sh`:

```bash
# page_slugs FILE: print the id kramdown generates for every heading in FILE, in
# document order. Mirrors kramdown's basic_generate_id on the heading's raw
# source text (inline markdown included), with its -1/-2 uniqueness suffixes.
page_slugs() {
  awk '
    function slug(s,    i, c, out) {
      sub(/^[^a-zA-Z]+/, "", s)
      out = ""
      for (i = 1; i <= length(s); i++) {
        c = substr(s, i, 1)
        if (c ~ /[A-Za-z0-9 -]/) out = out c
      }
      gsub(/ /, "-", out)
      out = tolower(out)
      if (out == "") out = "section"
      return out
    }
    BEGIN { fence = 0; frontmatter = 0; line = 0; delete used }
    {
      line++
      body = $0
      sub(/^[[:space:]]*(>[[:space:]]*)*/, "", body)
      if (line == 1 && body ~ /^---[[:space:]]*$/) { frontmatter = 1; next }
      if (frontmatter) { if (body ~ /^---[[:space:]]*$/) frontmatter = 0; next }
      if (fence == 0) {
        if (body ~ /^(```|~~~)/) {
          marker = substr(body, 1, 1); openlen = 0
          while (substr(body, openlen + 1, 1) == marker) openlen++
          if (!(marker == "`" && index(substr(body, openlen + 1), "`") > 0)) { fence = 1; next }
        }
      } else {
        trimmed = body; sub(/[[:space:]]+$/, "", trimmed)
        if (trimmed ~ /^(`+|~+)$/ && substr(trimmed, 1, 1) == marker && length(trimmed) >= openlen) fence = 0
        next
      }
      if (body ~ /^#{1,6}[ \t]/) {
        t = body
        sub(/^#{1,6}[ \t]+/, "", t)
        sub(/[ \t]+#+[ \t]*$/, "", t)          # trailing ATX hashes
        sub(/[ \t]*\{#[^}]*\}[ \t]*$/, "", t)  # declared id, if any
        s = slug(t)
        if (s in used) { used[s]++; s = s "-" used[s] } else used[s] = 0
        print s
      }
    }' "$1"
}

# check_fragment REL LINENO PAGE_FILE ANCHOR LABEL: report unless ANCHOR (bare,
# no leading #) is a heading id in PAGE_FILE.
check_fragment() {
  local rel="$1" lineno="$2" page_file="$3" anchor="$4" label="$5"
  [[ -z "$anchor" ]] && return 0
  if page_slugs "$page_file" | grep -qxF "$anchor"; then
    return 0
  fi
  report "$rel:$lineno: fragment '$anchor' matches no heading in $label"
}
```

- [ ] **Step 4: Resolve the root rendered-HTML fragment**

Rewrite the `"$SITE_BASE"/*` case in `handle_target` to capture the fragment before the existing page mapping, then call `check_fragment` after the page is confirmed to exist:

```bash
"$SITE_BASE"/*)
  path="${target#"$SITE_BASE"/}"
  fragment=""
  [[ "$path" == *"#"* ]] && { fragment="${path#*#}"; path="${path%%#*}"; }
  if [[ -z "$path" ]]; then
    [[ -f "$DOCS/index.md" ]] || report "$rel:$lineno: docs html link '$target' has no source index page"
    check_fragment "$rel" "$lineno" "$DOCS/index.md" "$fragment" "docs/index.md"
  elif [[ "$path" == */ ]]; then
    if [[ -f "$DOCS/${path}index.md" ]]; then
      check_fragment "$rel" "$lineno" "$DOCS/${path}index.md" "$fragment" "docs/${path}index.md"
    else
      report "$rel:$lineno: docs html link '$path' has no source index page"
    fi
  else
    path="${path%.html}.md"
    if [[ -f "$DOCS/$path" ]]; then
      check_fragment "$rel" "$lineno" "$DOCS/$path" "$fragment" "docs/$path"
    else
      report "$rel:$lineno: docs html link '$path' does not exist"
    fi
  fi
  ;;
```

- [ ] **Step 5: Run the checker tests and the real tree**

Run: `ci/test-check-docs.sh`
Expected: all `ok`, `N passed, 0 failed`.

Run: `ci/check-docs.sh`
Expected: `docs: all internal links are valid` (the real `CHANGELOG.md` `…troubleshooting.html#the-base-image-cannot-be-found` link resolves).

- [ ] **Step 6: Add the code-block and duplicate-heading slug tests**

Append to `ci/test-check-docs.sh`:

```bash
# A '#' line inside a fenced code block is not a heading.
d="$(new_fixture)"
cat >>"$d/docs/install.md" <<'EOF'

```console
# not a heading
```
EOF
printf '\n[Bad](https://inoio.github.io/agents-sandbox/install.html#not-a-heading)\n' >>"$d/README.md"
expect "fenced-hash not a heading" 1 "fragment 'not-a-heading' matches no heading in docs/install.md" "$d"
rm -rf "$d"

# A backticked heading slugs its raw source text.
d="$(new_fixture)"
printf '\n## `agents-sandbox doctor` fails\n' >>"$d/docs/install.md"
printf '\n[Doctor](https://inoio.github.io/agents-sandbox/install.html#agents-sandbox-doctor-fails)\n' >>"$d/README.md"
expect "backticked heading slug" 0 "all internal links are valid" "$d"
rm -rf "$d"

# A repeated heading gets kramdown's -1 suffix.
d="$(new_fixture)"
printf '\n## Foo\n\n## Foo\n' >>"$d/docs/install.md"
printf '\n[Second](https://inoio.github.io/agents-sandbox/install.html#foo-1)\n' >>"$d/README.md"
expect "duplicate heading suffix" 0 "all internal links are valid" "$d"
rm -rf "$d"
```

- [ ] **Step 7: Run, lint-format, commit**

Run: `ci/test-check-docs.sh && ci/check-docs.sh`
Expected: all pass.

```bash
git add ci/check-docs.sh ci/test-check-docs.sh
git commit -m "test(docs): resolve rendered-HTML link fragments against headings"
```

---

### Task 4: `docs/` in-page and cross-page Jekyll fragments

**Files:**
- Modify: `ci/check-docs.sh` (`emit_link:86-93`, `handle_target` `root` + `docs` cases, `check_jekyll_link:104-118`)
- Modify: `ci/test-check-docs.sh` (`new_fixture:28-35`; add cases)

**Interfaces:**
- Consumes: `check_fragment` from Task 3.

- [ ] **Step 1: Write the failing tests**

Extend `new_fixture`'s `docs/introduction.md` with a resolvable in-page and cross-page link:

```bash
cat >"$dir/docs/introduction.md" <<'EOF'
# Introduction

See [Install]({% link install.md %}) and [Launcher]({% link configuration/launcher.md %}).

- [top](#introduction)
- [Install section]({% link install.md %}#install)
EOF
```

Append the failure cases:

```bash
d="$(new_fixture)"
printf '\n[Gone](#no-such-heading)\n' >>"$d/docs/introduction.md"
expect "docs in-page dead fragment" 1 "fragment 'no-such-heading' matches no heading in docs/introduction.md" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n[Gone]({%% link install.md %%}#no-such-heading)\n' >>"$d/docs/introduction.md"
expect "docs cross-page dead fragment" 1 "fragment 'no-such-heading' matches no heading in docs/install.md" "$d"
rm -rf "$d"

# A fragment on a non-.md Jekyll target (a generated diagram) is not checked.
d="$(new_fixture)"
printf '\n![diagram]({%% link diagrams/arch.svg %%}#whatever)\n' >>"$d/docs/introduction.md"
expect "diagram fragment ignored" 0 "all internal links are valid" "$d"
rm -rf "$d"
```

- [ ] **Step 2: Run to verify they fail**

Run: `ci/test-check-docs.sh`
Expected: the two `dead fragment` cases FAIL.

- [ ] **Step 3: Stop dropping in-page anchors in `emit_link`**

Remove `\#*` from the drop case in `emit_link` so `#…` targets are emitted:

```bash
  case "$target" in
    "$SITE_BASE"/*) ;; # rendered docs html link - keep (validated by the root handler)
    http://* | https://* | mailto:* | tel:* | ftp://*) return ;;
  esac
```

- [ ] **Step 4: Handle in-page anchors per mode**

In the `root` case of `handle_target`, ignore same-file anchors (root files render on GitHub, a different slugger):

```bash
        '#'*) ;; # same-file anchor in a root file: GitHub-rendered, not kramdown
```

In the `docs` case, validate a same-file anchor:

```bash
        \#*) check_fragment "$rel" "$lineno" "$ROOT/$rel" "${target#\#}" "$rel" ;;
```

- [ ] **Step 5: Resolve a Jekyll tag's trailing fragment**

In `check_jekyll_link`, after extracting the path, capture any fragment after `%}` and check it only for `.md` targets:

```bash
check_jekyll_link() {
  local rel="$1" lineno="$2" base="$3" target="$4" path fragment
  path="$(extract_jekyll_path "$target")"
  fragment="${target#*%\}}"
  fragment="${fragment#\#}"
  if [[ "$path" == /* ]]; then
    report "$rel:$lineno: {% link %} path '$path' must be relative (no leading slash)"
    return
  fi
  if [[ "$path" == diagrams/*.svg ]]; then
    path="${path%.svg}.puml"
  fi
  if [[ ! -e "$base/$path" ]]; then
    report "$rel:$lineno: {% link %} target '$path' does not exist"
    return
  fi
  if [[ "$path" == *.md ]]; then
    check_fragment "$rel" "$lineno" "$base/$path" "$fragment" "docs/$path"
  fi
}
```

- [ ] **Step 6: Resolve rendered-HTML links in `docs/` pages too**

The spec scopes the gate to rendered-HTML links in *any* file, but the docs-mode `"$SITE_BASE"/*` arm ignores them. Extract the root arm's page mapping and fragment check into a shared function and call it from both modes.

Add near the other helpers:

```bash
# check_rendered_site_link REL LINENO TARGET: map a rendered docs HTML link back
# to its source page and resolve any fragment against that page's headings.
check_rendered_site_link() {
  local rel="$1" lineno="$2" target="$3" path fragment
  path="${target#"$SITE_BASE"/}"
  fragment=""
  [[ "$path" == *"#"* ]] && { fragment="${path#*#}"; path="${path%%#*}"; }
  if [[ -z "$path" ]]; then
    if [[ -f "$DOCS/index.md" ]]; then
      check_fragment "$rel" "$lineno" "$DOCS/index.md" "$fragment" "docs/index.md"
    else
      report "$rel:$lineno: docs html link '$target' has no source index page"
    fi
  elif [[ "$path" == */ ]]; then
    if [[ -f "$DOCS/${path}index.md" ]]; then
      check_fragment "$rel" "$lineno" "$DOCS/${path}index.md" "$fragment" "docs/${path}index.md"
    else
      report "$rel:$lineno: docs html link '$path' has no source index page"
    fi
  else
    path="${path%.html}.md"
    if [[ -f "$DOCS/$path" ]]; then
      check_fragment "$rel" "$lineno" "$DOCS/$path" "$fragment" "docs/$path"
    else
      report "$rel:$lineno: docs html link '$path' does not exist"
    fi
  fi
}
```

Replace the root arm's inline body with `"$SITE_BASE"/*) check_rendered_site_link "$rel" "$lineno" "$target" ;;` and the docs arm's `"$SITE_BASE"/*) ;;` with the same call. Update the `emit_link` comment on the `"$SITE_BASE"/*` case (drop "validated by the root handler" — it is validated by both handlers now) and the docs-arm comment.

Add the tests:

```bash
d="$(new_fixture)"
printf '\n[Rendered](https://inoio.github.io/agents-sandbox/install.html#no-such-heading)\n' >>"$d/docs/introduction.md"
expect "docs rendered html link dead fragment" 1 "fragment 'no-such-heading' matches no heading in docs/install.md" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n[Rendered](https://inoio.github.io/agents-sandbox/install.html#install)\n' >>"$d/docs/introduction.md"
expect "docs rendered html link resolves" 0 "all internal links are valid" "$d"
rm -rf "$d"
```

- [ ] **Step 7: Run, lint-format, commit**

Run: `ci/test-check-docs.sh && ci/check-docs.sh`
Expected: all pass (the real tree's 30+ in-page and 13 cross-page fragments resolve).

```bash
git add ci/check-docs.sh ci/test-check-docs.sh
git commit -m "test(docs): resolve docs in-page and cross-page link fragments"
```

---

### Task 5: Go doc-link extraction

**Files:**
- Modify: `ci/check-docs.sh` (add extractor + `check_go_files`; invoke before the final exit)
- Modify: `ci/test-check-docs.sh` (`new_fixture`; add cases)

**Interfaces:**
- Consumes: `check_fragment` from Task 3; `docslink` call form from Task 2.

- [ ] **Step 1: Write the failing tests**

Extend `new_fixture` with a valid Go doc link and its target page:

```bash
mkdir -p "$dir/internal/fixture"
cat >"$dir/internal/fixture/fixture.go" <<'EOF'
package fixture

import "github.com/inoio/agents-sandbox/internal/docslink"

var _ = docslink.SectionURL("troubleshooting", "docker-not-running")
EOF
cat >"$dir/docs/troubleshooting.md" <<'EOF'
# Troubleshooting

## Docker not running
EOF
```

Append the failure and literal cases:

```bash
d="$(new_fixture)"
sed -i 's/docker-not-running/dead-anchor/' "$d/internal/fixture/fixture.go"
expect "go docslink dead fragment" 1 "fragment 'dead-anchor' matches no heading in docs/troubleshooting.md" "$d"
rm -rf "$d"

d="$(new_fixture)"
cat >>"$d/internal/fixture/fixture.go" <<'EOF'

var _ = docslink.SectionURL("nope", "whatever")
EOF
expect "go docslink missing page" 1 "doc link points at missing page docs/nope.md" "$d"
rm -rf "$d"

d="$(new_fixture)"
cat >>"$d/internal/fixture/fixture.go" <<'EOF'

const literal = "https://inoio.github.io/agents-sandbox/troubleshooting.html#dead-anchor"
EOF
expect "go literal url dead fragment" 1 "fragment 'dead-anchor' matches no heading in docs/troubleshooting.md" "$d"
rm -rf "$d"
```

- [ ] **Step 2: Run to verify they fail**

Run: `ci/test-check-docs.sh`
Expected: the Go cases FAIL (exit 0, wanted 1).

- [ ] **Step 3: Add the Go extractor and walker**

```bash
# find_go_files: every Go source under cmd/ and internal/, tests included.
find_go_files() {
  local dir
  for dir in "$ROOT/cmd" "$ROOT/internal"; do
    [[ -d "$dir" ]] && find "$dir" -name '*.go'
  done | sort
}

# go_doc_targets FILE: emit "lineno<TAB>page<TAB>anchor" for each documentation
# link the launcher can print: a SectionURL call (package-qualified or not), or
# a literal site URL.
go_doc_targets() {
  local file="$1" line lineno
  local re_section='SectionURL\("([A-Za-z0-9/._-]+)",[[:space:]]*"([A-Za-z0-9._-]+)"\)'
  local re_literal='https://inoio\.github\.io/agents-sandbox/([A-Za-z0-9/._-]+)\.html#([A-Za-z0-9._-]+)'
  lineno=0
  while IFS= read -r line; do
    lineno=$((lineno + 1))
    if [[ "$line" =~ $re_section ]]; then
      printf '%s\t%s\t%s\n' "$lineno" "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
    fi
    if [[ "$line" =~ $re_literal ]]; then
      printf '%s\t%s\t%s\n' "$lineno" "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
    fi
  done <"$file"
}

# check_go_files: resolve every doc link in a Go source against its docs page.
check_go_files() {
  local file rel lineno page anchor page_file
  while IFS= read -r file; do
    rel="${file#"$ROOT"/}"
    while IFS=$'\t' read -r lineno page anchor; do
      page_file="$DOCS/$page.md"
      if [[ ! -f "$page_file" ]]; then
        report "$rel:$lineno: doc link points at missing page docs/$page.md"
      else
        check_fragment "$rel" "$lineno" "$page_file" "$anchor" "docs/$page.md"
      fi
    done < <(go_doc_targets "$file")
  done < <(find_go_files)
}
```

- [ ] **Step 4: Invoke the walker**

Add `check_go_files` alongside the other checks (after `check_files docs …`, before the terminology check):

```bash
# Go sources: the documentation links the launcher prints must resolve too.
check_go_files
```

- [ ] **Step 5: Run the checker tests and the real tree**

Run: `ci/test-check-docs.sh`
Expected: all pass.

Run: `ci/check-docs.sh`
Expected: `docs: all internal links are valid` — the rewired `doctor_*` and `build.go` calls (`troubleshooting` page) resolve, and `internal/docslink/docslink_test.go` uses real anchors.

- [ ] **Step 6: Add the non-`troubleshooting` page test**

```bash
d="$(new_fixture)"
cat >"$d/internal/fixture/fixture.go" <<'EOF'
package fixture

import "github.com/inoio/agents-sandbox/internal/docslink"

var _ = docslink.SectionURL("install", "install")
EOF
expect "go docslink non-troubleshooting page" 0 "all internal links are valid" "$d"
rm -rf "$d"

# The extractor matches SectionURL whether or not it is package-qualified, so an
# in-package unqualified call (as in internal/docslink/docslink_test.go) is
# checked too — otherwise the "test sources are checked" invariant is vacuous.
d="$(new_fixture)"
mkdir -p "$d/internal/docslink"
cat >"$d/internal/docslink/fixture.go" <<'EOF'
package docslink

var _ = SectionURL("troubleshooting", "dead-anchor")
EOF
expect "go unqualified SectionURL extracted" 1 "fragment 'dead-anchor' matches no heading in docs/troubleshooting.md" "$d"
rm -rf "$d"
```

- [ ] **Step 7: Commit**

```bash
git add ci/check-docs.sh ci/test-check-docs.sh
git commit -m "test(docs): resolve the launcher's printed doc-link fragments"
```

---

### Task 6: Docs, changelog, full check

**Files:**
- Modify: `.operator-shared/specs/first-run-guidance.md:22-27`
- Modify: `CHANGELOG.md:9-26`

- [ ] **Step 1: Update the doctor-link clause**

In `.operator-shared/specs/first-run-guidance.md`, change the `Docs:` line reference from "`troubleshootingURL` builds the link" to "`docslink.SectionURL` builds the link", and replace the coupling sentence:

> `troubleshootingURL` builds the link. The anchors are the troubleshooting headings, and the coupling is silent:
> renaming a heading breaks the doctor link with no test failing. Current pairs: `kvm-unavailable`, `docker-not-running`,
> `microsandbox-runtime`, `not-apple-silicon`.

with:

> `docslink.SectionURL("troubleshooting", …)` builds the link. The anchors are the troubleshooting headings, and
> `ci/check-docs.sh` fails when a printed fragment matches no heading (see `docs-link-integrity.md`). Current pairs:
> `kvm-unavailable`, `docker-not-running`, `microsandbox-runtime`, `not-apple-silicon`.

- [ ] **Step 2: Add the changelog entry**

Under `## [Unreleased]` → `### Changed`, add:

```markdown
- Docs integrity: `ci/check-docs.sh` now verifies that every documentation fragment landing on the published site —
  rendered-HTML links, `docs/` in-page and cross-page anchors, and the launcher's printed links — resolves to a
  heading that slugs to it. The launcher composes its links through the new `internal/docslink` package (#296).
```

- [ ] **Step 3: Confirm the slug mirror against a real Jekyll build**

The published ids are what the gate claims to verify, so confirm the mirror once against kramdown's actual output. The gem bundle is gitignored, so `bundle install` may be needed first; run in a checkout that has it (a sibling worktree with `docs/vendor/bundle` works).

```bash
cd docs && bundle exec jekyll build
grep -oE '<h[1-6] id="[^"]+"' _site/troubleshooting/index.html 2>/dev/null || grep -oE '<h[1-6] id="[^"]+"' ../docs/_site/troubleshooting.html
```

Compare the printed ids against `ci/check-docs.sh`-style `page_slugs docs/troubleshooting.md`. Expected: identical. If the environment cannot install the gems, record that the mirror was calibrated against every existing fragment instead.
Clean up: remove `docs/_site` if the build created it.

- [ ] **Step 4: Run the full check**

Run: `make check`
Expected: all targets pass, including `docs-linkcheck` and `docs-linkcheck-test`.

- [ ] **Step 5: Commit**

```bash
git add .operator-shared/specs/first-run-guidance.md CHANGELOG.md
git commit -m "docs: note the documentation fragment gate (#296)"
```
