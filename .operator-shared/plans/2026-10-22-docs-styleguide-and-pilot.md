# Documentation Styleguide and Pilot Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Write the agents-sandbox documentation styleguide and rewrite `docs/sandboxes.md` end-to-end as the reference implementation that proves it.

**Architecture:** A contributor-facing `STYLEGUIDE.md` at the repo root fixes audience, structure, ordering, terminology, formatting, a page template, and a review checklist. The pilot applies every rule to `docs/sandboxes.md`, so the rules can be judged against a real page before a site-wide rollout. Enforcement is the manual checklist plus the existing `ci/check-docs.sh` link checks; an automated terminology lint is deferred to the rollout plan.

**Tech Stack:** Markdown; Jekyll with the `just-the-docs` remote theme (nav via front matter, internal links via `{% link %}`); `ci/check-docs.sh` + `ci/test-check-docs.sh`; `make check`.

**Spec:** The design is the conversation that produced this plan; its rules are the content of `STYLEGUIDE.md` (Task 1), and the phase-1 non-goals are in "Deferred to the rollout plan". External sources the styleguide cites: diataxis.fr (one content kind per page), NN/g ("How Users Read on the Web", "How Little Do Users Read?", "F-Shaped Pattern"), the Google developer documentation style guide (front-loading, second person, sentence-case headings), clig.dev, and just-the-docs v0.10 navigation.

## Context and decisions

Audience: a reader with **no prior knowledge and ordinary patience**. The docs must be understandable on first contact, not merely correct.

Terminology decision: user-facing docs say **agent** for the program that runs inside the VM (opencode, opencode2, pi, Claude Code). "Harness" is an internal/test-suite term and does not appear in `docs/`.

The feedback on the first `sandboxes.md` attempt generalizes into three ordering rules (they are styleguide rules, not one-off edits):

1. **Explain before you name.** Introduce a concept in a plain sentence before the shorthand; do not lead with a parenthetical like "one VM per (checkout, agent)" or an incidental internal path like `/home/dev`.
2. **Lead a section with its applicability.** The first sentence of a conditional section names who it applies to, so everyone else can skip it (e.g. the idle-session rule is "opencode only").
3. **Order by universality and search intent, not taxonomy.** Actions that apply to everyone and are commonly searched ("stop a sandbox now") come before conditional or specialized behavior.

These sit alongside: one content kind per page (task vs reference vs explanation), inverted pyramid per page and per section, one term per concept, plain language (clig.dev).

## Global Constraints

- User-facing docs use **agent**, never "harness".
- Every internal link under `docs/` uses `{% link ... %}`; root-level Markdown links to docs use rendered HTML (`https://inoio.github.io/agents-sandbox/<page>.html`). `ci/check-docs.sh` enforces both, and generated `diagrams/*.svg` references resolve to their `.puml` source.
- One `#` H1 per page; it matches the page's front-matter `title` in sentence case.
- `make check` must pass (fmt, lint, test, compile-all, `docs-linkcheck`, `docs-linkcheck-test`, `deadcode-test`).
- Add a `[Unreleased]` line to `CHANGELOG.md`.
- Keep `README.md` and `docs/` in sync (AGENTS.md).

## Review Focus

- A section that applies to only some agents does not state so in its first sentence → a pi/claude-code reader wastes time on it.
- A concept is introduced with its technical name or path before it is explained → the exact failure the user flagged in "one VM per (checkout, agent)".
- A universally useful action (stop, remove) sits below harness-specific behavior.
- The word "harness" leaks into `docs/`.
- A page's first body heading is not an H1, or does not match the front-matter `title`.

---

### Task 1: Write `STYLEGUIDE.md` and wire it into the contributor docs

**Files:**
- Create: `STYLEGUIDE.md`
- Modify: `CONTRIBUTING.md` (the `## Documentation` section)
- Modify: `AGENTS.md` (the `## Documentation` section)
- Modify: `CHANGELOG.md` (`## [Unreleased]` → `### Added`)
- Modify: `.operator-shared/index/index.md` (add the new root file)

**Interfaces:**
- Consumes: nothing.
- Produces: the rule names Task 2 is verified against — "Explain before you name", "Lead with applicability", "Order by universality", and the required sections listed below.

- [ ] **Step 1: Write `STYLEGUIDE.md` with these exact sections, in order**

The required section list and rule names below are content the plan fixes; the surrounding prose is the implementer's.

```markdown
# Documentation Styleguide

## Who this is for
## Voice and audience
## Structure: one kind per page
## Ordering: the three rules
## Page template
## Terminology
## Formatting
## Review checklist
```

Required content of each section:

