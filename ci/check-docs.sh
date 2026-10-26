#!/usr/bin/env bash
# Validate internal documentation links.
#
# - Root-level Markdown files (README.md, ...) must link to documentation pages
#   with rendered HTML links (https://inoio.github.io/agents-sandbox/...).
#   Non-docs relative links must resolve to an existing repo file.
# - Pages under docs/ link to each other with Jekyll {% link %} tags that must
#   resolve relative to docs/ (generated diagrams/*.svg resolve to the .puml
#   source, since the SVGs are built by PlantUML and gitignored).
#
# Exit status is non-zero when any violation is found.
#
# Usage: ci/check-docs.sh [ROOT]
#   ROOT defaults to the repository root; the test harness passes a fixture root.

set -euo pipefail

ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
DOCS="$ROOT/docs"
SITE_BASE="https://inoio.github.io/agents-sandbox"

violations=0

report() {
  printf 'docs: %s\n' "$1"
  violations=$((violations + 1))
}

# Emit "lineno<TAB>target" for every markdown link in a file, resolving
# reference-style links ([text][id], [id], and definitions "[id]: url") and
# stripping titles and angle-bracket wrappers from inline links. Rendered docs
# HTML links and in-page anchors are kept (validated per file role); other
# external links are skipped.
link_targets() {
  local file="$1" line lineno rest match target id
  local -A refs=()
  local re_def re_inline re_ref
  re_def='^[[:space:]]*\[([^]]+)\]:[[:space:]]*([^#[:space:]]+)'
  re_inline='\[[^]]*\]\(([^()]*\([^()]*\)[^()]*|[^()]*|<[^>]*>)\)'
  re_ref='\[([^]]*)\]\[([^]]*)\]|\[([^]]*)\]'

  # First pass: collect reference definitions "[id]: url".
  while IFS= read -r line; do
    if [[ "$line" =~ $re_def ]]; then
      refs["${BASH_REMATCH[1]}"]="${BASH_REMATCH[2]}"
    fi
  done <"$file"

  lineno=0
  while IFS= read -r line; do
    lineno=$((lineno + 1))
    rest="$line"

    # Inline links [text](target) / [text](target "title"). The target may be
    # wrapped in <...> to permit parentheses (e.g. foo(1).md).
    while [[ "$rest" =~ $re_inline ]]; do
      target="${BASH_REMATCH[1]}"
      if [[ "$target" == \<*\> ]]; then
        target="${target#<}"
        target="${target%>}"
      elif [[ "$target" == *' "'* || "$target" == *" '"* ]]; then
        # Strip an optional title ("target "title"" / "target 'title'").
        target="${target%%[[:space:]]*}"
      fi
      emit_link "$lineno" "$target" || true
      match="${BASH_REMATCH[0]}"
      [[ -n "$match" && "$rest" == *"$match"* ]] || break
      rest="${rest#*"$match"}"
    done

    # Reference-style links: [text][id] and shortcut [id].
    while [[ "$rest" =~ $re_ref ]]; do
      id="${BASH_REMATCH[2]:-${BASH_REMATCH[3]}}"
      if [[ -n "$id" && -n "${refs[$id]:-}" ]]; then
        emit_link "$lineno" "${refs[$id]}" || true
      fi
      match="${BASH_REMATCH[0]}"
      [[ -n "$match" && "$rest" == *"$match"* ]] || break
      rest="${rest#*"$match"}"
    done
  done <"$file"
}

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

