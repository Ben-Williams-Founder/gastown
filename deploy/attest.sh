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

LIVE_BIN="${1:-}"; LIVE_COMMIT="${2:-}"
[ -n "$LIVE_BIN" ] && [ -n "$LIVE_COMMIT" ] || { echo "usage: attest.sh <live-gt-binary> <live-lineage-commit> [out-dir]" >&2; exit 2; }
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
MISSING="$(comm -23 \
  <("$GO" tool nm "$LIVE_BIN"  2>/dev/null | awk '{print $NF}' | LC_ALL=C sort -u) \
  <("$GO" tool nm "$CAND"      2>/dev/null | awk '{print $NF}' | LC_ALL=C sort -u) | wc -l)"
if [ "$MISSING" -ne 0 ]; then
  echo "FAIL G1: $MISSING live symbols MISSING from candidate — a live fix would be dropped. DO NOT DEPLOY." >&2
  exit 1
fi
echo "   G1 PASS: 0 live symbols missing"

echo "== G2 fork-patch completeness (candidate must carry every lineage patch) =="
"$HERE/verify-fork-patches.sh" "$CAND" "$HEAD_COMMIT" "$REPO" "$HERE/fork-patch-signatures.tsv" || {
  echo "FAIL G2: fork-patch completeness — see above. DO NOT DEPLOY." >&2; exit 1; }

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
  "gates": { "supersetMissingSymbols": 0, "forkPatches": "PASS" },
  "builder": "attest.sh",
  "buildFinishedOn": "$TS"
}
EOF
echo "== attestation emitted: $OUT/attestation.json (id $ATTEST_ID) =="
