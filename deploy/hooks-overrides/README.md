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
