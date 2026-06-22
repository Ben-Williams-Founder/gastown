# Gas Town — Diagnosis & Improvement Record

Durable record of gastown (gt-fork) reliability problems observed in production
operation of the Whiz town, with the methodology behind each fix:
**problem → evidence → root cause → fix (+ branch) → verification.**

This complements `docs/dolt-health-guide.md` (Dolt-specific health) and the
`.claude/rules/` operational rules. Append a dated section per run; never
rewrite history.

---

## 2026-06-22 — Completion-path reliability (three fork fixes) + box findings

All three fixes target the same failure class observed live this run: the town
was doing real work but **failing to *land* it cleanly**, producing re-dispatch
churn, stranded work, and false `NEEDS_RECOVERY`. The cost was amplified by CPU
contention on this box (a starved town has no spare capacity to absorb churn),
so completion-path correctness is a force-multiplier, not a nicety.

Collected branch this run: `town-rebase-2026-06-22-reaperfix` (each fix also
lives on its own topic branch, below). gastown-src commits — local only, **not
pushed** (fork-patch policy: patches stay on the fork branch).

### Fix 1 — Work bead doesn't close on PR/MR merge → re-dispatch churn

- **Branch:** `fix/bead-close-on-merge`  ·  **Commit:** `ec219ce6`
- **Symptom (live):** A polecat's MR/PR merged, but its work bead stayed OPEN.
  The scheduler then re-dispatched a *new* polecat onto already-merged work; the
  polecat redid the work and wedged in `NEEDS_RECOVERY`. Observed this session as
  repeated re-dispatch churn — wasted polecat spawns on a CPU-starved box, each
  one a casualty competing for the same scarce cores.
- **Root cause:** `HandleMRInfoSuccess` (the deterministic merge-success handler,
  reached only via `result.Success` in `batch.go`) closed the source issue *only*
  inside `if mr.SourceIssue != ""`. When the MR bead carried no resolvable
  `source_issue`, the merge succeeded but nothing closed the work bead — it
  stayed open and was re-slung. There was no fallback to the worker agent bead's
  `active_mr → work-bead` mapping, which is exactly the link that survives when
  `source_issue` is absent.
- **Fix:** Extract `closeMergedWorkBead(bd, out, mr, mergeCommit)`, called from
  `HandleMRInfoSuccess` on **real merge success only** (`HandleMRInfoFailure`
  never calls it, so rejects/conflicts still leave the bead open for re-submit).
  The helper: resolves the work bead via `source_issue` first, then falls back to
  the agent bead's `last_source_issue`; is **idempotent** (already-terminal bead =
  no-op, covering the polecat-already-ran-`gt done` race); force-closes to bypass
  open molecule-step deps (matching `gt done`); and never falsely reports "closed"
  on a hard error (re-checks terminal state first). A small `workBeadCloser`
  interface (`Show` + `ForceCloseWithReason`) makes it unit-testable without a
  live Dolt server.
- **Verification:** `internal/refinery/engineer_bead_close_test.go` (pure, no Dolt
  container): merge-success → source-issue bead closed; merge-success with empty
  `source_issue` → closed via the agent `active_mr` fallback; already-terminal
  bead → idempotent no-op; merge-failure path never closes (rejects re-submit).
- **Residual risk:** `Manager.PostMerge` (the `gt mq post-merge` CLI path) was
  left unchanged — it already closes `source_issue` and is idempotent but lacks
  the `active_mr` fallback. If a rig relies on that CLI path, an empty-`source_issue`
  MR could still churn there. PostMerge hardening is a candidate follow-up.

### Fix 2 — Polecat commit/submit unreliability → stranded uncommitted work

- **Branch:** `fix/polecat-submit-reliability`  ·  **Commit:** `0a2c7aa5`
- **Symptom (live):** Polecats were observed doing the work (writing
  implementation files) but the session turn ended before they ran
  `git commit && gt done`, stranding the work as **uncommitted changes** in the
  worktree → `NEEDS_RECOVERY` → work lost on reap.
- **Root cause:** There is already a Stop-hook safety net (`gt tap
  polecat-stop-check`, gas-lob) and `gt done` auto-commits a dirty worktree before
  submitting (gt-pvx). The Stop hook is the correct trigger — per the Claude Code
  hooks spec, `Stop` fires **only** on normal turn completion (never on
  context-limit/PreCompact, crash, or API error), so a Stop is a genuine
  completion signal. **The gap:** `polecat-stop-check` invoked `gt done` only when
  there were commits *ahead* of `origin/main` (`git rev-list --count
  origin/main..HEAD > 0`). In the stranding case the work is uncommitted, so the
  ahead-count is 0, the hook concluded "nothing to submit," and exited — leaving
  the work to rot.
