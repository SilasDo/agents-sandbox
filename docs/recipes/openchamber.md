---
title: Connect OpenChamber
description: "Use OpenChamber's web UI with an OpenCode 2 server running in an agents-sandbox VM."
layout: default
parent: Recipes
nav_order: 15
---
# Connect OpenChamber

Use OpenChamber's web UI as a host-side client for an OpenCode 2 server in an agents-sandbox VM. OpenChamber 2 is the
OpenChamber application release line; `opencode2` is agents-sandbox's profile for the OpenCode 2 agent. Current
OpenChamber versions require OpenCode 2 (currently 2.1.0 or newer).

This recipe runs the OpenChamber CLI/server on the host and the OpenCode agent in the VM. It does not install
OpenChamber in the VM.

## Start the sandbox server

Run this from the project checkout and keep the process open:

```console
agents-sandbox run --agent opencode2 --serve-only
```

The command prints a loopback URL such as `http://127.0.0.1:41237`. Use the exact port it prints; agents-sandbox
allocates it dynamically and keeps the host binding on `127.0.0.1`.

## Start OpenChamber

Install OpenChamber's CLI/Web server on the host using its
[installation instructions](https://github.com/openchamber/openchamber/blob/main/packages/docs/content/docs/install.mdx).
The CLI requires Node.js 24.14 or newer and an installed OpenCode CLI. Then, in another host terminal, configure it to
use the sandbox's OpenCode server:

```console
export OPENCODE_HOST=http://127.0.0.1:41237 # replace with the URL printed above
export OPENCODE_PASSWORD=opencode
export OPENCODE_SKIP_START=true
export OPENCHAMBER_UI_PASSWORD='choose-a-strong-password'
openchamber
```

Open the local URL printed by OpenChamber, usually `http://localhost:3000`. `OPENCODE_SKIP_START=true` prevents
OpenChamber from starting a second OpenCode server. The `opencode2` profile currently uses HTTP Basic Auth with
username `opencode` and password `opencode`; `OPENCODE_PASSWORD` supplies that password to OpenChamber. The UI password
is a separate credential for signing in to OpenChamber.

## Project path and host-side features

The OpenCode server runs with the project mounted at `/workspace`. Select `/workspace` as the project directory in
OpenChamber so its requests refer to the same path. On the host, `/workspace` must resolve to the checkout. The setup
differs by operating system.

On Linux, create a symlink to the project root:

```console
sudo ln -s "$PWD" /workspace
```

On macOS, the root filesystem is read-only. Add a synthetic root-level symlink pointing to the checkout, then reboot
for macOS to create it. Back up `/etc/synthetic.conf` first if it already exists; keep any existing entries and add one
tab-separated line for the current checkout:

```console
printf 'workspace\t%s\n' "${PWD#/}" | sudo tee -a /etc/synthetic.conf
sudo reboot
```

After reboot, verify that `/workspace` resolves to the checkout before starting OpenChamber. The synthetic entry is
system-wide and tied to this checkout's absolute path; update it if the checkout moves or you want to use a different
project.

OpenChamber's filesystem, terminal, and preview processes run on the host. They do not inherit the VM's isolation,
installed tools, or network policy. OpenChamber file operations can therefore access the host checkout through
`/workspace`; commands launched from its terminal run on the host.

OpenChamber's project path is host-local, and agents-sandbox does not currently translate paths between host and guest.
Use the main checkout for this setup. Worktrees and multi-run sessions need a separately tested path arrangement and
are not covered by this recipe.

## Remote access

OpenChamber's web server binds to localhost by default. To use its mobile or desktop clients remotely, follow
OpenChamber's [device pairing guide](https://github.com/openchamber/openchamber/blob/main/packages/docs/content/docs/connect-devices.mdx)
and use its Private Relay or a trusted VPN. Keep the agents-sandbox OpenCode port loopback-only; remote clients should
connect to OpenChamber, not directly to the agent server. Set a strong `OPENCHAMBER_UI_PASSWORD` before enabling any
broader network access.

Both the `agents-sandbox run --serve-only` process and the OpenChamber server must remain running while you use the UI.
When finished, stop OpenChamber and press `Ctrl-D` in the agents-sandbox terminal.
