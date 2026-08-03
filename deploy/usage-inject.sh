#!/usr/bin/env bash
# usage-inject.sh — token-budget CONSUMER (UserPromptSubmit hook).
# (hq-dmc2, the "checkpoint-on-warning" path.) Reads the producer's status file
# and injects a system-reminder into the agent's turn ONLY when the binding pool
# is warning/critical/rejected — so the agent SEES budget pressure in its own
# context and can wrap up/commit before an external gate or the API fires.
# Silent when healthy. Microseconds (cat+parse, no network). Deterministic.
STATUS="${WD_STATE_DIR:-$HOME/gt}/USAGE-STATUS.json"
[ -f "$STATUS" ] || exit 0
python3 - "$STATUS" <<'PY' 2>/dev/null
import json,sys
try: d=json.load(open(sys.argv[1]))
except Exception: sys.exit(0)
if d.get("stale"): sys.exit(0)   # fail-open: no fresh signal => say nothing
sev=d.get("binding_severity","normal")
if sev in ("normal",) or sev is None: sys.exit(0)
pool=d.get("binding_pool"); pct=d.get("binding_pct"); reset=d.get("binding_resets_at")
tag={"warning":"⚠️ BUDGET WARNING","critical":"⛔ BUDGET CRITICAL","rejected":"🛑 BUDGET EXHAUSTED"}.get(sev, "BUDGET "+sev.upper())
print("<system-reminder>")
print(f"{tag}: the binding rate-limit pool '{pool}' is at {pct}% (resets {reset}).")
if sev in ("critical","rejected"):
    print("Reach a safe stopping point NOW: commit WIP, finish or hand off the current step, and do not start new expensive work or sub-agent fan-outs until this pool resets. Uncommitted work is lost if the pool hits the wall mid-turn.")
else:
    print("Prefer to finish and commit in-flight work over starting new work; avoid sub-agent fan-outs against this pool.")
print("</system-reminder>")
PY
exit 0
