# gt 1.2 Routing Identity Clean Package

Prepared: 2026-05-23 21:30 UTC

Scope: branch-hygiene evidence for `gt-12-routing-branch-cleanup`. This file records the clean routing identity package branch, supersession decisions, validation, and review passes. It does not add package content to the routing branch.

## Package Branch

| Field | Value |
| --- | --- |
| Clean package branch | `fork/polecat/ghoul/gt-1-2-candidate-routing-identity` |
| Clean package tip | `2023d9239d8cd699a6c4315f04341dd3497aa181` |
| Upstream PR base | `origin/main` at `625bcf8a92f9faef9804f73624a8bf770085ebd2` |
| Canonical source branch | `origin/integration/gt-1-2-routing-identity-gate-identity` |
| Canonical source tip | `21c5d9244d4a067b72df60de6c808672db9ca620` |
| Construction | One squashed commit on `origin/main`; package tree matches canonical source exactly. |

## History Cleanup Result

- Source history contains WIP/autosave/checkpoint commits: `afc7b553`, `617f8276`, `db9f2c64`, and `a752ac97`.
- The clean branch has exactly one commit ahead of `origin/main`: `2023d923 fix: package gt 1.2 routing identity candidate (gt-12-clean-subepic-pr-branches)`.
- `git diff --name-only fork/polecat/ghoul/gt-1-2-candidate-routing-identity origin/integration/gt-1-2-routing-identity-gate-identity` is empty, so no routing content was lost during squash cleanup.

## Supersession Story

| PR | Disposition | Routing identity package decision |
| --- | --- | --- |
| `#4086` `fix: block rig add prefix route hijacks` | Closed, superseded | Tracked-prefix hijack guard, checked route append, rollback, and route tests are preserved by clean main-target replacement `#4096`; do not retarget or merge `#4086`. |
| `#4088` `test: cover newly-created rig bead sling routing` | Closed, superseded | Newly-created rig bead sling routing smoke coverage is preserved by `#4096`; do not retarget or merge `#4088`. |
| `#4092` `fix: converge routing sling safeguards` | Closed, superseded | Useful `#4092`/`#4086`/`#4088` route-registration and sling-routing work is preserved by `#4096`; do not retarget or merge `#4092`. |
| `#4096` `fix: rebuild routing convergence for main` | Open main-target replacement | Replacement path for the superseded integration-target routing PRs; later folded into the routing identity integration path before clean package squashing. |
| `#4110` `Merge: gt-12-formula-identity-tests` | Merged internal integration PR | Formula identity tests are merged into canonical source `origin/integration/gt-1-2-routing-identity-gate-identity`. |

## Research Pass Log

1. Read `bd show gt-12-routing-branch-cleanup` for scope, dependencies, and acceptance criteria.
2. Checked the polecat hook and inbox; no handoff messages or merge-rejection notes were present.
3. Searched the worktree for routing, formula, and release evidence artifacts.
4. Read `docs/release/gt-1.2-subepic-branch-candidates.md` to identify the existing clean routing candidate.
5. Read `docs/release/gt-1.2-release-evidence.md` to verify current PR disposition evidence.
6. Read routing-related test files under `internal/cmd` to identify targeted validation candidates.
7. Queried `#4096` metadata, comments, commits, and check rollup to confirm it is the main-target routing replacement.
8. Queried `#4086` metadata and comments to confirm tracked-prefix work was closed as superseded by `#4096`.
9. Queried `#4088` metadata and comments to confirm sling-routing smoke coverage was closed as superseded by `#4096`.
10. Queried `#4092` metadata and comments to confirm converged routing safeguards were closed as superseded by `#4096`.
11. Queried `#4110` metadata and commits to confirm formula identity tests merged into the canonical routing source branch.
12. Listed `origin/main..origin/integration/gt-1-2-routing-identity-gate-identity` history and confirmed noisy WIP/autosave/checkpoint commits exist in the source lineage.
13. Listed `origin/main..origin/integration/gt-1-2-convergence-cleanup` history to keep this evidence branch scoped to the requested integration target.
14. Compared routing source changed-file stats and file names to identify the exact package surface.
15. Validated the clean candidate branch: one commit ahead of `origin/main`, merge-base equals `625bcf8a`, and tree comparison against the canonical source is empty.

## Pre-Implementation Review Log

1. Scope review: the implementation should record routing branch hygiene only; no routing package source changes are needed because the clean candidate already tree-matches the canonical source.
2. Base review: the clean package branch is based on `origin/main`, while this evidence commit targets `integration/gt-1-2-convergence-cleanup` for merge-queue bookkeeping.
3. History review: WIP/autosave/checkpoint commits must remain absent from the package branch; the one-commit squash satisfies this without force-pushing stale refs.
4. Supersession review: `#4086`, `#4088`, and `#4092` must remain closed superseded artifacts, with `#4096` and the routing identity gate as the replacement path.
5. Diff review: a single focused release manifest is sufficient; broad docs or unrelated code changes would violate the packaging scope.

## Targeted Validation

- `git rev-list --count origin/main..fork/polecat/ghoul/gt-1-2-candidate-routing-identity` returned `1`.
- `git merge-base fork/polecat/ghoul/gt-1-2-candidate-routing-identity origin/main` returned `625bcf8a92f9faef9804f73624a8bf770085ebd2`.
- `git diff --quiet fork/polecat/ghoul/gt-1-2-candidate-routing-identity origin/integration/gt-1-2-routing-identity-gate-identity` exited `0`.
- From a detached worktree at `fork/polecat/ghoul/gt-1-2-candidate-routing-identity`, `go test ./internal/cmd -run 'TestFormulaConvoyIDUsesTownConvoyPrefix|TestExecuteConvoyFormulaCreatesTownConvoyAndRigLegs|^TestSlingNewlyCreatedRigBeadRoutesBDCommandsToTargetRig$' -count=1` passed.
- From the same clean-branch worktree, `go test ./internal/beads -run 'TestAppendRouteRejectsCrossRigPrefixRewrite|TestAppendRouteIfPrefixAvailable|TestCheckPrefixAvailable|TestWriteRoutesWaitsForRoutesLock' -count=1` passed.
- From the same clean-branch worktree, `go test ./internal/rig -run 'TestAddRig_TrackedBeadsPrefixCollisionDoesNotRewriteRoute|TestAddRig_RollsBackRouteWhenRegistrationFails' -count=1` passed.
- From the same clean-branch worktree, `make build` passed.
- On this evidence branch, `make build` passed with only this release manifest changed.

## Post-Implementation Review Log

1. Branch-shape review: the clean routing package branch remains one commit on `origin/main` and contains no WIP/autosave/checkpoint commit subjects.
2. Tree-equivalence review: the package branch tree still matches the canonical source branch exactly.
3. Supersession review: `#4086`, `#4088`, and `#4092` are recorded as closed superseded paths, with `#4096` and `#4110` evidence explicit.
4. Scope review: only this release manifest is added on the merge-queue branch; routing source files and broad docs are untouched.
5. Validation review: targeted routing/formula tests and build are recorded from the clean package branch, and the evidence branch build is recorded separately.
