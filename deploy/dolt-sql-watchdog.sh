#!/usr/bin/env bash
# dolt-sql-watchdog.sh — deterministic SQL-LIVENESS watchdog for Dolt (NO LLM).
#
# Complements (does NOT replace) the systemd self-heal on dolt.service
# (MemoryMax=12G + OOMPolicy=kill + Restart=always). That config handles
# process-death and memory-cap OOM. It is BLIND to the "alive-but-deadlocked"
# wedge: the dolt process stays healthy (under the cap, burning CPU) while its
# SQL server is internally hung — handshakes fail "unexpected EOF / invalid
# connection". systemd's process-liveness check cannot see that; only a
# SQL-level probe can. (Root cause of the 2026-06-01 town-wide wedge, caught
# only because boot escalated while the LLM deacon monitor was capped/down.)
#
# This is the deterministic replacement for "an LLM agent watches Dolt": a
# 20-line healthcheck does the liveness job better, cheaper, and with no weekly
# usage cap.
#
# NOTE: supersedes the older RSS-threshold/nohup dolt-watchdog.sh (retired
# 2026-05-28 when Dolt moved to systemd management). This one restarts via
# systemctl, cooperating with systemd rather than bypassing it.
#
# Disable: systemctl --user disable --now dolt-sql-watchdog.service
set -uo pipefail

INTERVAL="${DOLT_WD_INTERVAL:-30}"        # seconds between probes
TIMEOUT="${DOLT_WD_TIMEOUT:-10}"          # per-probe SQL timeout
FAIL_THRESHOLD="${DOLT_WD_THRESHOLD:-2}"  # consecutive fails before restart
COOLDOWN="${DOLT_WD_COOLDOWN:-90}"        # pause after restart (let Dolt come up)
# Wedged-query mode (hq-kss7, 4th failure mode, 2 incidents in 30h): zombie Query
# sessions accumulate while the SELECT-1 probe path stays HEALTHY — invisible to the
# liveness probe above. Detect via processlist: stuck (>QAGE s) Query sessions over
# threshold => restart. Probed every WEDGE_EVERY-th liveness cycle (~5 min at defaults).
WEDGE_STUCK_THRESHOLD="${DOLT_WD_STUCK:-50}"   # stuck-query count that triggers restart
WEDGE_QAGE="${DOLT_WD_QAGE:-180}"   # R2 (ANL-OPS-dolt-wedged-query-fta): 300->180 shrinks throttle window ~2min              # seconds a Query must be running to count as stuck
WEDGE_EVERY="${DOLT_WD_EVERY:-10}"             # check every N liveness cycles

fails=0
cd /home/dev/gt/mayor 2>/dev/null || cd /home/dev/gt 2>/dev/null || true
log(){ echo "[dolt-sql-watchdog $(date -u +%Y-%m-%dT%H:%M:%SZ)] $*"; }
# R1 (ANL-OPS-dolt-wedged-query-fta): durable auto-RCA before ANY restart —
# June evidence died in /tmp; the next occurrence must self-document A1 vs A2.
capture_rca(){
  local tag="$1" d="$HOME/gt/daemon/rca" ts
  ts=$(date -u +%Y%m%dT%H%M%SZ); mkdir -p "$d"
  { echo "=== $tag $ts ==="; timeout 8 bd sql -q "SELECT id,user,host,db,command,time,state,LEFT(info,200) FROM information_schema.processlist ORDER BY time DESC;" 2>&1
    echo "=== conn summary ==="; timeout 8 bd sql -q "SELECT command,COUNT(*),MAX(time) FROM information_schema.processlist GROUP BY command;" 2>&1
    echo "=== gt dolt status ==="; timeout 15 gt dolt status 2>&1
    echo "=== sockets ==="; ss -tn state established '( sport = :3307 )' 2>&1 | wc -l
  } > "$d/rca-$tag-$ts.log" 2>&1
  ls -t "$d"/rca-*.log 2>/dev/null | tail -n +25 | xargs -r rm -f   # keep newest 24
  log "RCA captured: $d/rca-$tag-$ts.log"
}

