# hooks-overrides — town-local provenance-hook survival

Installed to `~/.gt/hooks-overrides/` (= `gtPrimaryDir()/hooks-overrides`, where
`internal/hooks` ComputeExpected LoadOverride reads them). `gt doctor --fix`
regenerates each `.claude/settings.json` from DefaultOverrides + these files and
MERGES them (same-matcher replaces), so the provenance consumer hooks
(UserPromptSubmit → provenance-inject, SessionStart → provenance-assert-units)
survive doctor runs that previously stripped a hand-edit.

DRIFT CAVEAT: these files carry the FULL matcher="" hook lists (base commands +
provenance), so if the fork's DefaultOverrides changes a base command
(gt mail check, dispatch-selfheal, gt prime), update these too. Durable
zero-drift fix = add provenance to DefaultOverrides() in internal/hooks (rides an
attested deploy); tracked with the hooks-template bead.

## polecats.json / crew.json — token-budget governor TIER-2 (hq-qn7m)

`PreToolUse` gate on sub-agent fan-out (`usage-gate-fanout.sh`). Installed on the
two fan-out-spawning worker roles only: `<rig>/polecats` and `<rig>/crew`.

NO DRIFT CAVEAT here, unlike the files above: the matcher `Agent|Task` exists in
neither the base config nor `DefaultOverrides()`, so `mergeEntries` APPENDS it
rather than replacing anything. These files therefore carry ONLY the new entry and
never need to mirror base commands.

`Agent|Task` is an EXACT tool-name list, not a regex: Claude Code compares a matcher
made only of letters/digits/`_`/`-`/space/`,`/`|` as an exact string with `|`
alternatives. `Agent` is the live tool name (Claude Code 2.1.218); `Task` is kept
only for version drift. Verified that `AgentOutputStyle`/`Agentic` do NOT match.

`|| true` in the command is a deliberate fail-open backstop: for PreToolUse ONLY
exit code 2 blocks, and a bash parse error also exits 2 — so a broken script could
otherwise block every sub-agent spawn town-wide. The gate blocks exclusively via
JSON `permissionDecision: deny`, never via an exit code.

Roles deliberately NOT gated: mayor, deacon, witness, refinery, boot. They are T0/T1
in `tiers.py` and the engine exempts T0 before it ever reads a threshold, so
installing there would be dead weight. See hq-qn7m for the mayor recommendation.
