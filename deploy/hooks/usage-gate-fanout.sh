#!/usr/bin/env bash
#
# usage-gate-fanout.sh — TIER-2 of the token-budget governor (bead hq-qn7m): a PreToolUse
# hook that refuses NEW sub-agent fan-out when the binding usage pool is exhausted.
#
# There is no built-in sub-agent concurrency/budget cap in Claude Code, so this hook IS
# the tier-2 governor. It is wired via the town's durable override mechanism
# (~/.gt/hooks-overrides/<target>.json — which `gt hooks sync` / `gt doctor --fix` MERGE,
# unlike a settings.json edit, which they overwrite).
#
# ── SCOPE: SUB-AGENT SPAWNS ONLY. TWICE.
#  1. The settings matcher is the exact tool-name list "Agent|Task" (a matcher of only
#     letters/digits/_/-/space/,/| is compared as an EXACT string with `|` alternatives —
#     NOT a regex — so it cannot accidentally widen). `Agent` is the current tool name
#     (verified against Claude Code 2.1.218 transcripts + the hooks docs); `Task` is kept
#     for version drift. No other tool is matched.
#  2. This script re-checks `tool_name` itself and exits immediately for anything else.
#     If the matcher is ever mis-edited, the gate still cannot touch another tool.
#
# ── PRINCIPLE 3 (shed new admission, never interrupt in-flight)
# A PreToolUse deny prevents a sub-agent from STARTING. It has no effect on an already-
# running sub-agent, on the parent's current turn, or on any polecat. This script sends
# no signals and runs no gt/bd command; it reads a JSON file and prints a verdict.
#
# ── VERDICTS (from usage-gate.sh)
#   HALT      → permissionDecision "deny" + reason. The parent agent is told why and can
#               keep working in-process; it just cannot fan out.
#   THROTTLE  → NO permission decision (tool proceeds under the normal permission flow);
#               injects `additionalContext` warning the agent to consolidate rather than
#               fan out. Checkpoint-on-WARNING, not on block.
#   ADMIT / anything else → silent exit 0. Zero behaviour change.
#
# ── FAIL-OPEN IS STRUCTURAL
# For PreToolUse, ONLY exit code 2 blocks; every other exit code is a non-blocking error
# and execution continues. This script therefore never exits 2 — it blocks exclusively via
# the JSON `permissionDecision`. A crash, a missing python3, a corrupt status file, an
# unreadable gate: all of them let the spawn through. The install line additionally wraps
# the call in `|| true` so even a shell-level failure (exit 2 from a bash parse error)
# cannot block the town.
set -uo pipefail

# Parse the hook payload ONCE (this runs on every sub-agent spawn — keep it to a single
# interpreter start). Emits exactly three tab-separated fields; any parse failure yields
# empties, which flow into the fail-open paths below.
IFS=$'\t' read -r tool hook_cwd agent_type < <(python3 -c '
import json,sys
try: d=json.load(sys.stdin)
except Exception: d={}
if not isinstance(d,dict): d={}
ti=d.get("tool_input") if isinstance(d.get("tool_input"),dict) else {}
def s(v): return v if isinstance(v,str) else ""
print("\t".join((s(d.get("tool_name")), s(d.get("cwd")), s(ti.get("agent_type")))))
' 2>/dev/null) || { tool=""; hook_cwd=""; agent_type=""; }

case "${tool:-}" in
  Agent|Task) : ;;
  *) exit 0 ;;                 # guard 2: never gate any other tool
esac

# ── Whose fan-out is it? Here the CALLER is what we classify (tier-0 classifies the
#    dispatch target instead — see budget-sling.sh). GT_ROLE is set in every gastown
#    agent session (e.g. "mayor", "deacon", "whiz_kb/witness"); fall back to the cwd the
#    hook payload reports, then to $PWD, then to the empty string (⇒ T2, gateable).
role="${GT_ROLE:-}"
if [ -z "$role" ]; then
  cwd="${hook_cwd:-$PWD}"
  role="${cwd#"${GT_TOWN_ROOT:-$HOME/gt}"/}"
fi

GATE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd 2>/dev/null)/usage-gate.sh"
[ -x "$GATE" ] || exit 0                                    # not installed ⇒ fail-open

rc=0
verdict_json="$("$GATE" --role "$role" --context "fanout:${agent_type:-?}" --quiet 2>/dev/null)" || rc=$?

case "$rc" in
  20)  decision=deny ;;
  10)  decision=warn ;;
  *)   exit 0 ;;                                            # ADMIT / fail-open / T0-exempt
esac

printf '%s' "$verdict_json" | python3 -c "
import json,sys
try: d=json.load(sys.stdin)
except Exception: d={}
pool=d.get('binding_pool','?'); pct=d.get('binding_pct','?'); reset=d.get('binding_resets_at','?')
if '$decision'=='deny':
    out={'hookSpecificOutput':{
        'hookEventName':'PreToolUse',
        'permissionDecision':'deny',
        'permissionDecisionReason':(
            f\"Token-budget governor (tier-2 HALT): binding usage pool '{pool}' is at {pct}% \"
            f\"(>= {d.get('halt','?')}%), resets {reset}. New sub-agent fan-out is shed to protect \"
            'the remaining budget for in-flight work. Do this work in-process instead, or narrow it. '
            'Already-running sub-agents and polecats are unaffected — land and commit what is open. '
            'This lifts automatically when the pool resets.')}}
else:
    out={'hookSpecificOutput':{
        'hookEventName':'PreToolUse',
        'additionalContext':(
            f\"⚠️ Token-budget governor (tier-2 THROTTLE): binding usage pool '{pool}' is at {pct}% \"
            f\"(>= {d.get('throttle','?')}%), resets {reset}. This spawn is ALLOWED, but prefer \"
            'consolidating work in-process over fanning out, and checkpoint/commit what you have.')}}
print(json.dumps(out))
" 2>/dev/null || exit 0                                     # emit-failure ⇒ fail-open

exit 0     # NEVER exit 2 from an error path — only the JSON above may block.
