#!/usr/bin/env bash
# usage-gate.sh — the shared, deterministic DECISION ENGINE for the token-budget
# governor's enforcement legs (DEC-OPS-token-budget-governor, Whiz-KB #584).
#
# Consumers:
#   tier-0  budget-sling.sh        (hq-dmc2) — refuses NEW dispatch under pressure
#   tier-2  usage-gate-fanout.sh   (hq-qn7m) — refuses NEW sub-agent fan-out under pressure
#
# This script NEVER polls the usage endpoint. It only READS the file the single
# producer (`usage-monitor.sh`, usage-monitor.timer, 60s) atomically writes:
#   $WD_STATE_DIR/USAGE-STATUS.json
# One producer, many consumers — no duplicate polling, one source of truth.
#
# ── PRINCIPLES (ratified DEC; each one is load-bearing, do not "simplify" them out)
#  1. BROWNOUT BEFORE BLACKOUT — warn/defer at THROTTLE (95), hard-refuse at HALT (99).
#  2. CHECKPOINT-ON-WARNING, NOT ON BLOCK — the throttle tier tells agents to land work;
#     it never kills anything.
#  3. SHED NEW ADMISSION BEFORE INTERRUPTING IN-FLIGHT — this engine only ever answers
#     "may this NEW thing start?". It has NO code path that signals, kills, reaps or
#     otherwise touches a running polecat or an in-flight agent turn. It cannot: it
#     takes no pids, sends no signals, and runs no gt/bd command.
#  4. ACT ON THE BINDING POOL — `binding_pct`/`binding_severity`, whichever pool is
#     actually binding, never a hand-picked field.
#  5. FAIL-OPEN — missing / stale / unparseable / too-old signal ⇒ ADMIT. A broken
#     signal must never freeze the town. Every internal error path also ADMITs.
#  6. KILL-SWITCH — env GOV_BUDGET_GATE_OFF=1 or the file $WD_STATE_DIR/BUDGET-GATE-OFF
#     disables both gates instantly, no restart, mirroring the governor actuator pattern.
#
# ── ROLE EXEMPTION (T0 control plane)
# Classification is delegated to the governor's existing, ratified classifier
# `.gov-tools/tools/governor/enforcement/tiers.py` (ONE source of truth — the T0 set
# there is the superset {dolt, mayor, refinery, governor, deacon, overseer, boot,
# dashboard, recorder}). If it cannot be imported we fall back to an inline copy of that
# same set, and if even that fails we EXEMPT — matching tiers.py's own asymmetry:
# over-exemption merely wastes headroom, under-exemption wedges the town.
#
# NOTE ON *WHOSE* ROLE: the two tiers pass different things, deliberately.
#   tier-0 passes the DISPATCH TARGET (the rig/polecat about to be spawned) — the mayor
#          is the caller of every sling, so exempting the caller would make tier-0 a
#          no-op. "Never block the mayor from its own work" means the mayor's session,
#          not every worker it spawns.
#   tier-2 passes the CALLER (GT_ROLE) — there it IS the caller's own fan-out.
#
# ── USAGE
#   usage-gate.sh --role <role> [--context <label>] [--quiet]
# stdout : one-line JSON verdict (machine-readable)
# stderr : one human line (unless --quiet)
# exit   :  0 = ADMIT      (proceed)
#          10 = THROTTLE   (brownout: defer new admission; overridable by the caller)
#          20 = HALT       (blackout: refuse new admission)
#   Callers MUST treat any OTHER exit code as ADMIT (fail-open). This script tries very
#   hard to only ever emit 0/10/20, but the contract is what protects the town.
#
# ── ENV
#   GOV_THROTTLE            brownout threshold, %   (default 95; same name as producer)
#   GOV_HALT                blackout threshold, %   (default 99; same name as producer)
#   GOV_BUDGET_GATE_OFF=1   kill-switch (env)
#   WD_STATE_DIR            state dir               (default ~/gt)
#   GOV_USAGE_STATUS        override status path    (default $WD_STATE_DIR/USAGE-STATUS.json)
#   GOV_STATUS_MAX_AGE_SEC  age beyond which the signal is stale (default 600 = 10x the
#                           60s producer period; a wedged timer must not throttle anyone)
#   GOV_TIERS_PY            override tiers.py path
#   GOV_BUDGET_GATE_LOG     verdict log             (default $WD_STATE_DIR/budget-gate.jsonl)
set -uo pipefail

