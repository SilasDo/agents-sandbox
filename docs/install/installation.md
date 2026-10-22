---
title: Installation
description: "Install agents-sandbox via Homebrew or a release binary, and keep it up to date."
layout: default
parent: Get started
nav_order: 20
---

# Installation

Install agents-sandbox with Homebrew or a release binary.

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

  To install, simply set permissions and move to your path:

  ```console
  chmod u+x agents-sandbox
  mv agents-sandbox ~/.local/bin # or any other directory in your PATH 
  ```

## Download canaries

  Canary builds are GitHub prereleases, so they are not served by `/latest`. Download the asset from that prerelease's
  tag URL instead, substituting the canary version and your platform's `<os>-<arch>` suffix
  (`linux-amd64`, `darwin-arm64`, or `linux-arm64`):

  ```console
  curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/download/v<version>/agents-sandbox-<os>-<arch>
  ```

  Then set `upgrade.channel: canary` (or `AGENTS_SANDBOX_UPGRADE_CHANNEL=canary`) so later upgrades follow the canary
  line — see [self-upgrade]({% link configuration/self-upgrade.md %}). Homebrew cannot install canaries.

Once installed, continue with [Your first sandbox]({% link tutorial.md %}).
