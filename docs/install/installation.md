---
title: Installation
description: "Install agents-sandbox on Linux or macOS, then verify the host is ready to run your first sandbox."
layout: default
parent: Get started
nav_order: 20
---

# Installation

Install agents-sandbox via binary download or Homebrew.

## Binary download

  **Linux (x86_64):**

  ```console
  curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/latest/download/agents-sandbox-linux-amd64
  ```

  **macOS (Apple Silicon):**

  ```console
  curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/latest/download/agents-sandbox-darwin-arm64
  ```

  **Linux (arm64):**

  ```console
  curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/latest/download/agents-sandbox-linux-arm64
  ```

  To install, set permissions and move it onto your path:

  ```console
  chmod u+x agents-sandbox
  mv agents-sandbox ~/.local/bin # or any other directory in your PATH 
  ```

## Via Homebrew (macOS and Linux)

  ```console
  brew tap inoio/agents-sandbox https://github.com/inoio/agents-sandbox
  brew install agents-sandbox
  ```

Updates ship as ordinary releases; to get one:

  ```console
  brew update && brew upgrade agents-sandbox
  ```

Homebrew installs are detected automatically: the launcher disables its
[self-upgrade]({% link configuration/self-upgrade.md %}) so Homebrew's version bookkeeping stays accurate, and
`agents-sandbox upgrade` points you back to `brew upgrade`.

## Download canaries (pre-releases)

Canaries are not available via Homebrew, only as binary downloads.

Canary builds are GitHub prereleases, so they are not served by `/latest`. Download the asset from that prerelease's
tag URL instead, substituting the canary version and your platform's `<os>-<arch>` suffix
  (`linux-amd64`, `darwin-arm64`, or `linux-arm64`):

```console
# (non-ARM) Linux
CANARY_VERSION=v0.6.0-canary.1 curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/download/${CANARY_VERSION}/agents-sandbox-linux-amd64
# (ARM) Linux
CANARY_VERSION=v0.6.0-canary.1 curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/download/${CANARY_VERSION}/agents-sandbox-linux-arm64
# MacOS (Darwin)
CANARY_VERSION=v0.6.0-canary.1 curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/download/${CANARY_VERSION}/agents-sandbox-darwin-arm64
```

To install, set permissions and move it onto your path:

```console
chmod u+x agents-sandbox
mv agents-sandbox ~/.local/bin # or any other directory in your PATH 
```

Then set `upgrade.channel: canary` (or `AGENTS_SANDBOX_UPGRADE_CHANNEL=canary`) so later upgrades follow the canary line — see [self-upgrade]({% link configuration/self-upgrade.md %}). 

Once installed, continue with [Your first sandbox]({% link tutorial.md %}).