- **Fix:** Extend the Stop-check decision to also detect uncommitted work. It now
  runs `gt done` when **either** there are commits ahead of `origin/main`
  **or** the working tree is dirty (`git status --porcelain` non-empty). `gt
  done`'s gt-pvx net then auto-commits the dirty work (filtering runtime/overlay
  artifacts, never committing deletions) and submits. Genuinely-WIP polecats are
  preserved because the trigger is the Stop hook itself, which only fires on
  normal completion — a context-limit/crash/interrupt does **not** fire Stop, so
  in-flight work still goes through the witness recovery path unchanged. Git
  errors **fail closed** (no submit) so a transient git failure never blocks
  session stop.
- **Verification:** Stop-hook decision unit-tested for the four cases —
  ahead-only → submit; dirty-only → submit (the stranding case); clean+even →
  no-op; git error → fail-closed (no submit). Behaviourally: a finished-but-
  uncommitted polecat now lands instead of stranding.

### Fix 3 — `bead-closed ⇒ SAFE_TO_NUKE` despite unpushed checkpoints (false NEEDS_RECOVERY)

- **Branch:** `fix/recovery-bead-closed-safe`  ·  **Commit:** `f3fcf981`
  (supersedes the partial `headTreeEqual` attempt `da8452b5`)
- **Symptom (live):** After a squash-merge where `origin/main` **advances past**
  the squash commit (later PRs land on top), a completed polecat's HEAD sits
  *behind* an advanced `origin/main`. The recovery predicate then false-flagged
  the polecat `NEEDS_RECOVERY` on **every completion**, blocking the reap and
  holding a finished polecat's slot — capacity loss on a box already at its
  contention ceiling.
- **Root cause:** The prior `headTreeEqual` fix used a 2-dot `git diff --quiet
  origin/main HEAD`; once `origin/main` advances, that diff is non-empty,
  `headTreeEqual` returns false, the code falls through to `git cherry`, and
  counts every pre-squash checkpoint as "unpushed" → false `NEEDS_RECOVERY`. The
  git tree heuristic is the wrong layer for this signal.
- **Fix:** Move the signal to the right layer. The reliable "work is safe" fact is
  that the polecat's **work bead is CLOSED** (MR merged → bead closed), not git
  tree gymnastics. Add `WorkstateInput.WorkBeadClosed` (wired from the existing
  `workTerminal = assigned||source||hook-terminal` signal in **both** the
  check-recovery and witness paths). When the bead is closed, `DecideWorkstate`
  suppresses **only** the unpushed-commits blocker (the pre-squash checkpoints
  whose content already merged). Dirty/stash/uncommitted state, `push_failed`,
  `mr_failed`, and an open active-MR **still block**, so live WIP is never lost
  and open beads still recover.
- **Verification:** `internal/polecat/workstate_test.go` — closed bead + 36
  unpushed + clean ⇒ `SAFE_TO_NUKE` (the live case); regressions: open bead +
  unpushed ⇒ `NEEDS_RECOVERY`; closed bead + dirty/stash ⇒ still flags.
  `internal/git/git_test.go::TestBranchPreservationSquashThenAdvancedOriginStillFlags`
  proves the git heuristic *alone* still false-flags squash+advanced-origin work,
  documenting *why* the bead-layer signal is required (the prior fix's test only
  covered the origin-not-advanced case). Touched:
  `internal/cmd/polecat.go`, `internal/polecat/{workstate,reuse}.go`,
  `internal/witness/handlers.go`, plus the two test files.

### Fix 3b (process, not gt-fork code) — DEC-projection CI gate blocked DEC work from landing

This is logged here because it is the same "can't land the work" failure class,
but the fix is in the **Whiz-KB repo + author workflow**, not gastown-src.

