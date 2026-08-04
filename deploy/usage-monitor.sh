#!/usr/bin/env bash
# usage-monitor.sh — deterministic token-budget signal PRODUCER (no LLM).
# (hq-dmc2 tier-0 governor, step 1 per the AoA.) Polls the subscription usage
# endpoint and atomically writes ~/gt/USAGE-STATUS.json for consumers (the
# prompt-inject hook, and later the tier-0 dispatch gate) to read.
#
# PRINCIPLES (from the governor AoA):
#  - Single producer: this is the ONLY poller; consumers read the file, never
#    the endpoint (no duplicate polling, one source of truth).
#  - Fail-OPEN: on any fetch/parse error, leave the last-good file and mark it
#    stale — NEVER fabricate a number. Consumers treat missing/stale as "no
#    signal => do not throttle" (a broken endpoint must not freeze the town).
#  - Act on the representative claim: the "binding" pool is whichever limit is
#    actually warning/rejected (or highest %), across five_hour / seven_day /
#    per-model — not a hand-picked field.
#  - Never prints the OAuth token (Ben: never surface credentials).
set -uo pipefail
STATE="${WD_STATE_DIR:-$HOME/gt}"
OUT="$STATE/USAGE-STATUS.json"
CRED="$HOME/.claude/.credentials.json"
mkdir -p "$STATE"

now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
tok="$(python3 -c "import json;print(json.load(open('$CRED'))['claudeAiOauth']['accessToken'])" 2>/dev/null)"
if [ -z "$tok" ]; then
  python3 - "$OUT" "$now" <<'PY' 2>/dev/null   # mark stale, keep last-good body
import json,sys,os
out,now=sys.argv[1],sys.argv[2]
try: d=json.load(open(out))
except Exception: d={}
d.update({"stale":True,"stale_reason":"no credential readable","fetched_at":d.get("fetched_at"),"checked_at":now})
tmp=out+".tmp"; json.dump(d,open(tmp,"w")); os.replace(tmp,out)
PY
  exit 0
fi

BODY_FILE="$(mktemp)"; trap 'rm -f "$BODY_FILE"' EXIT
# The usage endpoint is ITSELF rate-limited (measured 2026-08-04: 9/10 rapid probes -> 429).
# A monitor that gets 429'd reports 'stale', and stale => fail-open => gates disarmed.
# So: bounded retry with backoff on non-200, and record the http code for diagnosis.
code=""
for attempt in 1 2 3; do
  code="$(curl -s -o "$BODY_FILE" -w '%{http_code}' --max-time 15 \
    https://api.anthropic.com/api/oauth/usage \
    -H "Authorization: Bearer $tok" -H "anthropic-beta: oauth-2025-04-20" -H "anthropic-version: 2023-06-01" 2>/dev/null)"
  [ "$code" = "200" ] && break
  [ "$attempt" -lt 3 ] && sleep $((attempt * 5))   # 5s, 10s backoff
done
[ "$code" = "200" ] || printf '' > "$BODY_FILE"    # non-200 => empty => parse fails => stale (fail-open, as ratified)

python3 - "$OUT" "$now" "$BODY_FILE" <<'PY' 2>/dev/null
import json,sys,os
out,now,body_file=sys.argv[1],sys.argv[2],sys.argv[3]
raw=open(body_file).read()
try:
    d=json.loads(raw)
    assert isinstance(d,dict) and ('five_hour' in d or 'seven_day' in d)
except Exception:
    # fail-open: keep last-good, mark stale (endpoint 404'd / changed shape / rate-limited)
    try: prev=json.load(open(out))
    except Exception: prev={}
    prev.update({"stale":True,"stale_reason":"fetch/parse failed","checked_at":now})
    tmp=out+".tmp"; json.dump(prev,open(tmp,"w")); os.replace(tmp,out); sys.exit(0)

