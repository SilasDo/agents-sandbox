# Documentation Site-Wide Rollout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply `STYLEGUIDE.md` to the rest of the `docs/` site: regroup the navigation, fix the known correctness defects, rewrite the seven pages the pilot plan flagged, give the remaining pages a styleguide pass, de-duplicate the repeated explanations, add a first-run tutorial, and add an automated terminology lint.

**Architecture:** One rollout branch (`docs/rollout`) with phased commits. The pilot's `docs/sandboxes.md` is the worked reference for structure; `STYLEGUIDE.md` is the binding rule set. The rewrite-level changes (heading order, applicability-first sections) are structural; the rest are mechanical conformance (heading case, one H1, callouts, fences, banned words, "above/below", terminology). Enforcement is the manual Review checklist plus a new `ci/check-docs.sh` terminology check and its test.

**Tech Stack:** Markdown; Jekyll with the `just-the-docs` v0.10 remote theme (nav via front matter `parent`/`nav_order`, internal links via `{% link %}`); `ci/check-docs.sh` + `ci/test-check-docs.sh`; `make check`.

**Spec:** `STYLEGUIDE.md` (the rule set) and this repository's `.operator-shared/plans/2026-10-22-docs-styleguide-and-pilot.md` — specifically its "Deferred to the rollout plan" section, which is the scope of this plan. Read both before starting.

## Scope and decisions

- **Breadth:** the seven pages the pilot plan named for rewrite get a structural rewrite; the other pages get a light styleguide pass (headings, ordering, terminology, banned words, "above/below", callouts, fences). Every user-facing page under `docs/` is touched.
- **Rewrites (7):** `docs/index.md`, `docs/how-it-works.md`, `docs/runner-image.md`, `docs/commands.md`, `docs/troubleshooting.md`, `docs/configuration/secrets.md`, `docs/configuration/launcher.md`.
- **Delivery:** one branch/PR, phased commits, in the task order below.
- **Terminology:** the user-facing word is **agent**; "harness" never appears in `docs/`.
- **Navigation target (review this before Task 1):** six top-level groups plus the home page, at most two levels deep. This keeps `docs/recipes.md` as its own group rather than nesting it under Guides, so every page stays within the styleguide's two-level depth rule:

  | Group (`parent`) | Landing page | Children |
  | --- | --- | --- |
  | — (home) | `index.md` | — |
  | Get started | `get-started.md` (new) | `introduction.md`, `install/installation.md`, `install/prerequisites.md`, `install/upgrading.md`, `switch.md`, `manage-config.md`, `tutorial.md` (new) |
  | Guides | `guides.md` (new) | `sandboxes.md`, `runner-image.md`, `branch-sessions.md` |
  | Recipes | `recipes.md` (retained) | `recipes/connect-desktop.md`, `recipes/bare-worktrees.md`, `recipes/java-ca-trust.md`, `recipes/openchamber.md` |
  | Configuration | `configuration/index.md` (retained) | the eight `configuration/*.md` pages |
  | Concepts | `concepts.md` (new) | `how-it-works.md` |
  | Reference | `reference.md` (new) | `commands.md`, `troubleshooting.md`, `deprecations.md` |

  `docs/install.md` is replaced by `docs/get-started.md`; the two README links to `install.html` become `get-started.html`. `docs/install/*.md` keep their path.

## Global Constraints

- User-facing docs use **agent**, never "harness".
- One `#` H1 per page; it matches the page's front-matter `title` in sentence case.
- Every internal link under `docs/` uses `{% link ... %}`; root-level Markdown links into `docs/` use rendered HTML (`https://inoio.github.io/agents-sandbox/<page>.html`). `ci/check-docs.sh` enforces both.
- Code fences carry a language (`console`, `sh`, `yaml`, `json`, `markdown`, `text`); leave it off only when none fits.
- Callouts use `> **Note:**`, `> **Warning:**`, or `> **Beta**` on the first line of the blockquote. No other markers.
- No page says "above" or "below"; name the section instead.
- No page uses "simply", "easy", "quickly", or "let's".
- A conditional section names its audience in its first sentence.
- Rewrites preserve every existing fact; move and rewrite presentation, never drop or invent facts.
- `make check` passes (fmt, lint, test, compile-all, `docs-linkcheck`, `docs-linkcheck-test`, `deadcode-test`).
- Keep `README.md` and `docs/` in sync; add `CHANGELOG.md` `[Unreleased]` lines.

