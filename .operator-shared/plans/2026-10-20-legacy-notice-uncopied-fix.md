# Legacy-runtime notice: uncopied-volume fix (PR #239 review)

**Context:** Greptile P1 on `issue-207-legacy-runtime-notice`. The notice derived
"uncopied" legacy volumes from name presence alone in the private home. A `kept`
volume — present but unverified, produced by the ambiguous-state "copy the missing
volumes" option — was therefore treated as safely copied, and the notice recommended
the wholesale `rm -rf ~/.microsandbox` and the `agents-sandbox-home-*` glob, either of
which deletes the only trustworthy copy.

**Fix:** `Copied` is retained at the migration's terminal word (`finalizeMigration`,
`dismissMigration`); `uncopiedLegacyVolumes` marks a legacy volume safe only when it is
both present in the private home and recorded `copied`; `showNotice` reads the persisted
state itself instead of taking a snapshot from `Offer`, so a campaign finalized moments
earlier is reflected without a caller having to re-read.

**Tests:** the ambiguous-state copy names the kept volume and withholds both destructive
commands; a `copied`-and-present volume is not named and the commands are offered;
terminal `Copied` retained; the notice's own state-read failure is best-effort.

Supersedes the "derive from listings only / render-time `Excluded`" wording in the
notice section of `specs/runtime-home-decoupling.md`, which this fix updates.
