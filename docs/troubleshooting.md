---
title: Troubleshooting
description: "Fixes for common agents-sandbox problems, such as failing doctor checks, Docker and microsandbox runtime issues."
layout: default
parent: Reference
nav_order: 20
---
# Troubleshooting

Common issues and how to resolve them.

## `agents-sandbox doctor` fails

If the doctor command reports missing prerequisites:

### Docker not running

Start the Docker daemon. How to start it depends on your environment. When using Docker Desktop, start it. With Docker installed as system wide package on Linux, the normal startup procedure is to execute `sudo systemctl start docker`

You can verify afterwards by running `docker info`. It should show a healthy daemon.

### Microsandbox runtime

agents-sandbox provisions its own private microsandbox runtime under its data
directory (`~/.local/share/agents-sandbox/msb`, or
`$XDG_DATA_HOME/agents-sandbox/msb`) and points the SDK at it. The runtime never
reads or writes your own `~/.microsandbox`; the one-time upgrade migration reads
it only to copy your old volumes (see [Upgrading to 0.6]({% link install/upgrading.md %})).
It does not install a user-facing `msb` command, and does not require any
particular `msb` version on your machine. If agents-sandbox is the only thing
installed, the absence of an `msb` command is expected, not a defect.

If you encounter a runtime error, submit a bug report at:

<https://github.com/inoio/agents-sandbox/issues/new?template=bug_report.yml>

### Legacy home volumes after upgrading

For the upgrade overview — what changed, the migration choices, and how to retire
`~/.microsandbox` — see [Upgrading to 0.6]({% link install/upgrading.md %}). The
notes below cover diagnosing an interrupted or unclear copy.

An interrupted copy resumes where it stopped, because progress is tracked in
agents-sandbox's host state. If a private volume already exists under a legacy
name — for example after `~/.local/state/agents-sandbox/state.yaml` was removed —
its status is unclear; the note below covers that case.

#### Private volumes with unclear status

The offer says so, cannot tell whether such a volume is a complete copy or a
partial one, and gives two options:

- **Copy the missing volumes; dismiss the existing private volumes from the
  migration** — copies only the absent volumes and leaves the existing ones
  untouched and never asked about again.