## Review Focus

- **A renamed heading orphans an inbound anchor.** `{% link %}` validation checks the target file, not its fragment, so a dead `#anchor` passes CI. After renaming any heading, grep the whole repo for the old anchor and retarget it. (The pilot already hit this with `#vm-identity`.)
- **Reparenting hides a page or breaks a group.** A page whose `parent:` does not exactly match a landing page's `title` disappears from the nav; a landing page that keeps `has_children` while its children moved produces an empty group. Every page must have a `parent` matching a retained landing, or be top-level.
- **A rewrite loses a fact.** Dense pages (`runner-image.md`, `launcher.md`, `commands.md`) carry many exact values (version floors, defaults, label names, flag aliases). Diff against the pre-rewrite page and confirm each value survives.
- **The terminology lint has false positives or misses.** `OPENCODE_SANDBOX_` is legitimate in `deprecations.md`; `harness` must be zero everywhere. The lint must not flag code identifiers or URLs, and the test must prove both the pass and the fail path.
- **Nav order contradicts search intent.** A commonly searched action ("stop a sandbox") drifting below specialized behavior after a regroup defeats the styleguide's third rule.

---

### Task 1: Navigation regroup

**Files:**
- Create: `docs/get-started.md`, `docs/guides.md`, `docs/concepts.md`, `docs/reference.md`
- Delete: `docs/install.md`
- Modify: front matter of every page under `docs/` (the `parent:` and `nav_order:` keys), `README.md` (the `install.html` links)
- Modify: `docs/index.md` "Explore" links (the section landings)

**Interfaces:**
- Consumes: nothing.
- Produces: the section landings every later rewrite task re-points its inbound links at; the `parent` names `Get started`, `Guides`, `Recipes`, `Configuration`, `Concepts`, `Reference`.

- [ ] **Step 1: Create the four new landing pages**

Each is one H1 matching its `title`, a one-sentence lead, and a `{% link %}` list of its children, in the shape of the retained `docs/install.md`:

```markdown
---
title: Get started
description: "<one line for search>"
layout: default
nav_order: 10
has_children: true
has_toc: false
---

# Get started

<One sentence: install agents-sandbox, run your first agent, and adopt your existing setup.>

- [<child title>]({% link <child path> %})
```

`guides.md` (`nav_order: 20`), `concepts.md` (`nav_order: 50`), and `reference.md` (`nav_order: 60`) follow the same shape with their children from the Scope table. Do not add `parent:` to a landing page. List only the children that already exist: **do not link `tutorial.md` yet** (Task 16 creates it and appends its line) — a `{% link %}` to a missing file fails `make docs-linkcheck`.

- [ ] **Step 2: Reparent every page**

Set each page's `parent:` to the group name from the Scope table and its `nav_order:` to the row order given there (10, 20, 30, …). Delete `has_children:`/`has_toc:` from any page that is not a landing (grep `has_children`). `docs/configuration/index.md` and `docs/recipes.md` keep `has_children: true` and gain `nav_order` 40 and 30 respectively.

- [ ] **Step 3: Delete `docs/install.md` and fix its inbound links**

Replace the two `install.html` references in `README.md` with `get-started.html`, and the `{% link install.md %}` references (grep `install.md`) with `{% link get-started.md %}`. Update `docs/index.md` "Explore" to link the five group landings instead of `install/installation.md` and `how-it-works.md` directly.

- [ ] **Step 4: Verify nav and links**

Run: `make docs-linkcheck`
Expected: `docs: all internal links are valid`.
Run: `for f in $(git ls-files 'docs/**/*.md' 'docs/*.md'); do p=$(awk -F': ' '/^parent:/{print $2}' "$f"); [ -n "$p" ] || continue; grep -rq "^title: $p\$" docs || echo "ORPHAN parent '$p' in $f"; done`
Expected: no `ORPHAN` lines — every `parent` matches a retained landing `title`.

- [ ] **Step 5: Commit**

```bash
git add docs README.md
git commit -m "docs: regroup navigation into six user-need sections"
```

---

### Task 2: Correctness fixes

**Files:**
- Modify: `docs/index.md` (truncated table cell; opencode2 omissions)
- Modify: `docs/_config.yml` (tagline and description name opencode2)
- Modify: `docs/install/prerequisites.md` (remove the Windows line)
- Modify: `docs/commands.md` (`build dockerfile --agent` list)

