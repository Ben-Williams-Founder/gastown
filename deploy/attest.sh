#!/usr/bin/env bash
# attest.sh <live-gt-binary> <live-lineage-commit> [out-dir]
#
# Runs the two hard verification gates against a candidate tree (the CURRENT
# HEAD of the repo this script lives in) and, ONLY if both pass, emits
# attestation.json recording exactly what was verified. deploy-gt.sh refuses to
# build without an attestation whose treeHash matches HEAD — so a tree that was
# never verified cannot become a stamped binary.
#
# Gates:
#   G1 superset-verify — candidate binary (built here from HEAD) must contain
#      every symbol of the LIVE binary (0 missing => no live functionality
#      silently dropped; the >=4x-recurring failure class).
#   G2 fork-patch completeness — deploy/verify-fork-patches.sh: every commit on
#      merge-base(MAIN_REF, <live-lineage-commit>)..HEAD accounted for in
#      deploy/fork-patch-signatures.tsv (signature present in candidate, or
#      explicit waive). Unmapped commit = RED; dropped signature = RED.
#
# Emits (out-dir, default deploy/.attest/):
#   attestation.json  — schema gt-attest/v1 (SLSA-aligned field names, no framework)
#   cand-gt           — the gate-built candidate binary (deploy-gt.sh rebuilds
#                       with the stamp; symbols are compared pre-stamp)
set -euo pipefail

ALLOW_DEP_CHURN=""
if [ "${1:-}" = "--allow-dep-churn" ]; then
  ALLOW_DEP_CHURN="${2:?--allow-dep-churn requires a reason string}"; shift 2
fi
LIVE_BIN="${1:-}"; LIVE_COMMIT="${2:-}"
[ -n "$LIVE_BIN" ] && [ -n "$LIVE_COMMIT" ] || { echo "usage: attest.sh [--allow-dep-churn <reason>] <live-gt-binary> <live-lineage-commit> [out-dir]" >&2; exit 2; }
[ -f "$LIVE_BIN" ] || { echo "FAIL: live binary not found: $LIVE_BIN" >&2; exit 2; }

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
OUT="${3:-$HERE/.attest}"
mkdir -p "$OUT"
GO="${GO:-$HOME/.local/go/bin/go}"; command -v "$GO" >/dev/null 2>&1 || GO="go"

# Refuse a dirty tree: the attestation must describe exactly one tree hash.
if [ -n "$(git -C "$REPO" status --porcelain --untracked-files=no)" ]; then
  echo "FAIL: worktree has uncommitted tracked changes — commit first (attestation must pin one tree)" >&2; exit 1
fi
HEAD_COMMIT="$(git -C "$REPO" rev-parse HEAD)"
TREE_HASH="$(git -C "$REPO" rev-parse 'HEAD^{tree}')"

echo "== attest: building candidate from HEAD ${HEAD_COMMIT:0:9} (tree ${TREE_HASH:0:9}) =="
CAND="$OUT/cand-gt"
( cd "$REPO" && "$GO" build -o "$CAND" ./cmd/gt )   # ambient CGO: live gt embeds dolt (cgo-gated); CGO_ENABLED=0 here cost a 134k-symbol false candidate (run-5 lesson)

echo "== G1 superset-verify: live symbols ⊆ candidate symbols =="
MISSLIST="$OUT/g1-missing.txt"
comm -23 \
  <("$GO" tool nm "$LIVE_BIN"  2>/dev/null | awk '{print $NF}' | LC_ALL=C sort -u) \
  <("$GO" tool nm "$CAND"      2>/dev/null | awk '{print $NF}' | LC_ALL=C sort -u) > "$MISSLIST"