- **Who this is for** — anyone editing `docs/` or `README.md`.
- **Voice and audience** — second person ("you"), active voice, short sentences, plain language; assume no prior knowledge and ordinary patience; define a term on first use; never use "simply", "easy", "quickly", or "let's".
- **Structure: one kind per page** — a page is a task, a reference, or an explanation; never mix more than one kind in the top-level flow. Cite diataxis.fr.
- **Ordering: the three rules** — state rules 1–3 from "Context and decisions" verbatim (Explain before you name / Lead with applicability / Order by universality), each with a one-line before/after example.
- **Page template** — lead sentence → "In short" callout or Quick reference table → the body in short chunks → detail and the "why" last.
- **Terminology** — a table fixing at least:

  | Use | Not |
  |---|---|
  | agent | harness |
  | sandbox (for the VM as a whole); VM only for kernel/hypervisor detail | (must not alternate for the same thing) |
  | worktree session | branch session |
  | `--docker` / `docker` | `--dind` / `dind` |
  | `AGENTS_SANDBOX_*` | `OPENCODE_SANDBOX_*` (only in the deprecations page) |

- **Formatting** — sentence-case headings; one H1 matching the title; fenced code blocks with the correct language (`console`, `yaml`, `json`, `sh`, `markdown`, `text`); one callout convention (`> **Note:**` / `> **Warning:**` / `> **Beta**`); tables for enumerations; no "above"/"below" (use self-describing references).
- **Review checklist** — a copy-pasteable list: name the page's one kind; can the headings alone carry the point; is every conditional section applicability-first; is every concept explained before it is named; is any paragraph over ~6 sentences; do command examples run unedited; does the terminology match the table; is nav depth ≤ 2.

- [ ] **Step 2: Link it from both contributor docs**

In `CONTRIBUTING.md`'s `## Documentation` section add one sentence: user-facing docs follow [`STYLEGUIDE.md`](STYLEGUIDE.md). In `AGENTS.md`'s `## Documentation` section add the same pointer.

- [ ] **Step 3: Verify the links resolve and the sections exist**

Run: `ci/check-docs.sh && for s in "Who this is for" "Voice and audience" "Structure: one kind per page" "Ordering: the three rules" "Page template" "Terminology" "Formatting" "Review checklist"; do grep -q "## $s" STYLEGUIDE.md || echo "MISSING: $s"; done`
Expected: `docs: all internal links are valid`, and no `MISSING:` lines.

- [ ] **Step 4: Add the CHANGELOG entry**

Under `## [Unreleased]` → `### Added`, add: docs: add `STYLEGUIDE.md` defining the documentation voice, structure, ordering, terminology, and formatting rules.

- [ ] **Step 5: Record the new root file in the Project Index**

Add a `STYLEGUIDE.md — Documentation styleguide for user-facing docs` line to the root-file list in `.operator-shared/index/index.md`.

- [ ] **Step 6: Commit**

```bash
git add STYLEGUIDE.md CONTRIBUTING.md AGENTS.md CHANGELOG.md .operator-shared/index/index.md
git commit -m "docs: add documentation styleguide"
```

---

### Task 2: Rewrite `docs/sandboxes.md` as the reference implementation

**Files:**
- Modify: `docs/sandboxes.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: the three rules and the page template from Task 1.
- Produces: the worked example the rollout copies.

- [ ] **Step 1: Rewrite the page to this required top-level heading sequence**

```markdown
# Sandboxes
## Quick reference
## Lifecycle
## Stopping and removing a sandbox
## Identity: one VM per checkout and agent
## Home volumes
## Idle timeout: when a sandbox stops
## Concurrency
## Why agents-sandbox runs its own runtime
```

- **Lead (before the first `##`)** — one or two plain sentences: a sandbox is a VM agents-sandbox creates for your project, it keeps its state between runs, and it stops on its own when idle. Do not use "checkout slug", "checkout/agent pair", or `/home/dev` here.
- **Quick reference** — a table of the universal actions, in this order: see all sandboxes (`agents-sandbox list`); stop the project sandbox (`agents-sandbox stop`); remove it (`agents-sandbox sandbox remove <name>`); reclaim stale ones (`agents-sandbox prune --dry-run`, then `--yes`); see storage use (`agents-sandbox df`). Precede it with one line that you rarely manage sandboxes by hand. The existing accuracy is kept; this collapses the former "List" and "Manual Pruning" sections.
- **Stopping and removing** — moved above identity/idle/authored detail because it applies to every agent and is a commonly searched action. Keep `stop`, `stop -f`, `kill`, and the state-vs-volume note.
- **Identity** — open with the concept ("Each checkout gets its own sandbox, and each agent within it gets its own") *before* naming the slugs; then the existing slug detail, unchanged in substance. This is the section the user found unreadable.
- **Idle timeout** — the first sentence must state that the wait-for-quiescence behavior applies to **opencode only**, and that opencode2, pi, and claude-code skip it; then the `auto-stop-on-active-sessions` fork as a two-row table; then the exception sentence (retry threshold, pending question); then a copy-pasteable YAML block for `auto-stop-timeout`, `auto-stop-on-active-sessions`, and `auto-stop-max-session-retries`; cross-link `Configuration` for the field table rather than as the only explanation.
- **Why agents-sandbox runs its own runtime** — the current opening section, moved last and labeled as explanation.
- Keep every existing fact (VM naming, volume lifecycle, prune semantics, concurrency lock, auto-prune). Do not invent, drop, or re-order facts; move and rewrite their presentation.