**Interfaces:**
- Consumes: nothing.
- Produces: corrected facts later rewrite tasks can build on.

- [ ] **Step 1: Complete the truncated comparison cell**

`docs/index.md` line 27 ends `...only workspace directory is read-write by default;`. Complete it with the accurate fact from `docs/configuration/mounts.md` and `docs/introduction.md`: extra host paths are opt-in via `mounts`. Use a sentence, not a fragment.

- [ ] **Step 2: Name opencode2 wherever agents are enumerated**

`docs/_config.yml` `tagline`/`description` and `docs/index.md` `title_category` list Claude Code, opencode, pi without opencode2. Add opencode2 alongside opencode (it is the opencode 2 beta; see `internal/agent/agent.go`).

- [ ] **Step 3: Remove the stale Windows platform**

`docs/install/prerequisites.md` lists `Windows (experimental)` in the description and the bullet list. The authoritative platform set is Linux (KVM) and macOS (Apple Silicon) (`AGENTS.md`, `README.md` comparison row, `docs/index.md`). Remove the Windows bullet and the description clause.

- [ ] **Step 4: Fix the `build dockerfile --agent` list**

`docs/commands.md` line ~118 lists `opencode`, `pi`, or `claude-code`. Add `opencode2` (registered in `internal/agent/agent.go`).

- [ ] **Step 5: Verify and commit**

Run: `rg -n "opencode2" docs/_config.yml docs/index.md docs/commands.md; rg -n "Windows" docs/install/prerequisites.md; make docs-linkcheck`
Expected: opencode2 present in the three files; no `Windows` match; links valid.

```bash
git add docs
git commit -m "docs: fix platform list, agent enumeration, and a truncated table cell"
```

---

### Task 3: Rewrite `docs/index.md` (landing page)

**Files:**
- Modify: `docs/index.md`

**Interfaces:**
- Consumes: Task 1's group landings; Task 2's corrected table.
- Produces: the site's lead, used as the tone reference for the remaining rewrites.

- [ ] **Step 1: Restructure to the page template**

Required top-level sequence:

```markdown
# agents-sandbox
## Comparison
## Start here
## Explore
```

- Lead (before `## Comparison`): two plain sentences — what the tool does and why (own runtime, secrets stay on the host). Drop the `;)` sentence and the "moves out of the way" line or fold their meaning into the lead. Do not use `checkout slug` or `/home/dev`.
- `## Comparison`: keep the table and its sources/legend unchanged.
- `## Start here`: keep the three routing bullets.
- `## Explore`: link the five group landings (`Get started`, `Guides`, `Recipes`, `Configuration`, `Concepts`, `Reference`) rather than individual pages.

- [ ] **Step 2: Self-audit and verify**

Run the `STYLEGUIDE.md` Review checklist over the page; confirm `grep -c harness docs/index.md` is `0` and exactly one `# ` heading. Then `make docs-linkcheck`.
Expected: links valid.

- [ ] **Step 3: Commit**

```bash
git add docs/index.md
git commit -m "docs: rewrite the landing page per styleguide"
```

---

### Task 4: Rewrite `docs/how-it-works.md`

**Files:**
- Modify: `docs/how-it-works.md`

**Interfaces:** Consumes Task 1's `Concepts` group. Produces nothing later tasks depend on.

- [ ] **Step 1: Restructure; this is an explanation page**

Required top-level sequence:

```markdown
# Architecture & Concepts
## What a run does
## Project identity and migration
## System context
```

- Remove the duplicate heading (`# How it works` followed by `## How It Works`). The single H1 is `# Architecture & Concepts` (matches the front-matter `title`).
- `## What a run does`: the current six-step numbered list, with the same facts.
- `## Project identity and migration`: unchanged in substance; explain the two slugs before naming them (identity rule).
- `## System context`: the C4 diagram, caption, and the `/workspace`/permission/secrets/multi-client paragraphs.

- [ ] **Step 2: Verify**

Run: `grep -n '^#\{1,2\} ' docs/how-it-works.md` and `grep -c '^# ' docs/how-it-works.md`
Expected: the sequence above; `1` H1.
Run: `make docs-linkcheck`
Expected: valid.

- [ ] **Step 3: Commit**

```bash
git add docs/how-it-works.md
git commit -m "docs: rewrite Architecture & Concepts per styleguide"
```

---

### Task 5: Rewrite `docs/runner-image.md`