# Four-way triage (run-5 lesson: strict-0 is the wrong contract under dep bumps,
# but loosening silently would be a false-green vector):
#   gastown symbols  — fork functionality: missing => ALWAYS FATAL.
#   stamp artifacts  — the 7 -X ldflags provenance vars: counted, never fatal (see below).
#   compiler noise   — $f32./$f64./..stmp_N numbering churn: counted, never fatal.
#   dependency syms  — FATAL unless the operator explicitly declares the churn
#                      via --allow-dep-churn "<reason>"; declaration + counts are
#                      recorded in the attestation (auditable, fail-closed default).
#
# STAMP-ARTIFACT CLASS (added during the hq-vcg3 run): once the FIRST attested
# binary went live, G1 began self-blocking EVERY subsequent attestation.
# deploy-gt.sh links the live binary with `-X internal/cmd.<Var>=<value>` for the 7
# provenance vars, which are declared `= ""` in source. The linker emits a `.str`
# data symbol for an injected value but NOT for an empty default — so the LIVE
# (stamped) binary permanently carries 7 `.str` symbols that the attest candidate
# (built plain, by design, so symbols are compared pre-stamp) can never have. Those
# were counted as gastown-loss => ALWAYS FATAL: a permanent false RED unrelated to
# any code change. Empirically confirmed by rebuilding the SAME tree WITH the stamp
# and re-running this comparison: total=0 missing, so no functionality is involved.
#
# Fail-closed by construction: this is an ANCHORED, EXPLICITLY ENUMERATED 7-symbol
# allowlist — exact package, exact var names, exact `.str` suffix. Any other gastown
# symbol loss stays fatal, including any other symbol on these same vars. A deleted
# or renamed stamp var cannot sneak through either: deploy-gt.sh's self-check
# refuses unless the stamped binary actually reports attested=true plus the expected
# verifiedBase / patchSetHash / attestationId.
STAMP_RE='^github\.com/steveyegge/gastown/internal/cmd\.(VerifiedBase|PatchSetHash|AttestationID|Version|Commit|Branch|Build)\.str$'
# GASTOWN-PACKAGE COMPILER TEMPORARIES (added during the hq-ud8e run): Go emits
# `..stmp_N` static-temporary data symbols inside the owning package's namespace,
# so they carry the gastown path prefix and were double-counted — matched by the
# compiler-artifact class below ("counted, never fatal" per the run-5 triage) AND
# by the gastown-fatal class (which only exempted stamp vars). Net effect: ANY
# source edit in a gastown package renumbers its temporaries and G1 self-blocks
# with a false "gastown symbols MISSING" (observed: 118 misses, dep=-118 from the
# same double-count). Empirical confirmation, mirroring the stamp-class method:
# a candidate built from the UNCHANGED live tree shows 0 ..stmp misses (only the
# 7 stamp vars), while the edited tree's candidate GAINS matching renumbered
# temporaries (118 old -> 121 new) — numbering churn, no functionality involved.
# Fail-closed by construction: the exemption is the anchored `..stmp_N` suffix
# class ONLY — every named gastown function/data symbol remains ALWAYS FATAL.
GAST_TMP_RE='^github\.com/steveyegge/gastown/.*\.\.stmp_[0-9]+$'
GAST_ALL="$(grep -c "steveyegge/gastown" "$MISSLIST" || true)"
STAMP_MISS="$(grep -cE "$STAMP_RE" "$MISSLIST" || true)"
GAST_TMP_MISS="$(grep -cE "$GAST_TMP_RE" "$MISSLIST" || true)"
GAST_MISS=$((GAST_ALL - STAMP_MISS - GAST_TMP_MISS))
ART_MISS="$(grep -cE '^\$f(32|64)\.|\.\.stmp_[0-9]+$|^go:itab\.' "$MISSLIST" || true)"   # go:itab = linker-generated (run-5 evidence)
TOT_MISS="$(wc -l < "$MISSLIST")"
DEP_MISS=$((TOT_MISS - GAST_ALL - (ART_MISS - GAST_TMP_MISS)))
echo "   missing: total=$TOT_MISS gastown=$GAST_MISS dep=$DEP_MISS compiler-artifact=$ART_MISS (gastown-pkg-tmp=$GAST_TMP_MISS) stamp-artifact=$STAMP_MISS"
if [ "$GAST_MISS" -ne 0 ]; then
  grep "steveyegge/gastown" "$MISSLIST" | grep -vE "$STAMP_RE" | grep -vE "$GAST_TMP_RE" | head -10
  echo "FAIL G1: $GAST_MISS gastown symbols MISSING — fork functionality would be dropped. DO NOT DEPLOY." >&2
  exit 1
