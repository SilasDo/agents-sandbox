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
OpenChamber so its requests refer to the same path. On the host, `/workspace` must resolve to the checkout. If you only
need one repository, point it at the project root as described below. For several repositories that should share one VM
and one network/resource policy, use a common parent directory as the project workspace.

### One repository

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

### Several repositories in one sandbox

Put the repositories you want to work on under a common parent directory, then start agents-sandbox from that parent.
For example:

```text
~/work/customer-x/
├── service-a/
├── service-b/
└── shared-library/
```

Start `agents-sandbox run --agent opencode2 --serve-only` from `~/work/customer-x/`. In OpenChamber, add each repository
as a separate project using its guest path: `/workspace/service-a`, `/workspace/service-b`, and
`/workspace/shared-library`. The one OpenCode 2 server can then serve requests for each repository, and OpenChamber
keeps their sessions and project settings separate. They share the same sandbox VM, home volume, resource limits, and
network policy. This is useful when several repositories belong to one customer or need the same permitted egress.

The host-side `/workspace` mapping described above must point to `~/work/customer-x/`, the common parent, rather than
to one repository inside it. On macOS, use the parent directory as the target in `/etc/synthetic.conf`. OpenChamber's
host-side filesystem, terminal, and preview features can then address those same subpaths.

This layout gives the agent access to every repository mounted under the parent. Use it only when those repositories
belong to the same trust boundary. Use separate agents-sandbox VMs when projects need different network policies,
credentials, or resource limits.

OpenChamber's filesystem, terminal, and preview processes run on the host. They do not inherit the VM's isolation,
installed tools, or network policy. OpenChamber file operations can therefore access the host checkout through
`/workspace`; commands launched from its terminal run on the host.

OpenChamber's project path is host-local, and agents-sandbox does not currently translate paths between host and guest.
For host-side OpenChamber, use the main checkout(s) in the shared workspace. OpenChamber-created worktrees live on the
host, while an OpenCode server in the VM cannot see them at the same paths. Conversely, agents-sandbox `--worktree`
creates worktrees inside the VM's persistent home volume, which OpenChamber's host-side Git, filesystem, terminal, and
preview features cannot see. Multi-run with isolation disabled may run several sessions against the same mounted
checkout, but they share files and can conflict; isolated multi-run requires worktrees and is not supported by this
host-side arrangement.

Using OpenChamber worktrees and isolated multi-run with consistent Git, file, terminal, and preview access requires
OpenChamber and OpenCode to run in the same VM, which is the optional in-VM integration being evaluated in
[issue #266](https://github.com/inoio/agents-sandbox/issues/266). Until that design is implemented and tested,
keep worktree and isolated multi-run features off for the host-side setup.

## Security boundary

This host-side arrangement isolates the OpenCode agent process in the VM; it does **not** sandbox the OpenChamber
server. OpenChamber runs with the host user's operating-system permissions, so treat access to its UI as access to
host-side development tools and data:

- OpenChamber's file browser reads and edits project files on the host. Normal workspace operations are scoped to the
  selected project, although some file-read APIs can explicitly access paths outside it, subject to host permissions.
- Its terminal and project actions run commands as the host user. Those commands can access any files and network
  services available to that user. Review and trust project actions or repository-provided OpenChamber setup before
  running them; do not assume the VM's egress policy applies to them.
- Its Git integration runs on the host and may use host Git configuration and credentials. OpenChamber's own server,
  extensions, integrations, update checks, tunnels, and other enabled features also use the host's network access.
- Only OpenCode-agent requests go through the sandboxed daemon and its microsandbox network policy. Keeping the
  OpenCode port on `127.0.0.1` does not restrict the host-side OpenChamber process.

Keep OpenChamber bound to localhost unless remote access is needed, and set a strong `OPENCHAMBER_UI_PASSWORD`. Pair
only devices you trust and revoke unused pairings. Prefer OpenChamber Private Relay or a trusted VPN over exposing a
public tunnel; a tunnel makes the host-capable UI reachable remotely. Never expose the agents-sandbox OpenCode port to
the LAN or Internet: the current `opencode2` Basic Auth credential (`opencode:opencode`) is fixed in the runner image,
not a per-project secret. For a shared-parent workspace, every repository under that parent is accessible to the VM
agent and host-side OpenChamber, so use it only for repositories in the same trust boundary.

See OpenChamber's [Security](https://github.com/openchamber/openchamber/blob/main/packages/docs/content/docs/security.mdx)
and [Environment](https://github.com/openchamber/openchamber/blob/main/packages/docs/content/docs/environment.mdx)
guides for its UI authentication, enterprise policy, extensions, and network settings. These controls govern
OpenChamber itself; they do not move its host-side processes into the sandbox.

## Remote access

OpenChamber's web server binds to localhost by default. To connect its mobile or desktop clients, follow
OpenChamber's [device pairing guide](https://github.com/openchamber/openchamber/blob/main/packages/docs/content/docs/connect-devices.mdx).
Keep the agents-sandbox OpenCode port loopback-only; remote clients should connect to OpenChamber, not directly to the
agent server. Apply the access controls described above before allowing connections from other machines.

Both the `agents-sandbox run --serve-only` process and the OpenChamber server must remain running while you use the UI.
When finished, stop OpenChamber and press `Ctrl-D` in the agents-sandbox terminal.
