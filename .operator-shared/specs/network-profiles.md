---
description: Contract for the launcher's composable network profiles: public/private/host compose into one microsandbox policy, none is terminal and allowlist-only, the config/env/flag surfaces accept a scalar, comma string, or list, and the fingerprint keeps one profile= line per profile so single-profile digests (and existing VMs) do not change.
read_if: Changing network.Policy, profile parsing or validation (ParseProfile, ParseProfiles, viperconfig profileEntries/normalizeNetworkProfile/stringToSliceDecodeHook), the network.profile / --network / AGENTS_SANDBOX_NETWORK_PROFILE surface, or network fingerprinting and its VM-reconfiguration trigger.
---

# Network profiles

The `network:` policy is built from composable profiles. `Policy` lives in `internal/sandbox/network`; the config surface
is wired in `internal/viperconfig`.

## public, private, host compose

`public`, `private`, and `host` are disjoint destination groups and compose into one policy: `Policy.Config()` passes
every selected profile to `msbSdk.NetworkPolicy.FromProfilesChecked`, which unions their allow rules, deduplicates them,
emits them in canonical `public` → `private` → `host` order, and adds one narrow gateway-DNS rule. `public` is the
complement of the other groups, so it does **not** include `private` or `host` — that is why reaching a host service
means combining `[public, host]` rather than relying on `public`.

## none is terminal

`none` is launcher-only: it contributes no SDK profile rule, yielding deny-by-default egress with ingress allowed, plus a
manually added gateway-DNS rule. It is an allowlist-only policy, not an airgap, and it is terminal — `none` combined with
any other profile is a validation error (`checkProfileCombination`), mirroring microsandbox's own treatment of
`none`/`all` as whole-policy values. An unset or empty profile list defaults to `public` (`Policy.Effective`).

## One key, three shapes

`network.profile` accepts a scalar, a comma-separated string, or a YAML list. `--network` is a `StringSlice` flag
(comma-separated or repeated) and `AGENTS_SANDBOX_NETWORK_PROFILE` takes a comma-separated string. viperconfig's
`stringToSliceDecodeHook` — not mapstructure's `StringToSliceHookFunc`, which fires only for `[]string` — normalizes the
scalar string forms into `[]network.Profile`. Whitespace around a profile name is insignificant in every shape, and
repeated names collapse instead of erroring (`network.ParseProfiles` trims and dedupes). `profileEntries` plus
`network.ParseProfiles` validate the raw config value before decode, and `normalizeNetworkProfile` stores the parsed list
back on the viper key so decode cannot disagree with validation. Precedence is unchanged: flag > env > config > default.

## Fingerprint stability (do not drift)

`Policy.Fingerprint()` emits one sorted, deduplicated `profile=<name>` line per profile. A single-profile policy therefore
hashes exactly as it did before profiles became composable, so upgrading does not change a project's persisted
`network_state.hash` and does not spuriously recreate its VM. Changing this format is a migration, not a refactor.
`state.BuildNetworkState` records the profile names for display only; they are never compared for change detection.