STATE="${WD_STATE_DIR:-$HOME/gt}"
ROLE=""; CONTEXT=""; QUIET=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --role)    ROLE="${2:-}"; shift 2 ;;
    --context) CONTEXT="${2:-}"; shift 2 ;;
    --quiet)   QUIET=1; shift ;;
    -h|--help) sed -n '2,60p' "$0"; exit 0 ;;
    *)         shift ;;                    # ignore unknowns rather than fail closed
  esac
done

# ── Kill-switch: checked FIRST, in pure bash, so it works even if python3 is broken.
if [ "${GOV_BUDGET_GATE_OFF:-0}" = "1" ] || [ -f "$STATE/BUDGET-GATE-OFF" ]; then
  printf '{"verdict":"ADMIT","reason":"kill-switch","role":"%s"}\n' "$ROLE"
  [ "$QUIET" = 1 ] || echo "budget-gate: DISABLED (kill-switch) — admitting" >&2
  exit 0
fi

STATUS="${GOV_USAGE_STATUS:-$STATE/USAGE-STATUS.json}"

# Engine stderr is swallowed in production (a gate must be silent, not noisy) but a
# permanently-crashing engine would then fail-open invisibly forever — so expose it
# under GOV_BUDGET_GATE_DEBUG=1 for verification/triage.
if [ "${GOV_BUDGET_GATE_DEBUG:-0}" = "1" ]; then ERRSINK=/dev/stderr; else ERRSINK=/dev/null; fi

out="$(python3 - "$STATUS" "$ROLE" "$CONTEXT" <<'PY' 2>"$ERRSINK"
import json, os, sys, time

status_path, role, context = sys.argv[1], sys.argv[2], sys.argv[3]
throttle = float(os.environ.get("GOV_THROTTLE") or 95)
halt     = float(os.environ.get("GOV_HALT") or 99)
max_age  = float(os.environ.get("GOV_STATUS_MAX_AGE_SEC") or 600)
state    = os.environ.get("WD_STATE_DIR") or os.path.join(os.path.expanduser("~"), "gt")
log_path = os.environ.get("GOV_BUDGET_GATE_LOG") or os.path.join(state, "budget-gate.jsonl")

def emit(verdict, reason, **extra):
    """Print the verdict and exit. ADMIT=0, THROTTLE=10, HALT=20."""
    rec = {"verdict": verdict, "reason": reason, "role": role, "context": context}
    rec.update(extra)
    rec["at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    # Durable bake evidence, bounded. Never let logging change the verdict.
    try:
        lines = []
        if os.path.exists(log_path):
            with open(log_path) as fh:
                lines = fh.readlines()
            if len(lines) > 4000:                      # rotate in place, keep the tail
                with open(log_path + ".tmp", "w") as fh:
                    fh.writelines(lines[-2000:])
                os.replace(log_path + ".tmp", log_path)
        with open(log_path, "a") as fh:
            fh.write(json.dumps(rec) + "\n")
    except Exception:
        pass
    print(json.dumps(rec))
    sys.exit({"ADMIT": 0, "THROTTLE": 10, "HALT": 20}[verdict])

# ── T0 exemption. Delegate to the governor's ratified classifier; on ANY failure,
#    exempt (tiers.py's own asymmetry: over-exemption is cheap, under-exemption is not).
def tier_of(name):
    path = os.environ.get("GOV_TIERS_PY") or os.path.join(
        state, ".gov-tools", "tools", "governor", "enforcement", "tiers.py")
    try:
        import importlib.util
        spec = importlib.util.spec_from_file_location("_gov_tiers", path)
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)
        return mod.classify(name), "tiers.py"
    except Exception:
        pass
    # Inline fallback — MUST stay a superset of tiers.py CONTROL_PLANE_ROLES.
    try:
        import re
        control = {"dolt", "mayor", "refinery", "refineries", "governor", "gov",
                   "deacon", "overseer", "boot", "dashboard", "recorder"}
        toks = {t for t in re.split(r"[-/_.]+", (name or "").strip().lower())
                if t and t not in ("slice", "service", "scope")}
        return ("T0" if toks & control else "T2"), "fallback"
    except Exception:
        return "T0", "unclassifiable"          # in doubt ⇒ exempt