**Files:**
- Modify: `docs/runner-image.md`

**Interfaces:** Consumes Task 1's `Guides` group.

- [ ] **Step 1: Restructure; this is a reference page**

Order by what a reader is most likely to need: build and manage, then the base contract, then the internals. Required top-level sequence:

```markdown
# Runner Image
## Building and managing images
## Home directory
## Docker-in-Docker
## Node and the agent
## The base image contract
## One image per checkout
## Labels and provenance
## Upgrades
```

- Lead: keep the two-sentence summary.
- Every heading is sentence case; rename `## Base starting point` → the base contract section; `### Skip-build when unchanged` and `### Content-verified load` become `###` subsections of `## Building and managing images`; `## Image Lifecycle` folds into `## Building and managing images`.
- Keep every value: stage names, `NODE_VERSION` v24.21.0, `/opt/agents-sandbox`, label names, tag format, the Alpine/distroless exclusions, `--dind`/`dind` alias notes.
- Replace "above"/"below" references with named sections (three occurrences: lines 43, 87, 103).
- The `msb-trust` paragraph and its links stay.

- [ ] **Step 2: Verify facts and structure**

Run: `grep -n '^#\{1,2\} ' docs/runner-image.md`; `grep -c '^# ' docs/runner-image.md`; `rg -n -i '\b(above|below)\b' docs/runner-image.md`
Expected: sequence above; `1`; no matches.
Run: `git show HEAD:docs/runner-image.md > /tmp/runner-before.md && diff <(rg -o '[A-Z_]+_VERSION|/opt/[a-z/-]+|agents-sandbox-[a-z-]+|debian:trixie-slim' /tmp/runner-before.md | sort -u) <(rg -o '[A-Z_]+_VERSION|/opt/[a-z/-]+|agents-sandbox-[a-z-]+|debian:trixie-slim' docs/runner-image.md | sort -u)`
Expected: no dropped identifiers.

- [ ] **Step 3: Commit**

```bash
git add docs/runner-image.md
git commit -m "docs: rewrite Runner Image per styleguide"
```

---

### Task 6: Rewrite `docs/commands.md`

**Files:**
- Modify: `docs/commands.md`

**Interfaces:** Consumes Task 1's `Reference` group.

- [ ] **Step 1: Add an in-page structure; this is a reference page**

Required changes:
- Fix the H1: `# Commands Reference` → `# Commands` (matches the front-matter `title`).
- Sentence-case every heading (`## Global Flags` → `## Global flags`, `#### build dockerfile` stays lower-case as a command).
- Add a short lead plus an in-page table of contents grouping commands by area (runtime, images, volumes, sandboxes, storage, config, agents) so a reader can jump; do not split the file.
- Replace "below" references (line ~174) with a named section.

- [ ] **Step 2: Verify**

Run: `grep -n '^#\{1,2\} ' docs/commands.md`; `grep -c '^# ' docs/commands.md`; `rg -n '\bbelow\b' docs/commands.md`
Expected: `## Commands` H1 present; `1`; no matches.

- [ ] **Step 3: Commit**

```bash
git add docs/commands.md
git commit -m "docs: restructure the Commands reference per styleguide"
```

---

### Task 7: Rewrite `docs/troubleshooting.md`

**Files:**
- Modify: `docs/troubleshooting.md`

**Interfaces:** Consumes Task 1's `Reference` group.

- [ ] **Step 1: Restructure; this is a task page**

Required changes:
- Lead: one sentence, plus a `> **Note:**` pointing at `doctor` first.
- Fold the resource-management block (`## Stale sandboxes consume resources`, `### List and prune`, `### Stop a specific VM`) — it duplicates `docs/sandboxes.md`; replace it with a one-line pointer to the Sandboxes page (see Task 15).
- Rename the stale `## Branch session issues` section to `## Worktree session issues` and its "branch prompt" wording to worktree terminology.
- Remove "above"/"below" (lines 40, 45) — name the section.
- Sentence-case headings.

- [ ] **Step 2: Verify**

Run: `grep -n '^#\{1,2\} ' docs/troubleshooting.md`; `rg -n -i '\bbranch\b' docs/troubleshooting.md`; `rg -n -i '\b(above|below)\b' docs/troubleshooting.md`
Expected: no "branch session" heading; no above/below matches.

- [ ] **Step 3: Commit**