fh=d.get('five_hour',{}) or {}; sd=d.get('seven_day',{}) or {}
pools=[("five_hour", fh.get('utilization'), fh.get('resets_at'), "normal")]
pools.append(("seven_day", sd.get('utilization'), sd.get('resets_at'), "normal"))
for l in (d.get('limits') or []):
    m=((l.get('scope') or {}).get('model') or {}).get('display_name')
    name=("model:"+m) if m else (l.get('group') or l.get('kind') or "limit")
    pools.append((name, l.get('percent'), l.get('resets_at'), l.get('severity','normal')))
# binding pool = worst severity, then highest utilization (the representative claim)
sev_rank={"rejected":4,"critical":3,"warning":2,"normal":1}  # incomplete map = gate on wrong pool (build-test caught 'critical' omitted)
def key(p):
    return (sev_rank.get(p[3],0), p[1] if isinstance(p[1],(int,float)) else -1)
binding=max(pools,key=key) if pools else (None,None,None,"normal")
status={
  "fetched_at":now,"checked_at":now,"stale":False,
  "five_hour_pct":fh.get('utilization'),"five_hour_resets_at":fh.get('resets_at'),
  "seven_day_pct":sd.get('utilization'),"seven_day_resets_at":sd.get('resets_at'),
  "binding_pool":binding[0],"binding_pct":binding[1],"binding_resets_at":binding[2],"binding_severity":binding[3],
  "pools":[{"name":n,"pct":p,"resets_at":r,"severity":s} for (n,p,r,s) in pools],
}
tmp=out+".tmp"; json.dump(status,open(tmp,"w"),indent=2); os.replace(tmp,out)
PY

# ── Edge-triggered alert: the ROUTINE injects ONCE when a threshold is crossed ──
# (Not a per-turn hook — that re-injected every turn while above threshold, wasteful.)
# Founder-set 2026-08-03 (less conservative): throttle 95, halt 99.
GOV_THROTTLE="${GOV_THROTTLE:-95}"; GOV_HALT="${GOV_HALT:-99}"
GT="${GT:-$HOME/.local/bin/gt}"; LEVELFILE="$STATE/.usage-gov-level"
read -r lvl pct pool reset < <(python3 - "$OUT" "$GOV_THROTTLE" "$GOV_HALT" <<'PY2' 2>/dev/null
import json,sys
d=json.load(open(sys.argv[1])); thr=float(sys.argv[2]); halt=float(sys.argv[3])
p=d.get('binding_pct') or 0
print('halt' if p>=halt else 'throttle' if p>=thr else 'ok', p, (d.get('binding_pool') or '?'), (d.get('binding_resets_at') or '?'))
PY2
)
lvl="${lvl:-ok}"; prev="$(cat "$LEVELFILE" 2>/dev/null || echo ok)"
rank(){ case "$1" in halt) echo 2;; throttle) echo 1;; *) echo 0;; esac; }
if [ "$(rank "$lvl")" -gt "$(rank "$prev")" ]; then    # crossing UP only
  case "$lvl" in
    halt)     m="🛑 BUDGET HALT: pool '$pool' at ${pct}% (>=${GOV_HALT}%, resets $reset). Stop new dispatch + sub-agent fan-outs; land/commit in-flight work only." ;;
    throttle) m="⚠️ BUDGET THROTTLE: pool '$pool' at ${pct}% (>=${GOV_THROTTLE}%, resets $reset). Finish+commit over starting new; no new fan-outs on this pool." ;;
  esac
  "$GT" nudge --mode=queue mayor  "$m" >/dev/null 2>&1 || true   # one-shot, best-effort
  "$GT" nudge --mode=queue deacon "$m" >/dev/null 2>&1 || true
  printf '%s\n' "$m" > "$STATE/USAGE-ALERT"                       # durable record
fi
printf '%s\n' "$lvl" > "$LEVELFILE"    # record level; re-arms when it drops after reset
exit 0