tier, tier_src = tier_of(role)
if tier == "T0":
    emit("ADMIT", "t0-exempt", tier=tier, tier_source=tier_src)

# ── Read the signal. Every failure below is FAIL-OPEN.
try:
    with open(status_path) as fh:
        d = json.load(fh)
except FileNotFoundError:
    emit("ADMIT", "fail-open:no-signal-file", tier=tier)
except Exception as e:
    emit("ADMIT", "fail-open:unparseable", tier=tier, detail=type(e).__name__)

if not isinstance(d, dict):
    emit("ADMIT", "fail-open:unparseable", tier=tier)

if d.get("stale") is True:
    emit("ADMIT", "fail-open:stale", tier=tier,
         stale_reason=d.get("stale_reason"), binding_pct=d.get("binding_pct"))

# A wedged producer leaves a fresh-looking body forever — age it out on mtime too.
try:
    age = time.time() - os.path.getmtime(status_path)
    if age > max_age:
        emit("ADMIT", "fail-open:signal-too-old", tier=tier, age_sec=round(age))
except Exception:
    emit("ADMIT", "fail-open:no-mtime", tier=tier)

pct = d.get("binding_pct")                     # principle 4: the BINDING pool only
if not isinstance(pct, (int, float)) or isinstance(pct, bool):
    emit("ADMIT", "fail-open:no-binding-pct", tier=tier)

pool = d.get("binding_pool") or "?"
sev  = d.get("binding_severity") or "?"
resets = d.get("binding_resets_at") or "?"
common = dict(tier=tier, binding_pool=pool, binding_pct=pct,
              binding_severity=sev, binding_resets_at=resets,
              throttle=throttle, halt=halt)

# Principle 4 acts on the binding pool's pct AND its severity. "rejected" is the API's own
# top severity — the pool is already refusing requests, so admitting new work would just
# manufacture failures no matter what percentage is reported. It is deliberately the ONLY
# severity that halts: today's live signal carries severity "critical" at 92%, and halting
# on "critical" would freeze the town far below the ratified 99 threshold.
if str(sev).lower() == "rejected":
    emit("HALT", "binding-pool-severity-rejected", **common)
if pct >= halt:
    emit("HALT", "binding-pool-at-halt", **common)
if pct >= throttle:
    emit("THROTTLE", "binding-pool-at-throttle", **common)
emit("ADMIT", "headroom-ok", **common)
PY
)"
rc=$?

# python3 itself missing/crashed ⇒ FAIL-OPEN (principle 5). Never inherit a mystery code.
case "$rc" in
  0|10|20) : ;;
  *)
    printf '{"verdict":"ADMIT","reason":"fail-open:engine-error","role":"%s","engine_rc":%s}\n' "$ROLE" "$rc"
    [ "$QUIET" = 1 ] || echo "budget-gate: engine unavailable (rc=$rc) — admitting (fail-open)" >&2
    exit 0
    ;;
esac

printf '%s\n' "$out"
if [ "$QUIET" != 1 ]; then
  python3 - "$out" <<'PY2' >&2 2>/dev/null || true
import json,sys
try: d=json.loads(sys.argv[1])
except Exception: sys.exit(0)
v=d.get("verdict"); pool=d.get("binding_pool","?"); pct=d.get("binding_pct","?")
if v=="HALT":
    print(f"budget-gate: 🛑 HALT — binding pool '{pool}' at {pct}% (>= {d.get('halt')}%), resets {d.get('binding_resets_at')}")
elif v=="THROTTLE":
    print(f"budget-gate: ⚠️  THROTTLE — binding pool '{pool}' at {pct}% (>= {d.get('throttle')}%), resets {d.get('binding_resets_at')}")
else:
    print(f"budget-gate: ADMIT ({d.get('reason')})")
PY2
fi
exit "$rc"