```bash
git add docs/troubleshooting.md
git commit -m "docs: rewrite Troubleshooting per styleguide"
```

---

### Task 8: Rewrite `docs/configuration/secrets.md`

**Files:**
- Modify: `docs/configuration/secrets.md`

**Interfaces:** Consumes Task 1's `Configuration` group.

- [ ] **Step 1: Restructure; this is a reference page**

Required changes:
- Lead: keep the two-sentence definition.
- Move the current `## Quick start` content out of a heading that is neither a task nor a summary. Fold its two facts (the `/connect` workflow pointer; where `provision-host-config: false` goes) into the lead as a `> **Note:**` and a `## Format`-adjacent sentence. There is no new `## Quick start` heading.
- Required top-level sequence: `# Secrets`, `## Format`, `## Precedence`, `## Supported files`, `## Placeholder values`, `## Trusting the CA in applications`, `## OpenCode authentication`.
- Replace "below" (lines 20, 124-anchor reference) by naming the section (`OpenCode authentication`).
- Normalize the `> **_NOTE:_**` blockquote at the end to `> **Note:**`.
- Keep every fact: formats, last-`@` rule, precedence order, placeholder `$MSB_<NAME>`, the `msb-trust` verbs and elevation, the Copilot example.

- [ ] **Step 2: Verify**

Run: `grep -n '^#\{1,2\} ' docs/configuration/secrets.md`; `rg -n -i '\bnotes?:_|_NOTE' docs/configuration/secrets.md`; `rg -n -i '\bbelow\b' docs/configuration/secrets.md`
Expected: sequence above; no `_NOTE_` markers; no below matches.

- [ ] **Step 3: Commit**

```bash
git add docs/configuration/secrets.md
git commit -m "docs: rewrite Secrets per styleguide"
```

---

### Task 9: Rewrite `docs/configuration/launcher.md`

**Files:**
- Modify: `docs/configuration/launcher.md`

**Interfaces:** Consumes Task 1's `Configuration` group.

- [ ] **Step 1: Restructure; merge the field table and the env-var table**

Required changes:
- H1 `# Configuration files & environment variables` → `# Configuration` is taken; use `# Configuration files and environment variables` matching the `title` (update the `title` to match too, sentence case).
- Merge `### Launcher configuration environment variables` into the single `## Configuration file` field table by adding an **Environment variable** column, then delete the duplicate table. Keep the `AGENTS_SANDBOX_`/`OPENCODE_SANDBOX_` precedence paragraph.
- Remove the duplicated precedence list in `## Per-slug configuration`; keep one numbered precedence list and cross-link it.
- Replace "above"/"below" (lines 185, 196) by naming sections.
- Required top-level sequence: `# …`, `## User-level defaults`, `## Project-level configuration`, `## Precedence`, `## Configuration file`, `## Per-slug configuration`, `## Environment variables`.
- Keep every field, flag, default, and validation rule.

- [ ] **Step 2: Verify**

Run: `grep -n '^#\{1,2\} ' docs/configuration/launcher.md`; `rg -n -i '\b(above|below)\b' docs/configuration/launcher.md`; `rg -c 'AGENTS_SANDBOX_' docs/configuration/launcher.md`
Expected: sequence above; no above/below; one env-var mapping table (the merged one).

- [ ] **Step 3: Commit**

```bash
git add docs/configuration/launcher.md
git commit -m "docs: rewrite Configuration files & env vars per styleguide"
```

---

### Task 10: Light pass — Get started group

**Files:**
- Modify: `docs/introduction.md`, `docs/install/installation.md`, `docs/install/prerequisites.md`, `docs/install/upgrading.md`, `docs/switch.md`, `docs/manage-config.md`

**Interfaces:** Consumes Task 1's `Get started` group and Task 2's prerequisites fix.

- [ ] **Step 1: Apply the light pass, page by page**

For each page: exactly one H1 matching the `title`; sentence-case headings; explain-before-you-name; conditional sections applicability-first; no "above"/"below"; no "simply/easy/quickly/let's"; callouts standardized; fences carry a language.

Specific fixes:
- `docs/install/installation.md`: change the `## Installation` H2 to an `# Installation` H1.
- `docs/switch.md`: `## ⚠️ What to know` → `## What to know` (drop the emoji).
- `docs/install/upgrading.md`: remove the banned words ("simply") and the "below" references.
- `docs/manage-config.md`: the `## 1.`–`## 4.` numbered headings are a task sequence; keep the numbers but sentence-case the titles and confirm every sentence of the intro names the outcome.
- `docs/introduction.md`: confirm the `## What this is for` section is the last (explanation order); light prose tightening only.