# Emit a single link target unless it is external. Rendered docs HTML links,
# Jekyll {% link %} tags, and in-page anchors are kept, each validated by the
# handler for the file's role.
emit_link() {
  local lineno="$1" target="$2"
  case "$target" in
    "$SITE_BASE"/*) ;; # rendered docs html link - keep (validated by the root handler)
    http://* | https://* | mailto:* | tel:* | ftp://*) return ;;
  esac
  printf '%s\t%s\n' "$lineno" "$target"
}

# Extract the path from a Jekyll "{% link some/path.md %}" tag.
extract_jekyll_path() {
  local path="${1#\{% link }"
  path="${path%%%\}*}"
  path="${path% }"
  printf '%s\n' "$path"
}

# Validate a single Jekyll {% link %} tag against a base directory.
check_jekyll_link() {
  local rel="$1" lineno="$2" base="$3" target="$4" path fragment
  path="$(extract_jekyll_path "$target")"
  fragment="${target#*%\}}"
  fragment="${fragment#\#}"
  if [[ "$path" == /* ]]; then
    report "$rel:$lineno: {% link %} path '$path' must be relative (no leading slash)"
    return
  fi
  # Generated diagram SVGs are gitignored; check the .puml source instead.
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

# Validate one link target found in a file, dispatching on the file's role.
handle_target() {
  local mode="$1" rel="$2" lineno="$3" target="$4" path fragment
  case "$mode" in
    root)
      case "$target" in
        '#'*) ;; # same-file anchor in a root file: GitHub-rendered, not kramdown
        "$SITE_BASE"/*)
          # Rendered docs HTML link: map back to the source page and check any
          # in-page anchor (the fragment names a heading in that page).
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
        *'{% link'*) report "$rel:$lineno: {% link %} tags only work under docs/; use a rendered docs html link" ;;
        docs | docs/* | /docs | /docs/*)
          report "$rel:$lineno: docs link '$target' must be a rendered html link ($SITE_BASE/...)" ;;
        /*) report "$rel:$lineno: internal link '$target' must be relative (no leading slash)" ;;
        *)
          path="${target%%#*}"
          [[ -e "$ROOT/$path" ]] || report "$rel:$lineno: link target '$path' does not exist"
          ;;
      esac
      ;;
    docs)
      case "$target" in
        "$SITE_BASE"/*) ;; # rendered docs link (external) - not a Jekyll tag
        *'{% link'*) check_jekyll_link "$rel" "$lineno" "$DOCS" "$target" ;;
        \#*) check_fragment "$rel" "$lineno" "$ROOT/$rel" "${target#\#}" "$rel" ;;
        *) report "$rel:$lineno: internal link '$target' must use a Jekyll {% link %} tag" ;;
      esac
      ;;
  esac
}

# Flag documentation vocabulary the styleguide forbids. Scoped to the migrated
# docs tree; fenced code blocks are skipped so literal command output is exempt,
# and the pages that document the rename may name the old prefix.
check_terminology() {
  local file rel
  while IFS= read -r file; do
    rel="${file#"$ROOT"/}"
    while IFS= read -r msg; do
      report "$msg"
    done < <(awk -v rel="$rel" '
      {
        # A code block is delimited by a run of >=3 backticks or tildes; the
        # close must use the same character and be at least as long as the
        # opening run, so a longer fence can contain a shorter run as content
        # (e.g. a four-backtick block showing a three-backtick example).
        body = $0
        sub(/^[[:space:]]*(>[[:space:]]*)*/, "", body)
        if (fence == 0) {
          if (body ~ /^(```|~~~)/) {
            marker = substr(body, 1, 1)
            openlen = 0
            while (substr(body, openlen + 1, 1) == marker) openlen++
            # A backtick fence info string may not contain a backtick.
            if (!(marker == "`" && index(substr(body, openlen + 1), "`") > 0)) { fence = 1; next }
          }
        } else {
          trimmed = body
          sub(/[[:space:]]+$/, "", trimmed)
          if (trimmed ~ /^(`+|~+)$/ && substr(trimmed, 1, 1) == marker && length(trimmed) >= openlen) fence = 0
          next
        }
        scrub = $0
        gsub(/`[^`]*`/, "", scrub)
        gsub(/https?:\/\/[^[:space:]]+/, "", scrub)
        low = tolower(scrub)
        if (low ~ /(^|[^[:alnum:]_])harness([^[:alnum:]_]|$)/)
          printf "%s:%d: terminology: \"harness\" is internal vocabulary; use \"agent\"\n", rel, NR
        if (rel != "docs/deprecations.md" && rel != "docs/install/upgrading.md" && scrub ~ /OPENCODE_SANDBOX_/)
          printf "%s:%d: terminology: the OPENCODE_SANDBOX_ prefix is named only on the deprecations and upgrading pages\n", rel, NR
        if (low ~ /(^|[^[:alnum:]_])(simply|easy|quickly)([^[:alnum:]_]|$)/ || low ~ /let\047s/)
          printf "%s:%d: terminology: avoid the filler words \"simply\", \"easy\", \"quickly\", and \"let\047s\"\n", rel, NR
        if (low ~ /(^|[^[:alnum:]_])(above|below)([^[:alnum:]_]|$)/)
          printf "%s:%d: terminology: do not use positional \"above\"/\"below\"; name the section\n", rel, NR
      }
    ' "$file")
  done < <(find "$DOCS" \
    -not -path "$DOCS/superpowers/*" \
    -not -path "$DOCS/_site/*" \
    -not -path "$DOCS/vendor/*" \
    -name '*.md' | sort)
}

# Run the link check over every .md file under a directory.
check_files() {
  local mode="$1" dir="$2"
  shift 2
  local file rel lineno target
  while IFS= read -r file; do
    rel="${file#"$ROOT"/}"
    while IFS=$'\t' read -r lineno target; do
      handle_target "$mode" "$rel" "$lineno" "$target"
    done < <(link_targets "$file")
  done < <(find "$dir" "$@" -name '*.md' | sort)
}

# Root-level markdown files (README.md, ...): docs links must be rendered HTML.
check_files root "$ROOT" -maxdepth 1

# Files under docs/: internal links must be Jekyll {% link %} tags.
# Skip gitignored trees that are not project documentation: docs/superpowers/
# is local tooling (see AGENTS.md), docs/_site/ is Jekyll build output, and
# docs/vendor/ is the Ruby gem bundle. They are absent in CI but present
# locally, and their markdown would otherwise produce false violations.
check_files docs "$DOCS" \
  -not -path "$DOCS/superpowers/*" \
  -not -path "$DOCS/_site/*" \
  -not -path "$DOCS/vendor/*"

# Terminology vocabulary checks over the same docs tree.
check_terminology

if [[ "$violations" -gt 0 ]]; then
  printf 'docs: %d violation(s) found\n' "$violations"
  exit 1
fi
printf 'docs: all internal links are valid\n'