fi
if [ "$DEP_MISS" -gt 0 ] && [ -z "$ALLOW_DEP_CHURN" ]; then
  grep -vE '^\$f(32|64)\.|\.\.stmp_[0-9]+$|steveyegge/gastown' "$MISSLIST" | sed 's/\..*//' | sort | uniq -c | sort -rn | head -8
  echo "FAIL G1: $DEP_MISS dependency symbols missing and no --allow-dep-churn declaration. If this is a declared dep bump, re-run with --allow-dep-churn \"<reason>\". DO NOT DEPLOY undeclared." >&2
  exit 1
fi
echo "   G1 PASS: gastown=0 missing; dep churn ${DEP_MISS} $( [ -n "$ALLOW_DEP_CHURN" ] && echo "(declared: $ALLOW_DEP_CHURN)" )"

echo "== G2 fork-patch completeness (candidate must carry every lineage patch) =="
"$HERE/verify-fork-patches.sh" "$CAND" "$HEAD_COMMIT" "$REPO" "$HERE/fork-patch-signatures.tsv" || {
  echo "FAIL G2: fork-patch completeness — see above. DO NOT DEPLOY." >&2; exit 1; }

# Rebase-deferral trigger (DEC-OPS-gt-fork-rebase-deferral — Tier-1 mechanization):
# every gated rebuild prints the upstream delta; >500 behind flips the DEC to revisit.
BEHIND="$(git -C "$REPO" rev-list --count "$(git -C "$REPO" merge-base "${GT_MAIN_REF:-origin/main}" HEAD)"..${GT_MAIN_REF:-origin/main} 2>/dev/null || echo '?')"
echo "== rebase-deferral trigger: ${BEHIND} commits behind upstream =="
echo "   predicates: (A) CVE fixable only upstream? (B) needed upstream fix in an open bead? Either => open the rebase."
if [ "${BEHIND:-0}" != "?" ] && [ "${BEHIND:-0}" -gt 500 ]; then
  echo "   ⚠ TRIGGER FIRED: >500 behind — flip DEC-OPS-gt-fork-rebase-deferral to revisit." >&2
fi

PATCHSET_HASH="$(sha256sum "$HERE/fork-patch-signatures.tsv" | awk '{print $1}')"
TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
# Deterministic id over the verified facts (not the timestamp).
ATTEST_ID="$(printf '%s\n%s\n%s\n%s\n' "$TREE_HASH" "$HEAD_COMMIT" "$LIVE_COMMIT" "$PATCHSET_HASH" | sha256sum | awk '{print $1}' | cut -c1-16)"

cat > "$OUT/attestation.json" <<EOF
{
  "schema": "gt-attest/v1",
  "attestationId": "$ATTEST_ID",
  "treeHash": "$TREE_HASH",
  "commit": "$HEAD_COMMIT",
  "verifiedBase": "$LIVE_COMMIT",
  "patchSetSha256": "$PATCHSET_HASH",
  "gates": { "supersetGastownMissing": 0, "supersetDepMissing": $DEP_MISS, "supersetArtifactMissing": $ART_MISS, "supersetStampArtifactMissing": $STAMP_MISS, "depChurnDeclared": "${ALLOW_DEP_CHURN:-none}", "forkPatches": "PASS" },
  "builder": "attest.sh",
  "buildFinishedOn": "$TS"
}
EOF
echo "== attestation emitted: $OUT/attestation.json (id $ATTEST_ID) =="
