# Patch Notes — Ben-Williams-Founder/gastown fork

Branch: `town-build-2026-06-07`
Canonical source: Ben-Williams-Founder/gastown (NOT gastownhall/gastown — never upstream)

## Deployed patches (in this branch)

| Commit | Bead | Fix |
|--------|------|-----|
| `ccd45f4e` | wkb-wc2 | `gt dashboard --snapshot`: read from cached status-snapshot.json, no live bd queries |
| `1149fad0` | hq-mnc1 | fix(reaper): purge reverse wisp_dependencies on target side |
| `c77c1720` | hq-60wv | doltserver: reduce read/write timeout 5min → 30s |
| `91f776b1` | hq-3q2c | fix(reaper): always use split-target for danglingParentQuery |
| `acf17b5b` | — | fix(lint): drop dbName from unused reaper queries |
| `01c4d7db` | — | fix(ci): resolve CI failures from upstream rebase |
| `be1cc877` | hq-k27dd | fix(polecat): record state as blocker on non-idle NEEDS_RECOVERY |
| `ba702edd` | — | fix(reaper): raise DefaultAlertThreshold 800→3000 (stop false-alarm flood) |

## Tracked bugs — pending implementation

### hq-3ey5v — `bd dolt start` uses `.beads/dolt/` as data_dir
**Status**: MITIGATED — no gastown-src change needed.
`internal/config/env.go` already sets `BEADS_DOLT_AUTO_START=0` for all Gas Town agents
(introduced in an earlier patch). `gt dolt start` uses the correct `.dolt-data/` path.
Permanent fix (make `bd dolt start` use `.dolt-data/`) belongs in beads-src, not gastown-src.
**Action**: Close hq-3ey5v as mitigated.

### hq-qcrnw — Refinery event channel is global, not rig-scoped
**Status**: TRACKED — implementation split across gastown-src (emitters) + formula (consumers).
**Root cause**: Two emitters both use the literal `"refinery"` channel; all rigs' refineries
share one channel dir and any rig's `MQ_SUBMIT` wakes all refineries.

**Emitter changes needed (this repo):**
- `internal/witness/handlers.go:344` — change `"refinery"` → `"refinery-" + rigName`
- `internal/cmd/sling_helpers.go:884` — change `"refinery"` → `"refinery-" + rigName`
  (rigName parameter already available in both call sites)

**Formula changes needed (NOT this repo):**
- `mol-refinery-patrol.formula.toml:1140` — change `--channel refinery` → `--channel refinery-<rig_prefix>`
  Requires formula variable support or a rig-aware template substitution.

**Deploy coordination required**: emitter change MUST be deployed atomically with formula
update, or refineries stop receiving events. Do NOT deploy emitter change alone.
**Needs**: founder sign-off before build (same scope as merge_queue TownSettings).

### hq-w3oj — Cross-rig convoy tracking beads don't auto-close
**Status**: TRACKED — medium complexity, needs careful convoy SDK work.
**Root cause**: `internal/cmd/close.go:273` calls `CheckConvoysForIssue(ctx, hqStore, ...)`.
`getTrackingConvoys(ctx, hqStore, issueID)` calls `hqStore.GetDependentsWithMetadata(ctx, issueID)`.
When `issueID` is a rig bead (e.g. `wkb-hat`), the HQ store's bead_dependencies table may not
have cross-DB dependency entries visible, so the `hq-cv-*` convoy tracking bead is never found
and never auto-closed. Result: stale `hq-cv-*` beads accumulate requiring manual mayor cleanup.

**Fix options (from bead hq-w3oj):**
1. After a rig bead closes, also query hq DB for convoy beads whose description references the closed ID.
2. Record hq convoy bead ID on the rig bead at sling time; refinery post-merge flow closes it explicitly.

**Fix location**: `internal/cmd/close.go` + `internal/convoy/operations.go` (getTrackingConvoys).

### hq-lmrqq — Refinery needs native PR + auto-merge path
**Status**: TRACKED — feature work, significant scope.
Branch-protected rigs (required checks + enforce_admins) prevent direct `git push origin main`.
Workaround: refineries manually run `gh pr create` + `gh pr merge --auto`.
**Gap**: refinery should (a) create PR + apply `--auto`, (b) watch for gh merge signal, (c) run
`gt mq` bookkeeping post-merge. First hit: NCA rig.
**Fix location**: `internal/cmd/refinery_*.go` or a new `internal/refinery/` package.
Prerequisite for rolling out branch protection to whiz_web and whiz_kb.
