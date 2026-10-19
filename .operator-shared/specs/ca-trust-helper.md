---
description: Contract for the CA-trust helper the runner image installs as /usr/local/bin/msb-trust - which application trust stores need it and which do not, the verbatim-vendoring pin, why it is not run automatically, and where it is documented.
read_if: Working on TLS interception, secret-substitution failures in a specific application (Java/Gradle, Chrome, ...), the runner image's build-context files, or the msb-trust helper.
---

# CA-trust helper

## Why it exists

A configured secret turns on TLS interception: microsandbox's proxy terminates TLS towards the allowed host to replace
the placeholder, and the guest trusts an interception CA at `/.msb/tls/ca.pem`. msb's guest init (agentd) installs that
CA into the system store and points the common runtimes at it through `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`,
`CURL_CA_BUNDLE`, and `NODE_EXTRA_CA_CERTS`, so `curl`, `git`, Python, Go, and Node work with no setup.

Applications that keep their **own** trust store do not read any of that. Upstream ships
`scripts/msb-trust/msb-trust.sh` for them, and the runner image installs it as `/usr/local/bin/msb-trust`. Issue #152
is driven by Java: a JVM's `cacerts` is a separate store, so after a user adds a secret every HTTPS request from
Gradle/Maven fails with `SunCertPathBuilderException`. The helper's handlers are `java` (JDK `cacerts`) and `chrome`
(Chrome/Chromium NSS store); `msb-trust all` skips handlers whose tools are absent.

## Contract

- The script is **vendored verbatim** at `internal/sandbox/image/data/msb-trust.sh` and is never modified.
  `data/msb-trust.LICENSE` carries the upstream source, the pinned version, the SHA-256, and the re-sync command. The pin
  follows the microsandbox SDK pin (`go.mod`): a new SDK version is the trigger to re-sync and re-record the hash.
- It is installed **executable**: the 755 mode travels in the build-context tar entry and the final stage's `COPY`
  takes the permission bits from there (no `chmod` `RUN`). `buildContextFiles()` is the single source of truth for the
  context tar *and* `computeDockerfileID`, so adding or editing a file ships it and invalidates an existing image by
  construction — a warm image must never keep an older helper.
- The helper is **manual**, or wired by the user through a `home:` entry with `hook: startup`. Java hooks need
  `root: true`: the system JDK's store (`/etc/ssl/certs/java/cacerts`, symlinked from `$JAVA_HOME/lib/security/`) is
  root-owned, so an import as `dev` fails. `MSB_JAVA_STORE_PASSWORD` covers a non-default store password, and
  `JAVA_HOME` selects a non-system JDK.
- We deliberately do **not** auto-run it at VM boot. Upstream rejected an automatic `JAVA_TOOL_OPTIONS` for #1689
  because it cannot cover Nix/SDKMAN JDKs or custom or password-protected stores, and a launcher-side auto-run would
  have to guess the same things. The documented answer is the startup hook.
- The import does **not** survive a VM recreate: the trust store is image state, not home-volume state. The hook is
  what makes it hold across rebuilds, config changes, and resets.
- Not covered, and out of scope for the helper: containers built inside the VM (they have their own root store, so a
  `docker build` that fetches over HTTPS still needs the CA installed into the image being built — the `AGENTS.md`
  workaround stands), and applications with a bundled trust store.

## Node needs no handler

Do not file an upstream feature request for a `node` handler. agentd's env vars already cover Node, and upstream's
position after #1689 is that the helper exists for the stores the system store cannot reach ("we can't cover every
application that uses a separate trust store"). A `node` handler would be dead code.

## Verification

`TestRenderDockerfileTrustHelperRunsInImage` (integration-tagged) builds the runner image and runs
`msb-trust java --help` directly — no shell — which proves the exec bit survived the context `COPY` and that the
vendored script still names the guest CA path (`/.msb/tls/ca.pem`). The unit tests pin the tar entry (content and mode)
and the final stage's `COPY`. Locally, the `java` handler was verified against a real `keytool` (import, idempotent
re-run, and the non-root failure on the root-owned store).

## Documentation

- `docs/configuration/secrets.md` — "Trusting the CA in applications": what already works, what needs the helper.
- `docs/recipes/java-ca-trust.md` — the worked recipe (JDK in the project Dockerfile, manual import, boot hook).
- `docs/runner-image.md` — the installed path, next to the profile.d `PATH` merge script.