- [ ] **Step 2: Verify**

Run: `rg -n -i "\b(simply|easy|quickly)\b|let's" docs/introduction.md docs/install/ docs/switch.md docs/manage-config.md`; `rg -n -i '\b(above|below)\b' docs/introduction.md docs/install/ docs/switch.md docs/manage-config.md`; `for f in docs/introduction.md docs/install/installation.md docs/install/prerequisites.md docs/install/upgrading.md docs/switch.md docs/manage-config.md; do echo "$f: $(grep -c '^# ' "$f")"; done`
Expected: no banned words; no above/below; each page `: 1`.
Run: `make docs-linkcheck`
Expected: valid.

- [ ] **Step 3: Commit**

```bash
git add docs/introduction.md docs/install docs/switch.md docs/manage-config.md
git commit -m "docs: styleguide pass over the Get started pages"
```

---

### Task 11: Light pass — Guides group

**Files:**
- Modify: `docs/branch-sessions.md`

**Interfaces:** Consumes Task 1's `Guides` group. (`sandboxes.md` is already conformant; `runner-image.md` is Task 5.)

- [ ] **Step 1: Apply the light pass**

Add a one-sentence lead after the H1 before the `--worktree` explanation; sentence-case the headings (`## Basic Usage` → `## Basic usage`); keep the daemon-agent `> **Requires a daemon-based agent.**` callout first in its section (applicability-first).

- [ ] **Step 2: Verify and commit**

Run: `grep -n '^#\{1,2\} ' docs/branch-sessions.md`; `grep -c '^# ' docs/branch-sessions.md`
Expected: sentence-case sequence; `1`.

```bash
git add docs/branch-sessions.md
git commit -m "docs: styleguide pass over the Guides pages"
```

---

### Task 12: Light pass — Configuration group

**Files:**
- Modify: `docs/configuration/agent.md`, `docs/configuration/mounts.md`, `docs/configuration/networking.md`, `docs/configuration/notifications.md`, `docs/configuration/home-provisioning.md`, `docs/configuration/self-upgrade.md`, `docs/configuration/index.md`

**Interfaces:** Consumes Task 1's `Configuration` group and Tasks 8–9's rewritten pages.

- [ ] **Step 1: Apply the light pass**

Specific fixes:
- `docs/configuration/agent.md`: remove the four "above/below" references by naming the section (lines 68, 125, 131, 146, 159); sentence-case headings.
- `docs/configuration/home-provisioning.md`: remove "simply" (line 30); add a lead.
- `docs/configuration/index.md`: it is a landing — add a one-sentence lead and keep the `{% link %}` child list.
- `docs/configuration/mounts.md`, `networking.md`, `notifications.md`, `self-upgrade.md`: sentence-case headings, lead sentence, callout and fence conformance; `notifications.md` already leads with applicability.

- [ ] **Step 2: Verify and commit**

Run: `rg -n -i '\b(above|below|simply|easy|quickly)\b|let''s' docs/configuration`; `for f in docs/configuration/*.md; do echo "$f: $(grep -c '^# ' "$f")"; done`
Expected: no matches; each page `: 1`.
Run: `make docs-linkcheck`
Expected: valid.

```bash
git add docs/configuration
git commit -m "docs: styleguide pass over the Configuration pages"
```

---

### Task 13: Light pass — Recipes group

**Files:**
- Modify: `docs/recipes.md`, `docs/recipes/connect-desktop.md`, `docs/recipes/bare-worktrees.md`, `docs/recipes/java-ca-trust.md`, `docs/recipes/openchamber.md`

**Interfaces:** Consumes Task 1's `Recipes` group.

- [ ] **Step 1: Apply the light pass**

- `docs/recipes.md`: it is a landing — keep the AGENTS.md example, sentence-case `## Agent Context (AGENTS.md)` and the code fence (currently `dotenv`; use `markdown` for an AGENTS.md snippet), and ensure the `{% link %}` child list is present.
- `docs/recipes/openchamber.md`: remove the three "above" references (lines 36, 94, 152) by naming the section or repeating the short fact; remove "below" (line 52).
- Others: sentence-case headings, lead sentence, callout conformance.

- [ ] **Step 2: Verify and commit**