log "started (interval=${INTERVAL}s timeout=${TIMEOUT}s threshold=${FAIL_THRESHOLD} cooldown=${COOLDOWN}s)"
while true; do
  if timeout "$TIMEOUT" bd sql -q "SELECT 1;" >/dev/null 2>&1; then
    if [ "$fails" -gt 0 ]; then log "SQL recovered after ${fails} consecutive fail(s)"; fi
    fails=0
  else
    fails=$((fails + 1))
    log "SQL probe FAILED (${fails}/${FAIL_THRESHOLD})"
    if [ "$fails" -ge "$FAIL_THRESHOLD" ]; then
      log "WEDGE confirmed (alive-but-deadlocked) — capturing RCA then restarting dolt.service"
      capture_rca wedge
      systemctl --user restart dolt.service 2>&1 | sed 's/^/  /'
      log "restart issued; cooling down ${COOLDOWN}s before resuming probes"
      sleep "$COOLDOWN"
      fails=0
    fi
  fi
  # Wedged-query probe (every WEDGE_EVERY cycles)
  cycle=$(( ${cycle:-0} + 1 ))
  if [ $(( cycle % WEDGE_EVERY )) -eq 0 ]; then
    stuck=$(timeout "$TIMEOUT" bd sql -q "SELECT COUNT(*) FROM information_schema.processlist WHERE command='Query' AND time > ${WEDGE_QAGE} AND COALESCE(info,'') NOT LIKE '%GET_LOCK%';" 2>/dev/null | grep -oE '[0-9]+' | tail -1)
    if [ -n "$stuck" ] && [ "$stuck" -ge "$WEDGE_STUCK_THRESHOLD" ]; then
      log "WEDGED-QUERY mode confirmed (${stuck} stuck Query sessions > ${WEDGE_QAGE}s, threshold ${WEDGE_STUCK_THRESHOLD}) — capturing RCA then restarting dolt.service"
      capture_rca wedged-query
      systemctl --user restart dolt.service 2>&1 | sed 's/^/  /'
      log "restart issued (wedged-query); cooling down ${COOLDOWN}s"
      sleep "$COOLDOWN"
      fails=0
    elif [ -n "$stuck" ] && [ "$stuck" -gt 0 ]; then
      # identity logging (2026-08-02): the chronic sub-threshold single-stuck persisted
      # across the 2.1.7->2.2.3 upgrade; pattern = recurring 3-5min query, not growth.
      # Log WHO it is so the signature self-documents instead of staying a count.
      identerr="$HOME/gt/daemon/rca/ident-err.log"
      ident=$(timeout "$TIMEOUT" bd sql -q "SELECT CONCAT('id=',COALESCE(id,'?'),' user=',COALESCE(user,'NULL'),' t=',COALESCE(time,'?'),'s q=',LEFT(COALESCE(info,'?'),110)) FROM information_schema.processlist WHERE command='Query' AND time > ${WEDGE_QAGE} AND COALESCE(info,'') NOT LIKE '%GET_LOCK%' LIMIT 3;" 2>>"$identerr" | grep -F "id=" | head -3 | tr '\n' ' ')
      if [ -z "$ident" ]; then
        # count>0 but identity empty: self-diagnose — snapshot FULL processlist + errors durably
        { echo "=== ident-empty at $(date -u +%FT%TZ) (count=${stuck}) ==="
          timeout "$TIMEOUT" bd sql -q "SELECT id,user,command,time,state,LEFT(COALESCE(info,'-'),140) FROM information_schema.processlist ORDER BY time DESC;" 2>&1
        } >> "$HOME/gt/daemon/rca/ident-empty-snapshots.log"
      fi
      log "wedged-query probe: ${stuck} stuck session(s) (below threshold ${WEDGE_STUCK_THRESHOLD}) ${ident}"
    fi
  fi
  sleep "$INTERVAL"
done
