#!/usr/bin/env bash
# install-tool.sh <src-file> <dest-path>
#
# FR-02 no-clobber installer for fork-hosted operational tooling (gt-idle-check,
# verify-fork-patches.sh, ...). Used by bootstrap wiring instead of bare `cp`
# (a naive restore can overwrite a NEWER live hotfix while believing it is
# "restoring backup" — adversarial finding, COA-A pass).
#
# Rules:
#   - Fresh install (no dest): stage on same fs + atomic mv; record installed hash.
#   - Update where dest == repo copy: no-op (already current).
#   - Update where dest == last-installed hash: normal update (live copy is ours,
#     unmodified) — atomic rename + record.
#   - 3-WAY REFUSE: dest differs from BOTH the repo copy AND the recorded
#     last-installed hash => the live copy was hotfixed out-of-band. REFUSE and
#     demand a human diff — never silently clobber live changes.
# State: <dest>.installed-sha256 (hash recorded at install time).
set -euo pipefail

SRC="${1:-}"; DEST="${2:-}"
[ -n "$SRC" ] && [ -n "$DEST" ] || { echo "usage: install-tool.sh <src-file> <dest-path>" >&2; exit 2; }
[ -f "$SRC" ] || { echo "FAIL: source not found: $SRC" >&2; exit 2; }

STATE="$DEST.installed-sha256"
h() { sha256sum "$1" | awk '{print $1}'; }
SRC_H="$(h "$SRC")"

atomic_install() {
  local dir stage
  dir="$(dirname "$DEST")"; mkdir -p "$dir"
  stage="$dir/.install-staging-$$"
  cp "$SRC" "$stage"; chmod --reference="$SRC" "$stage" 2>/dev/null || chmod +x "$stage"
  mv -f "$stage" "$DEST"                      # atomic rename, never cp-onto-live
  printf '%s\n' "$SRC_H" > "$STATE"
}

if [ ! -f "$DEST" ]; then
  atomic_install; echo "INSTALLED (fresh): $DEST (${SRC_H:0:12})"; exit 0
fi

DEST_H="$(h "$DEST")"
if [ "$DEST_H" = "$SRC_H" ]; then
  printf '%s\n' "$SRC_H" > "$STATE"           # refresh state; content already current
  echo "CURRENT: $DEST already matches source (${SRC_H:0:12})"; exit 0
fi

LAST_H="$(cat "$STATE" 2>/dev/null || true)"
if [ -n "$LAST_H" ] && [ "$DEST_H" = "$LAST_H" ]; then
  atomic_install; echo "UPDATED: $DEST (${LAST_H:0:12} -> ${SRC_H:0:12})"; exit 0
fi

echo "REFUSED (3-way): $DEST differs from BOTH the repo copy and the last-installed hash." >&2
echo "  live=$DEST_H" >&2
echo "  repo=$SRC_H" >&2
echo "  last-installed=${LAST_H:-<none recorded>}" >&2
echo "  The live copy appears hotfixed out-of-band. Diff + reconcile (fold the hotfix into the repo), then re-run." >&2
exit 1
