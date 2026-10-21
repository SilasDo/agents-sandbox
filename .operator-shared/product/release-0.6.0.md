---
description: Product framing for the 0.6.0 release — its two headline stories in order, "no system-wide microsandbox required" as a selling point of the isolation boundary, the renames demoted to a footnote, and where each part of the story lives in docs. The framing spec the prepare-release issues (#208/#209/#210/#211) refer to.
read_if: Writing or reviewing any user-facing text for the 0.6.0 release, curating its changelog/release notes, or asking what the release's story is.
---

# Release 0.6.0 — positioning and framing

The durable framing spec behind the 0.6.0 prepare-release issues (#207–#211).

## Two headline stories (order matters)

1. **Own your runtime.** agents-sandbox provisions its own private microsandbox
   home (`~/.local/share/agents-sandbox/msb`) and manages it. A system-wide
   microsandbox is no longer used **or required** — a genuine selling point of
   the isolation story, not an implementation detail. The management commands
   (`df` and the `storage` group; `sandbox`/`volume`/`image` `inspect`/`remove`)
   make that ownership concrete.
2. **Fast, small runner images.** Per-tool multistage composition: the tool
   layers (base CLI tools, Node, agent, optional Docker engine) cache
   independently and are reused across projects, Node ships only for the agent
   that needs it, and a custom base needs no `curl`/`tar`.

## Renames are a footnote

The three renames — `dind`→`docker`, the `OPENCODE_SANDBOX_*`→`AGENTS_SANDBOX_*`
environment-variable prefix, and prune's confirmation-by-default clean break —
are a clearly-scoped footnote, never a headline. Narrative dilution from the
renames is the release's only "too much"; the bundle otherwise ships whole
(the entire `[Unreleased]` range, no deferrals — the #211 verdict).

## Where the story lives

- `docs/sandboxes.md` — the durable "why" (private runtime, no system-wide
  microsandbox required) plus the management commands.
- `docs/runner-image.md` — the fast, small, cache-friendly image story.
- `docs/install/upgrading.md` — migration mechanics, the uninstall payoff, and
  the renames footnote.
- `docs/deprecations.md` — the full rename register and removal targets.
- `README.md` and `docs/index.md` are deliberately left unchanged for 0.6.0.

Nothing overclaims: the runtime is framed as private and self-managed, not
"dependency-free", and the isolation boundary stays **separate microVM and Linux
kernel**.
