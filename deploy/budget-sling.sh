#!/usr/bin/env bash
#
# budget-sling.sh — TIER-0 of the token-budget governor (bead hq-dmc2): the deterministic
# PRE-SLING gate that refuses NEW dispatch when the binding usage pool is exhausted.
#
# Deliberately modelled on the existing advisory admit-gate,
# `.gov-tools/tools/governor/gated-sling.sh` (the box-optimizer/Governor's wrapper): same
# shape, same argument order, same fail-open contract, same "bead is never lost" rule.
# That gate meters the BOX (cpu/ram/CI); this one meters the TOKEN BUDGET. They are
# orthogonal and composable — see GOV_SLING_CMD below.
#
# Like that precedent, this is a WRAPPER, not a fork-patch of `gt`: no gt source change,
# no attested deploy, instantly removable.
#
# ── BEHAVIOUR (verdict comes from usage-gate.sh; see that file for the principles)
#   ADMIT     → sling, exactly as bare `gt sling`. Zero behaviour difference.
#   THROTTLE  → BROWNOUT. Defer: do NOT sling; the bead is left READY and re-dispatches
#               on the next ADMIT (nothing is lost, nothing is closed, nothing is killed).
#               Overridable per-call with `--force` / GOV_BUDGET_FORCE=1 — a brownout
#               must not stop the town from pushing something genuinely urgent.
#   HALT      → BLACKOUT. Refuse. Bead left READY. NOT overridable by --force; only the
#               kill-switch disables it (that is the point of a hard halt).
#   anything else (gate broke, python missing, no signal, stale signal, T0 role)
#             → FAIL-OPEN: sling, ungated, exactly as before.
#
# ── WHAT THIS GATE CANNOT DO (principle 3: shed new admission, never interrupt in-flight)
# It refuses to START a sling. That is its entire vocabulary. It sends no signals, knows
# no pids, and never calls `gt polecat`, `gt reap`, `gt estop`, `bd close` or anything
# else that could touch work already running. A polecat mid-turn is untouched by design.
#
# ── ROLE / EXEMPTION
# The gate classifies the DISPATCH TARGET (the rig the polecat is being spawned into),
# not the caller. The mayor is the caller of every sling, so exempting the caller would
# make this gate a permanent no-op; "never block the mayor from its own work" protects
# the mayor's own session (tier-2 does that), not every worker it spawns. Override the
# classified name with --role if you are dispatching something unusual.
#
# ── USAGE
#   budget-sling.sh [--force] [--role <name>] <bead> <rig> [gt sling args…]
#   budget-sling.sh wkb-123 whiz_kb --merge=mr
#
# ── ENV
#   GOV_THROTTLE / GOV_HALT     thresholds (default 95 / 99) — shared with the producer
#   GOV_BUDGET_FORCE=1          override a THROTTLE (never a HALT)
#   GOV_BUDGET_GATE_OFF=1       kill-switch (also: file $WD_STATE_DIR/BUDGET-GATE-OFF)
#   GOV_BUDGET_EXIT_NONZERO=1   exit 75 (EX_TEMPFAIL) instead of 0 when deferring, for
#                               callers that want to branch on it. Default 0 mirrors the
#                               gated-sling.sh precedent ("deferred" is not an error).
#   GOV_SLING_CMD               the command actually invoked (default: gt sling). Set it
#                               to .gov-tools/tools/governor/gated-sling.sh to CHAIN the
#                               box-resource gate behind this one — same <bead> <rig> ABI.
set -uo pipefail

STATE="${WD_STATE_DIR:-$HOME/gt}"
GATE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/usage-gate.sh"

FORCE="${GOV_BUDGET_FORCE:-0}"
ROLE_OVERRIDE=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --force) FORCE=1; shift ;;
    --role)  ROLE_OVERRIDE="${2:-}"; shift 2 ;;
    *)       break ;;                 # first non-flag = <bead>; everything after is gt's
  esac
done

if [ "$#" -lt 2 ]; then
  echo "usage: budget-sling.sh [--force] [--role <name>] <bead> <rig> [gt sling args…]" >&2
  exit 2
fi
bead="$1"; rig="$2"; shift 2

role="${ROLE_OVERRIDE:-$rig}"

# ── Consult the gate. A gate failure must never wedge dispatch.
verdict_json=""; rc=0
if [ -x "$GATE" ]; then
  verdict_json="$("$GATE" --role "$role" --context "sling:$bead" --quiet 2>/dev/null)" || rc=$?
else
  rc=99          # gate not installed → fail-open below
fi

deferred_exit=0
[ "${GOV_BUDGET_EXIT_NONZERO:-0}" = "1" ] && deferred_exit=75

# Pull a couple of fields for the human message (best-effort; never fatal).
read -r pool pct resets < <(printf '%s' "$verdict_json" | python3 -c '
import json,sys
try: d=json.load(sys.stdin)
except Exception: d={}
print(d.get("binding_pool") or "?", d.get("binding_pct") if d.get("binding_pct") is not None else "?", d.get("binding_resets_at") or "?")
' 2>/dev/null) || { pool="?"; pct="?"; resets="?"; }

case "$rc" in
  0)
    : # ADMIT (headroom, T0-exempt, kill-switch, or fail-open) — fall through and sling.
    ;;
  10)
    if [ "$FORCE" = "1" ]; then
      echo "budget-gate: ⚠️ THROTTLE overridden by --force — pool '$pool' at ${pct}% (resets $resets); slinging $bead → $rig anyway" >&2
    else
      echo "budget-gate: ⚠️ THROTTLE — DEFER $bead → $rig. Binding pool '$pool' at ${pct}% (>= ${GOV_THROTTLE:-95}%, resets $resets)." >&2
      echo "budget-gate:    Bead left READY; it re-dispatches on the next ADMIT. Land/commit in-flight work first." >&2
      echo "budget-gate:    Override this one dispatch with: budget-sling.sh --force $bead $rig" >&2
      printf '%s\n' "$verdict_json"
      exit "$deferred_exit"
    fi
    ;;
  20)
    echo "budget-gate: 🛑 HALT — REFUSING to dispatch $bead → $rig. Binding pool '$pool' at ${pct}% (>= ${GOV_HALT:-99}%, resets $resets)." >&2
    echo "budget-gate:    Bead left READY; it re-dispatches automatically once the pool resets." >&2
    echo "budget-gate:    In-flight work is untouched — land and commit it. --force does NOT override a HALT;" >&2
    echo "budget-gate:    the only override is the kill-switch (touch $STATE/BUDGET-GATE-OFF)." >&2
    printf '%s\n' "$verdict_json"
    exit "$deferred_exit"
    ;;
  *)
    echo "budget-gate: gate unavailable (rc=$rc) — proceeding ungated (fail-open)" >&2
    ;;
esac

# ── Sling, exactly as before.
# shellcheck disable=SC2086   # GOV_SLING_CMD is an operator-supplied command word list.
exec ${GOV_SLING_CMD:-gt sling} "$bead" "$rig" "$@"