- **Repo/gate:** `Whiz-KB/.github/workflows/build.yml` step *"Verify
  DECISIONS-FOR-REVIEW.md is a current projection"* → `python
  tools/build_decisions_for_review.py --check`.
- **Symptom (live):** Whiz-KB **PR #395** ("New DEC + stub: payment/billing
  provider choice — phase ladder + mock invoice/payment adapter", `wkb-rovf`) was
  rejected/closed: it added a DEC node but did **not** regenerate
  `DECISIONS-FOR-REVIEW.md`, so the projection drift-check failed CI and the DEC
  work could not land.
- **Root cause:** `DECISIONS-FOR-REVIEW.md` is **generated** from the decision
  nodes (the SSOT) by `tools/build_decisions_for_review.py` and must never be
  hand-edited. The `--check` gate (added under wkb-9793 to kill the hand-append
  merge contention that previously stalled the queue) fails the build whenever a
  PR touches a decision node without regenerating the projection. A DEC-authoring
  polecat that edits the node but skips the regen step trips the gate every time.
- **Fix (author-side, deterministic):** Any PR that adds/edits a decision node
  must run `python tools/build_decisions_for_review.py` and commit the regenerated
  projection in the same PR. This belongs in the DEC-authoring dispatch context /
  polecat instructions so DEC beads regenerate-then-commit by default, rather than
  being rejected at CI. (Candidate gt-side improvement: surface this as a
  pre-submit `gt done` check in KB rigs, mirroring the gt-pvx auto-commit net, so
  the projection is refreshed before MR submission.)
- **Verification:** Re-running `build_decisions_for_review.py` then `--check`
  passes (no drift) on a correctly-authored PR; the gate is doing its job
  (catching un-regenerated DEC edits), so the durable fix is process, not loosening
  the gate.

---

## Broader findings this run (box ceiling, CI contention, actuator tuning)

These are the systemic constraints that made the completion-path bugs expensive,
and the levers to raise throughput. Live evidence captured under GOAL `hq-p1uw`
(24h token-burn) and bead `hq-hsjb`.

### Sustainable polecat ceiling: ~3 on a 14-core / 1-runner box

- The box's token-consumption ceiling is gated by **CI↔polecat CPU contention**
  on this single 14-core box, **not** by token capacity. Observed live: 1 serial
  self-hosted runner + heavy `whiz_web` CI (next / playwright / scancode) drove
  load to **~12.8**, tripping the governor load-gate (backstop 10) → polecat
  dispatch HOLDs → only **~3 of 8** slots actively consuming. Treat ~3 governed
  polecats as the *sustainable* concurrent floor on this hardware; more cores
  (a bigger A1) is the real lever to consume the token budget.
- Work-mix corollary: `whiz_kb` (light markdown-compile CI) cycles fast and lands
  value; `whiz_web` jams behind the serial runner. Biasing the mix toward
  light-CI rigs raises effective throughput on a contended box.

### CI contention — `hq-hsjb`: github-runner is in `app.slice`, not `ci.slice`

- The self-hosted runner runs in `app.slice/github-runner.service`, **outside**
  the cgroup tiers the box-optimizer actuator throttles
  (`polecat.slice` T2 + `ci.slice` T3). So when CI saturates the box (observed:
  2× `scancode --license` at 99% CPU each, ungoverned, load 12.8, 5 polecats
  starved), the actuator **cannot** throttle CI to free cores for the token-burn.
- **Fix (open):** place `github-runner.service` under `ci.slice` via a systemd
  drop-in (`Slice=ci.slice`) so the actuator's PSI-throttle governs CI as the T3
  tier it's meant to be; then CI contention self-regulates against the burn.
  Verify `cat /proc/<runner-pid>/cgroup` shows `ci.slice` and that the actuator
  throttles it under PSI. Do **not** restart the runner mid-CI-job.

### Actuator tuning gap: load-gate (admission) vs PSI-gate (throttle) mismatch

- The **admission** gate trips on **LOAD ≥ 10**, but the **actuator** only
  throttles on **PSI-cpu ≥ 60**. In the observed regime, `load=12.8` while
  `PSI-cpu≈38` — so dispatch HOLDs (no new polecats) yet **nothing throttles** the
  ungoverned CI runner to relieve the load. The two control loops key off
  different signals at incompatible thresholds, producing a stall with no
  corrective actuation.
- **Implication / lever:** unify the signals (or align thresholds) so that when
  the load-gate HOLDs dispatch, the actuator is also actuating (throttling T3 CI)
  to drain the load that caused the HOLD. Combined with `hq-hsjb` (runner →
  `ci.slice`), this closes the loop: HOLD → throttle CI → load falls → dispatch
  resumes. Until then, expect the town to sit at ~3 polecats whenever `whiz_web`
  CI is hot.

---
