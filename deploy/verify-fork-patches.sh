#!/usr/bin/env bash
# verify-fork-patches.sh <gt-binary> <live-commit> [src-repo] [manifest.tsv]
#
# COMPLETENESS guard (hq-qk6x). Derives the fork patch set from git
# (`git rev-list --no-merges <merge-base origin/main..LIVE>..LIVE`) and requires
# EVERY commit to be accounted for in fork-patch-signatures.tsv — as a signature
# present in the binary, or an explicit `waive`. Fails (exit 1) if any commit is
# UNMAPPED (unguarded patch) or any mapped signature is MISSING (dropped patch).
# This replaces the earlier presence-only check that went green while 17/21 patches
# (incl. the Go CVE bumps) were unguarded. Run as a HARD pre-install gate.
set -uo pipefail

BIN="${1:-}"; LIVE="${2:-}"
SRC="${3:-$(cd "$(dirname "$0")/.." && pwd)}"
MAN="${4:-$(cd "$(dirname "$0")" && pwd)/fork-patch-signatures.tsv}"
if [ -z "$BIN" ] || [ -z "$LIVE" ]; then
  echo "usage: verify-fork-patches.sh <gt-binary> <live-commit> [src-repo] [manifest.tsv]" >&2; exit 2
fi
[ -f "$BIN" ] || { echo "FAIL: binary not found: $BIN" >&2; exit 2; }
[ -f "$MAN" ] || { echo "FAIL: manifest not found: $MAN" >&2; exit 2; }
[ -d "$SRC/.git" ] || { echo "FAIL: not a git repo: $SRC" >&2; exit 2; }
GO="${GO:-$HOME/.local/go/bin/go}"; command -v "$GO" >/dev/null 2>&1 || GO="go"

BASE="$(git -C "$SRC" merge-base "${GT_MAIN_REF:-origin/main}" "$LIVE" 2>/dev/null)" || { echo "FAIL: cannot merge-base ${GT_MAIN_REF:-origin/main}..$LIVE (fetch origin?)" >&2; exit 2; }
mapfile -t PATCHES < <(git -C "$SRC" rev-list --no-merges "$BASE".."$LIVE" 2>/dev/null)
[ "${#PATCHES[@]}" -gt 0 ] || { echo "FAIL: no patches derived for $LIVE (bad commit?)" >&2; exit 2; }

SYMS="$("$GO" tool nm "$BIN" 2>/dev/null | awk '{print $NF}')"
STRS="$(strings "$BIN" 2>/dev/null)"
TOOLV="$("$GO" version "$BIN" 2>/dev/null)"

# Load manifest into an assoc array keyed by shortsha -> "kind<TAB>value".
declare -A MAP
while IFS=$'\t' read -r sha kind val subj; do
  case "$sha" in ''|'#'*) continue;; esac
  [ -z "${kind:-}" ] && continue
  MAP["$sha"]="$kind"$'\t'"$val"
done < "$MAN"

unguarded=0; dropped=0; checked=0
for full in "${PATCHES[@]}"; do
  checked=$((checked+1))
  subj="$(git -C "$SRC" log -1 --format=%s "$full" | cut -c1-64)"
  # Tooling/docs-only commits (nothing outside deploy/ or *.md) cannot drop
  # binary functionality — auto-exempt. This also breaks the self-reference
  # regress: a commit that only maintains this TSV needs no row naming itself.
  # Any commit touching a compiled path still REQUIRES a signature/waive row.
  nonexempt="$(git -C "$SRC" diff-tree --no-commit-id --name-only -r "$full" | grep -vE '^deploy/|\.md$' | head -1)"
  if [ -z "$nonexempt" ]; then
    printf '  ok   [tool ] %s  %s\n' "${full:0:9}" "$subj"; continue
  fi
  # find the manifest key that is a prefix of this full commit sha
  key=""; for k in "${!MAP[@]}"; do case "$full" in "$k"*) key="$k"; break;; esac; done
  if [ -z "$key" ]; then
    printf '  UNGUARDED  %s  %s\n' "${full:0:9}" "$subj"; unguarded=$((unguarded+1)); continue
  fi
  kind="${MAP[$key]%%$'\t'*}"; val="${MAP[$key]#*$'\t'}"
  ok=1
  case "$kind" in
    sym)   grep -qF -- "$val" <<<"$SYMS"  || ok=0 ;;
    str)   grep -qF -- "$val" <<<"$STRS"  || ok=0 ;;
    tool)  grep -qF -- "$val" <<<"$TOOLV" || ok=0 ;;
    waive) ok=1 ;;
    *)     echo "  ? unknown kind '$kind' for ${full:0:9}"; ok=0 ;;
  esac
  if [ "$ok" = 1 ]; then printf '  ok   [%-5s] %s  %s\n' "$kind" "${full:0:9}" "$subj"
  else printf '  DROPPED [%-5s %s] %s  %s\n' "$kind" "$val" "${full:0:9}" "$subj"; dropped=$((dropped+1)); fi
done

echo "---"
if [ "$unguarded" -gt 0 ] || [ "$dropped" -gt 0 ]; then
  echo "FAIL ($checked patches): $unguarded UNGUARDED (add a signature/waive row) + $dropped DROPPED (patch missing from binary). DO NOT DEPLOY." >&2
  exit 1
fi
echo "PASS: all $checked fork patches accounted for in $BIN (signatures present or explicitly waived)."
