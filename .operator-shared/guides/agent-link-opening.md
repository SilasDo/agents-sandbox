---
description: How the coding agents open links (what each shells out to on Linux, claude-code's own scheme allowlist and headless guard, the OSC 8 wrinkle), plus the network-free VM→host transport constraint for any host-side opener.
read_if: Working on opening links/URLs on the host from inside the VM (issue #273), an in-VM `xdg-open` shim, or any VM→host action channel.
---

# Agent link opening and the VM→host opener channel

Findings for issue #273 ("Open links from the agent TUI on the host"). Read from the shipped
binaries with `strings` — refresh by re-running `strings -n 6 <binary> | rg -i 'xdg-open|openUrl|openPath'`.
Observed against: opencode v1 `1.19.3` (npm `opencode-ai`), opencode2 (the `opencode2` binary in
the dev VM), claude-code `2.1.308`, pi `1.4.0`.

## What each agent runs on Linux

- **opencode (v1)** — TUI link click: `Bun.spawn(["xdg-open", url])`, no scheme check. Its
  GitHub-app install (`xdg-open "<url>"`) and GitLab/OAuth flows also shell out to `xdg-open`.
- **opencode2** — bundles the `open` npm package. `openUrl` rejects anything that is not
  `http:`/`https:`; `openPath` accepts any path or URL. Both reach `xdg-open` on Linux
  (bundled copy preferred, else `PATH`).
- **pi** — `openBrowser(target)` (its own helper) runs `xdg-open <target>` with no restriction;
  called for the login-dialog auth URL and wired as the markdown renderer's `openUrl`, so TUI
  link clicks go through it. The helper never invokes a shell (deliberate, to avoid URL
  injection).
- **claude-code** — its own opener with a **scheme allowlist** in code: `http`, `https`,
  `vscode:`, `vscode-insiders:`, `cursor:`, `windsurf:`, `zed:`, `jetbrains:`, `idea:`,
  `slack:`, `linear:`, `notion:`, `figma:`. `file:` is routed to the file manager (dbus
  `FileManager1.ShowItems`); other schemes are refused with a warning. Two behaviours matter for
  any shim: on headless Linux (`!DISPLAY && !WAYLAND_DISPLAY`) it returns `no_display` *before*
  calling the opener, and it honours `$BROWSER` (spawning that instead of `xdg-open`). It also
  has an `openByHand` fallback that prints a `! xdg-open <url>` command for the user to run.

## The OSC 8 wrinkle

All four also emit OSC 8 hyperlinks (`\x1b]8;;URL`), which the *host* terminal handles — so
link rendering is not the gap. It is not sufficient either: the TUIs capture mouse input for
their own clickable UI, so the terminal never receives the click and the TUI's own opener is
what runs. OSC 8 also cannot cover URLs opened programmatically (OAuth/login flows).

## Transport constraint: keep it off the network

Any guest→host action channel must not use guest egress: the default `network: none` profile
blocks it, and this must work out of the box. microsandbox's `host` network profile exists but
is not the default.

Network-free path — the per-project **home volume** is already mounted at `/home/dev` and,
being a local microsandbox volume, exposes its host-side path to the launcher via
`VolumeHandle.Path()` (see `internal/sandbox/volume/inspect.go`). So an in-VM shim can write a
request into a spool directory under `$HOME`, and a launcher-owned watcher reads it directly
from the volume's host path. No guest egress, no listening socket, no new mount; a launcher
bind-mounted spool dir is the fallback if the volume path is not directly readable (one extra
mount, a one-time VM recreate).

Direction note: the existing `internal/notify` watcher is the opposite direction — the host
connects to the in-VM daemon's SSE stream over the published host port (host→guest), which also
needs no guest egress. An opener is the launcher's first VM→host *action* channel.