Run: `rg -n -i '\b(above|below)\b' docs/recipes.md docs/recipes`; `for f in docs/recipes/*.md docs/recipes.md; do echo "$f: $(grep -c '^# ' "$f")"; done`
Expected: no matches; each page `: 1`.
Run: `make docs-linkcheck`
Expected: valid.

```bash
git add docs/recipes.md docs/recipes
git commit -m "docs: styleguide pass over the Recipes pages"
```

---

### Task 14: Light pass — Reference group (Deprecations)

**Files:**
- Modify: `docs/deprecations.md`

**Interfaces:** Consumes Task 1's `Reference` group. (`commands.md` is Task 6; `troubleshooting.md` is Task 7.)

- [ ] **Step 1: Apply the light pass**

Sentence-case headings; remove the "below" reference (line 24) by naming the table; keep `OPENCODE_SANDBOX_` here (this page is the one place the old prefix is named).

- [ ] **Step 2: Verify and commit**

Run: `grep -n '^#\{1,2\} ' docs/deprecations.md`; `rg -n -i '\bbelow\b' docs/deprecations.md`
Expected: sentence-case; no matches.

```bash
git add docs/deprecations.md
git commit -m "docs: styleguide pass over Deprecations"
```

---

### Task 15: De-duplicate repeated explanations

**Files:**
- Modify: `docs/sandboxes.md`, `docs/configuration/launcher.md`, `docs/runner-image.md`, `docs/troubleshooting.md`, `docs/install/upgrading.md`, `docs/how-it-works.md`

**Interfaces:** Consumes the rewrites; runs after them.

- [ ] **Step 1: Consolidate the prune-confirmation boilerplate**

The "prune prompts for confirmation; pass `--yes`; `--dry-run` previews" explanation appears in `docs/sandboxes.md`, `docs/runner-image.md`, and (after Task 7) is removed from `docs/troubleshooting.md`. Keep the full explanation once — in `docs/sandboxes.md` under Pruning — and have `runner-image.md` and `troubleshooting.md` link to it for the mechanics while keeping their own command examples.

- [ ] **Step 2: Consolidate the home-volume keep/migrate/reset explanation**

It appears in `docs/sandboxes.md` (`Home volumes`), `docs/configuration/launcher.md` (`Resource Config Application`), `docs/runner-image.md` (`Home directory`), and `docs/install/upgrading.md`. Keep the canonical explanation in `docs/sandboxes.md`; reduce the others to the one fact they each need plus a `{% link %}`.

- [ ] **Step 3: Verify no page repeats the full explanation**

Run: `rg -n "keep, migrate, or reset" docs`; `rg -n "prompts for confirmation" docs`
Expected: each phrase appears once (in `docs/sandboxes.md`).

- [ ] **Step 4: Commit**

```bash
git add docs
git commit -m "docs: de-duplicate prune and home-volume explanations"
```

---

### Task 16: First-run tutorial

**Files:**
- Create: `docs/tutorial.md`
- Modify: `docs/get-started.md` (add the child), `docs/index.md` if its Explore list enumerates tutorial, `docs/install/installation.md` (link forward)

**Interfaces:** Consumes Task 1's `Get started` landing; Task 15's de-duplicated sandbox explanation.

- [ ] **Step 1: Write the tutorial as a task page**

Required top-level sequence:

```markdown
# Your first sandbox
## Install agents-sandbox
## Check your system
## Run an agent
## What you see
```

- Lead: one sentence stating the outcome (a sandbox running your project, in about the time it takes to read the page) — without "quickly" or "easy".
- `## Install agents-sandbox`: the Homebrew / release-binary choice, linking `{% link install/installation.md %}` rather than re-listing every step.
- `## Check your system`: `agents-sandbox doctor`, what a pass looks like, and where to go on failure (`{% link install/prerequisites.md %}`, `{% link troubleshooting.md %}`).
- `## Run an agent`: `cd` into a project, `agents-sandbox`, and selecting an agent with `--agent`; a `console` block.
- `## What you see`: the agent TUI inside the VM, `/workspace` is the project, where to stop it (`agents-sandbox stop`), and the next steps list (Switch / Manage config / Sandboxes).
- Every command must run unedited.

- [ ] **Step 2: Verify**