- [ ] **Step 2: Verify structure and the two lead requirements**

Run:
```bash
grep -n '^#\{1,2\} ' docs/sandboxes.md
grep -c '^# ' docs/sandboxes.md
awk '/^## Idle timeout/{f=1;next} f&&/^[A-Za-z]/{print;exit}' docs/sandboxes.md
```
Expected: the heading sequence matches Step 1; exactly `1` H1; the first sentence after the Idle-timeout heading names `opencode`.

- [ ] **Step 3: Verify links and the full check**

Run: `make docs-linkcheck && make check`
Expected: both pass.

- [ ] **Step 4: Self-audit against the styleguide checklist**

Walk the Review checklist from `STYLEGUIDE.md` over the rewritten page. In particular confirm: the word "harness" is absent (`grep -c harness docs/sandboxes.md` → `0`); no paragraph exceeds ~6 sentences; every command example runs unedited.

- [ ] **Step 5: Add the CHANGELOG entry**

Under `## [Unreleased]` → `### Changed`, add: docs: rewrite the Sandboxes page for a first-time reader (plain-language lead, quick-reference table, applicability-first idle-timeout section), following `STYLEGUIDE.md`.

- [ ] **Step 6: Commit**

```bash
git add docs/sandboxes.md CHANGELOG.md
git commit -m "docs: rewrite Sandboxes page per styleguide"
```

---

### Task 3: Whole-phase verification and review handoff

**Files:** none (verification only).

- [ ] **Step 1: Run the full gate**

Run: `make check`
Expected: passes.

- [ ] **Step 2: Confirm the phase's two artifacts**

Run: `ls STYLEGUIDE.md && git log --oneline -2`
Expected: `STYLEGUIDE.md` exists and the two commits are present.

- [ ] **Step 3: Request review**

Use the `code-review` skill over the branch, or dispatch a reviewer, against the spec: the styleguide must contain the three ordering rules and the terminology table, and the pilot must satisfy them. Report any rule the pilot could not satisfy, so the rollout plan can revisit it.

---

## Deferred to the rollout plan

Not in this phase; captured so scope stays honest:

- **Navigation regroup** into sections by user need (Get started / Guides / Configuration / Concepts / Reference), using `parent:` (just-the-docs v0.10 ignores `has_children`; several pages still carry the old three-level pattern).
- **Remaining page rewrites**: `runner-image.md`, `secrets.md` (the mislabeled "Quick start"), `how-it-works.md` (duplicate heading), `index.md` (landing-page lead), `commands.md` (in-page TOC or per-group split), `configuration/launcher.md` (merge the field table and the env-var table), `troubleshooting.md` (stale "branch" sections).
- **A first-run tutorial** (install → doctor → run → what you see).
- **Correctness fixes** found in review: the truncated cell in `index.md`, the Windows line in `install/prerequisites.md`, the opencode2 omissions in `_config.yml`/`index.md`, the `build dockerfile --agent` list.
- **De-duplication** of the prune-confirmation boilerplate and the home-volume keep/migrate/reset explanation.
- **An automated terminology lint** in `ci/check-docs.sh` scoped to a list of migrated pages, grown as the rollout proceeds.

## Post-implementation notes

Implemented on `docs/styleguide` (a nested worktree of the bare `/workspace`), commits `b8a3452..9b15af4`, pending merge.

- Task 1 landed `STYLEGUIDE.md` and the contributor-doc pointers; Task 2 rewrote `docs/sandboxes.md` as the reference implementation; Task 3 verified (`make check` EXIT=0).
- **Recorded deviation from this plan's fixed heading text:** Task 2 mandated the H2 `## Identity: one VM per checkout and agent`; it was renamed to `## Identity: one sandbox per checkout and agent`, and the inbound anchor in `docs/configuration/agent.md` was retargeted. The styleguide's Terminology rule is the binding authority; the plan's heading text was its argument. Also fixed, as consequences of the heading renames: a stale `#vm-identity` anchor in `agent.md` and a dead `#why-agents-sandbox-runs-its-own-microsandbox` fragment in `docs/install/upgrading.md`.
- A whole-branch review fix wave (`7fcd1da`, `9b15af4`) softened the styleguide's command-output rule (not fabricated output), fixed four of the styleguide's own "above/below" self-violations, added rule 3's example, mapped the "In short" template item to a `> **Note:**` callout, added the `{% link %}` linking line, softened the "need not know Jekyll" claim, restored two idle-timeout qualifiers, and added the one-kind clarification.
- Deferred from review (rollout): `docs/switch.md:11` links `{% link index.md %}#install`, but `index.md` has no `#install` heading — `ci/check-docs.sh` validates a `{% link %}` target's file, not its fragment, so no CI check catches a dead fragment; and `docs/configuration/agent.md:49` still says "the VM name, home volume, and state file".
