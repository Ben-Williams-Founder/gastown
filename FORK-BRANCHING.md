# Fork branching model — Ben-Williams-Founder/gastown

This is a **maintained fork** of `gastownhall/gastown`. We track upstream and
carry our own patches, following the standard maintained-fork best practice
(main = clean upstream mirror; patches rebased on top).

## Branches

| Branch | What it is | Build/deploy from it? |
|---|---|---|
| **`main`** | A **clean mirror of `gastownhall/gastown` `main`** (0 ahead / 0 behind). For tracking upstream + clean diffs only. **Never commit patches here.** | ❌ **NO** — building `main` yields a binary missing our patches |
| **`town`** | **Canonical source** = `upstream/main` + our patches, rebased on top. | ✅ **YES** — all builds, deploys, and CI come from `town` |

We **never push to gastownhall** (one-way fork). Upstream ingestion is deliberate.

## ⚠️ The trap
Building from `main` produces a **broken binary** (~38–51M, mis-resolves rigs →
`hq-wisp`, dispatch stalls) because `main` is pristine upstream *without our deps/
patches*. A **healthy build is ~208M** and `gt sling` produces `web-wisp`.
**Always build from `town`; functional-test `gt sling` before deploying.**

## Sync workflow (ingesting upstream)
```sh
git fetch upstream
git branch -f main upstream/main && git push -f origin main   # main = clean mirror
git checkout town && git rebase main                          # re-apply patches on fresh upstream
# fix any patch that no longer applies; then build from town:
CGO_ENABLED=1 go build -ldflags '-linkmode external -extldflags "-static"' -o gt ./cmd/gt
# verify ~208M + `gt sling` → web-wisp, then deploy via atomic-rename.
```

## Patches on `town` (keep this list current)
refinery merge-queue-config, patrol ephemeral-wisp query, witness recovery
predicate, recovery SAFE_TO_NUKE, polecat submit-uncommitted, refinery
close-on-real-merge, reaper non-mutating scan, dispatch schema_version envelope,
go1.26.4 toolchain pin, **done close-on-merge hold**, role-image CI.