Run: `grep -n '^#\{1,2\} ' docs/tutorial.md`; `grep -c '^# ' docs/tutorial.md`; `rg -n -i "\b(simply|easy|quickly)\b|let's" docs/tutorial.md`; `make docs-linkcheck`
Expected: sequence above; `1`; no banned words; links valid.

- [ ] **Step 3: Commit**

```bash
git add docs/tutorial.md docs/get-started.md docs/install/installation.md
git commit -m "docs: add a first-run tutorial"
```

---

### Task 17: Terminology lint in `ci/check-docs.sh`

**Files:**
- Modify: `ci/check-docs.sh`
- Modify: `ci/test-check-docs.sh`

**Interfaces:**
- Consumes: the rollout leaves `docs/` free of the lint's targets, so the check passes on the branch.
- Produces: `check_terminology` and a `violations` contribution; the test proves the pass and the fail path.

- [ ] **Step 1: Write the failing test first**

Add a fixture case to `ci/test-check-docs.sh` that writes a docs page containing `harness` and asserts a non-zero exit with a message naming the page and the word; add a second case asserting `OPENCODE_SANDBOX_FOO` in `docs/deprecations.md` is **allowed** while the same string in another page is flagged. Run `make docs-linkcheck-test`; expect the new cases to FAIL (the check does not exist yet).

- [ ] **Step 2: Implement the check**

Add a `check_terminology` function to `ci/check-docs.sh`, run after the link checks. For every `.md` under `docs/` (same `-not -path` exclusions as the link check):
- `harness` (case-insensitive, word boundary) → violation.
- `OPENCODE_SANDBOX_` → violation **except** in `docs/deprecations.md`.
- `\b(simply|easy|quickly)\b` or `let's` → violation.
- `\b(above|below)\b` → violation.
Report as `docs: <rel>:<lineno>: terminology: "<word>" ...` and increment `violations`.

- [ ] **Step 3: Run the test and the full check**

Run: `make docs-linkcheck-test`
Expected: PASS (all cases, including the two new ones).
Run: `make docs-linkcheck`
Expected: `docs: all internal links are valid` (no terminology violations on the migrated tree).

- [ ] **Step 4: Commit**

```bash
git add ci/check-docs.sh ci/test-check-docs.sh
git commit -m "docs: add a terminology lint to the docs check"
```

---

### Task 18: Whole-rollout verification and handoff

**Files:**
- Modify: `CHANGELOG.md` (`## [Unreleased]` → `### Changed` / `### Added`)
- Modify: `.operator-shared/index/index.md` and `.operator-shared/catalog.md` (new pages: `tutorial.md`, the section landings)

**Interfaces:** none (verification and bookkeeping).

- [ ] **Step 1: Add the CHANGELOG entries**

Under `## [Unreleased]` add one `### Changed` line: docs: apply `STYLEGUIDE.md` across the site (navigation regroup, page rewrites, first-run tutorial, terminology lint). Add one `### Added` line for the tutorial.

- [ ] **Step 2: Run the full gate**

Run: `make check`
Expected: passes.

- [ ] **Step 3: Confirm the rollout's invariants**

Run: `rg -n -i "harness" docs`; `rg -n -i "\b(above|below|simply|easy|quickly)\b|let's" docs`; `for f in $(git ls-files 'docs/**/*.md' 'docs/*.md'); do c=$(grep -c '^# ' "$f"); [ "$c" = 1 ] || echo "$c $f"; done`
Expected: no "harness"; no banned words; every page exactly `1` H1 (the `configuration/secrets.md`, `runner-image.md`, `troubleshooting.md`, `java-ca-trust.md` false positives from shell comments in fences may appear — confirm each extra count is a code-block comment, not a heading).

- [ ] **Step 4: Update the brain**

Update `.operator-shared/index/index.md` for `docs/tutorial.md` and the new landing pages, and refresh the `docs/` entry in `.operator-shared/catalog.md` if it names pages. Note in the pilot plan (`2026-10-22-docs-styleguide-and-pilot.md`) that its deferred rollout is implemented by this plan, and record this plan's completion per `guides/operator-brain-maintenance.md`.

- [ ] **Step 5: Request review, then commit**

Use the `code-review` skill over the branch against `STYLEGUIDE.md` and this plan. Then:

```bash
git add CHANGELOG.md .operator-shared
git commit -m "docs: changelog and brain updates for the rollout"
```

---

## Post-implementation notes

(to be filled in during execution; includes any deviation from the fixed heading sequences above and the reason, in the shape of the pilot plan's notes.)