- **Quit** — if you want a specific volume fully migrated, remove its private
  copy with `agents-sandbox volume remove --force <name>` (the copy is still
  referenced by its project's state, hence `--force`), then run again: the next
  offer will re-copy it from the legacy home. Keep the copies you do not want
  migrated.

### KVM unavailable

```console
# Check if /dev/kvm exists
ls -la /dev/kvm
```

If missing, enable virtualization in your system BIOS/UEFI (INTEL-VT, AMD-V or similar) and ensure your user is in the `kvm` group:

```console
sudo usermod -aG kvm "$USER"
```

Log out and back in for the group change to take effect.

## macOS-specific issues

### Not Apple Silicon

The doctor command will exit with an error if you are running the x86_64 binary under Rosetta 2. Download the `darwin-arm64` binary instead. To check your current architecture:

```
uname -m
```

Expected output on supported hardware: `arm64`.

### Docker socket not found

On macOS, the Docker socket is managed by Docker Desktop or colima. Verify it is running:

```
docker info
```

If Docker Desktop is installed but not running, launch it from Applications. If using colima:

```
colima start
```

agents-sandbox finds the socket the same way the `docker` CLI does: `DOCKER_HOST` wins, then the active
docker context, then the default `/var/run/docker.sock`. If `docker info` works but agents-sandbox reports
the socket as unreachable, check which context is active and where it points:

```
docker context ls
```

A context marked `*` that points at a stopped provider explains the failure — switch it with
`docker context use <name>`, or point agents-sandbox at the socket directly:

```
export DOCKER_HOST=unix://$HOME/.colima/default/docker.sock
```

Note that `DOCKER_HOST` takes precedence over the active context, so an outdated value in your shell
profile overrides a correct context.

## VM won't start

When a VM won't start, check the general troubleshooting steps first.

### "create sandbox: ..." errors

Try `--log-level verbose` to see the full error:

```console
agents-sandbox run --log-level verbose
```

Common causes:
- Not enough system memory — reduce with `-m 2G`

## Stale sandboxes consume resources

### List and prune

```console
# See what's running
agents-sandbox list

# Remove old resources
agents-sandbox prune --dry-run    # preview
agents-sandbox prune --yes        # remove (skips the confirmation prompt)
```

### Stop a specific VM

```console
agents-sandbox stop               # graceful stop
agents-sandbox kill               # force kill
```

## Branch session issues

### "failed to create worktree"

If the managed worktree creation fails:

```console
# Clean up stale worktrees in your repo
git worktree prune
```

Then retry the command. If the problem persists, check for uncommitted changes or locked worktrees:

```console
git worktree list
```

### Branch prompt hangs

If the branch creation prompt doesn't respond, check that the repository has a remote configured:

```console
git remote -v
```

Without a remote, some git operations may hang. Add one or use an absolute branch name:

```console
git remote add origin https://example.com/repo.git
```

## Missing tools inside the sandbox

If you need a tool that isn't in the base image (e.g., `go`, `rustc`, `python3`), add it to your project's custom Dockerfile, see [Runner Image]({% link runner-image.md %}) documentation.

## Secrets not available

Verify the secret is set with the correct format. For legacy `env.secret`, the format is `KEY=value@host` where the
part after the **last** `@` is the host policy tag — values may contain `@`. If no `@host` part is present the
secret is dropped with a warning.

For `env.secret.yaml`, values may contain `@` without issues. Ensure the entry defines either `host`, `hosts`, or
`allow_any_host_dangerous: true` — entries with no hosts definition are silently skipped. An empty `value` is valid
and passed through unchanged.

## TLS certificate errors for one application

Once a secret is configured, requests to the matching hosts go through microsandbox's TLS proxy, whose interception CA
the guest system store trusts. An application with its own trust store does not, and fails with a certificate-path
error — Java reports
`SunCertPathBuilderException: unable to find valid certification path to requested target`. Import the CA into that
store with the `msb-trust` helper from the runner image:

```console
agents-sandbox shell --root
msb-trust java
```

See [Trusting the CA in applications]({% link configuration/secrets.md %}) for which applications already work, and the
[Java recipe]({% link recipes/java-ca-trust.md %}) for the boot hook that repeats the import after a recreate.

## Memory or CPU limits too low

If the agent runs slowly or VMs fail to start with resource errors, increase allocation:

```console
agents-sandbox run -c 4 -m 8G
```

Or set defaults in config:

```yaml
# ~/.config/agents-sandbox/config.yaml
cpus: 4
memory: 8G
```

## Config not applying

If your config files aren't being picked up:

1. Verify the file exists in the right location:

   ```console
   ls ~/.config/agents-sandbox/config.yaml
   ls .agents-sandbox/config.yaml
   ```

2. Check valid syntax:

   ```console
   # For YAML files
   python3 -c "import yaml; yaml.safe_load(open('config.yaml'))"
   
   # For JSON/JSONC files
   python3 -c "import json; json.load(open('config.json'))"
   ```

3. Check that CLI flags aren't overriding your config (flags always win).

4. Use `--log-level verbose` to see which config files were loaded:

   ```console
   agents-sandbox run --log-level verbose
   ```

## Image build fails

Docker build failures are usually due to:

1. **Network issues** — The base image downloads packages from the internet. Ensure outbound access:

    ```console
    curl -fsSL https://debian.org | head -c 100
    ```

2. **Docker daemon memory** — Large builds may need more memory:

    ```console
    docker info | grep "Total Memory"
    ```

3. **Custom Dockerfile errors** — If using a project Dockerfile, build it manually to isolate the issue:

    ```console
    docker build -f .agents-sandbox/Dockerfile -t test-image .
    ```

If an older image or updater state reports `Release vuser-provided not found`, update agents-sandbox and rebuild the
runner image. `user-provided` identifies an agent that was previously found in a custom base; it is not a valid release
version. A rebuilt image will keep an agent supplied by the base, or install a real resolved release if that binary is
no longer present.
