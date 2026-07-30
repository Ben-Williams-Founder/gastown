#!/usr/bin/env bash
# coherence-check.sh <gt-binary> <manifest> [signatures-tsv]
#
# NFR-01: ONE command proving the three provenance surfaces agree:
#   (1) the TSV on disk        — sha256(fork-patch-signatures.tsv)
#   (2) the binary's own stamp — gt version --provenance (patchSetHash, attestationId)
#   (3) the generated manifest — patchSetHash / attestationId / binarySha256
# Exit 0 iff all pairwise checks hold; otherwise names every mismatch (exit 1).
# A hand-edited manifest, a drifted TSV, or a foreign/unattested binary all fail loudly.
set -uo pipefail

BIN="${1:-}"; MAN="${2:-}"
HERE="$(cd "$(dirname "$0")" && pwd)"
TSV="${3:-$HERE/fork-patch-signatures.tsv}"
[ -n "$BIN" ] && [ -n "$MAN" ] || { echo "usage: coherence-check.sh <gt-binary> <manifest> [signatures-tsv]" >&2; exit 2; }
for f in "$BIN" "$MAN" "$TSV"; do [ -f "$f" ] || { echo "FAIL: not found: $f" >&2; exit 2; }; done

fail=0
say_fail() { echo "MISMATCH: $*" >&2; fail=1; }

PROV="$("$BIN" version --provenance 2>/dev/null)" || { echo "FAIL: binary does not support 'version --provenance'" >&2; exit 1; }
pget() { sed -n "s/^$1=//p" <<<"$PROV" | head -1; }
mget() { sed -n "s/^$1: *//p" "$MAN" | head -1; }

grep -qxF "attested=true" <<<"$PROV" || say_fail "binary is UNATTESTED (built outside the gated deploy path)"

TSV_SHA="$(sha256sum "$TSV" | awk '{print $1}')"
BIN_SHA="$(sha256sum "$BIN" | awk '{print $1}')"

[ "$(pget patchSetHash)"  = "$TSV_SHA" ]              || say_fail "binary patchSetHash != sha256(TSV on disk)  [binary=$(pget patchSetHash | cut -c1-12) tsv=${TSV_SHA:0:12}]"
[ "$(mget patchSetHash)"  = "$(pget patchSetHash)" ]  || say_fail "manifest patchSetHash != binary stamp"
[ "$(mget attestationId)" = "$(pget attestationId)" ] || say_fail "manifest attestationId != binary stamp"
[ "$(mget binarySha256)"  = "$BIN_SHA" ]              || say_fail "manifest binarySha256 != sha256(binary)  [manifest=$(mget binarySha256 | cut -c1-12) actual=${BIN_SHA:0:12}]"

if [ "$fail" -ne 0 ]; then
  echo "COHERENCE: FAIL — provenance surfaces disagree (see mismatches above)." >&2
  exit 1
fi
echo "COHERENCE: PASS — TSV, binary stamp, and manifest agree (patchSet ${TSV_SHA:0:12}, binary ${BIN_SHA:0:12})."
