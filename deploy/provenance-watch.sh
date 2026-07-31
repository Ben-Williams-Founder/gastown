#!/usr/bin/env bash
# provenance-watch.sh — deterministic bypass detector for the live gt binary.
# (DEC-OPS-gt-provenance-watchdog; RVTM FR-06/FR-08/NFR-02.)
#
# PRODUCER side of the watchdog pair. Fired by provenance-watch.path (instant on
# binary swap) and provenance-watch.timer (15-min heartbeat). EXEC-FREE by
# design: the suspect binary is NEVER executed (a wedged imposter cannot hang
# the watchdog; adversarial lens-1 finding). All external calls timeout-bounded;
# flock single-instance; 30s debounce absorbs legitimate mid-deploy states.
#
# GREEN: binary sha == manifest binarySha256, stamped attestationId+patchSetHash
#        present in the binary's bytes, PATH resolves gt to the watched path,
#        bd matches its pin. Clears any sentinel.
# RED:   write sha-keyed ~/gt/PROVENANCE-RED FIRST (source of truth; works when
#        gt is broken), then ONCE-PER-SHA best-effort `gt escalate` (trust
#        caveat: escalation execs live gt — bounded by timeout, never relied on).
# ACK:   touch ~/gt/PROVENANCE-ACK-<sha12> — suppresses escalation for that
#        exact sha (rollback flow); recorded in the sentinel, auditable.
#
# Env overrides (container drill): WD_BIN WD_MANIFEST WD_STATE_DIR WD_DEBOUNCE
#                                  WD_BD WD_FORK_MANIFEST WD_SKIP_PATH_GUARD
set -uo pipefail

BIN="${WD_BIN:-$HOME/.local/bin/gt}"
MAN="${WD_MANIFEST:-$HOME/.local/bin/PINNED-BUILD.generated.md}"
STATE="${WD_STATE_DIR:-$HOME/gt}"
DEB="${WD_DEBOUNCE:-30}"
BD="${WD_BD:-$HOME/.local/bin/bd}"
FORK_MAN="${WD_FORK_MANIFEST:-}"   # optional anchor: fork-committed manifest copy
RED="$STATE/PROVENANCE-RED"
LOCK="$STATE/.provenance-watch.lock"

mkdir -p "$STATE"
exec 9>"$LOCK"; flock -n 9 || exit 0   # another instance is running

T() { timeout 60 "$@"; }               # bound every external call
mget() { sed -n "s/^$1: *//p" "$MAN" 2>/dev/null | head -1; }

check_gt() {  # -> 0 green | 1 red (reason on stdout)
  [ -f "$BIN" ] || { echo "live binary missing: $BIN"; return 1; }
  [ -f "$MAN" ] || { echo "generated manifest missing: $MAN"; return 1; }
  local msha aid psh bsha
  msha="$(mget binarySha256)"; aid="$(mget attestationId)"; psh="$(mget patchSetHash)"
  [ -n "$msha" ] && [ -n "$aid" ] && [ -n "$psh" ] || { echo "manifest incomplete (missing fields)"; return 1; }
  bsha="$(T sha256sum "$BIN" | awk '{print $1}')"
  [ "$bsha" = "$msha" ] || { echo "sha mismatch: binary=${bsha:0:12} manifest=${msha:0:12}"; return 1; }
  # exec-free stamp presence: ldflags -X strings live in the binary's data section
  T grep -aq -- "$aid" "$BIN" || { echo "attestationId $aid not present in binary bytes"; return 1; }
  T grep -aq -- "$psh" "$BIN" || { echo "patchSetHash not present in binary bytes"; return 1; }
  # optional anchor: local manifest must match the fork-committed copy (spoof => auditable push)
  if [ -n "$FORK_MAN" ] && [ -f "$FORK_MAN" ]; then
    cmp -s "$MAN" "$FORK_MAN" || { echo "local manifest != fork-committed manifest (possible local 'repair')"; return 1; }
  fi
  # PATH-shadowing guard (lens 3): what agents resolve must BE the watched path
  if [ -z "${WD_SKIP_PATH_GUARD:-}" ] && command -v gt >/dev/null 2>&1; then
    [ "$(command -v gt)" = "$BIN" ] || { echo "PATH shadowing: gt resolves to $(command -v gt), not $BIN"; return 1; }
  fi
  return 0
}

check_bd() {  # digest-pin: baseline on first sight; ack-gated change (lens 2 bonus)
  [ -f "$BD" ] || return 0
  local pin="$STATE/.bd-pin.sha256" cur
  cur="$(T sha256sum "$BD" | awk '{print $1}')"
  if [ ! -f "$pin" ]; then printf '%s\n' "$cur" > "$pin"; return 0; fi
  [ "$cur" = "$(cat "$pin")" ] && return 0
  if [ -f "$STATE/PROVENANCE-ACK-${cur:0:12}" ]; then
    printf '%s\n' "$cur" > "$pin"; rm -f "$STATE/PROVENANCE-ACK-${cur:0:12}"
    return 0   # acknowledged legitimate bd deploy: re-pin
  fi
  echo "bd changed without ack: ${cur:0:12} (pinned $(cut -c1-12 "$pin")); legit deploy => touch $STATE/PROVENANCE-ACK-${cur:0:12}"
  return 1
}

run_checks() { local r1 r2 rc=0
  r1="$(check_gt)" || rc=1
  r2="$(check_bd)" || rc=1
  printf '%s\n%s\n' "$r1" "$r2" | sed '/^$/d'
  return $rc
}

reason="$(run_checks)"; ok=$?
if [ $ok -ne 0 ]; then
  sleep "$DEB"                          # debounce: a mid-deploy state is transient (FR-08)
  reason="$(run_checks)"; ok=$?
fi

if [ $ok -eq 0 ]; then
  if [ -f "$RED" ]; then rm -f "$RED"; echo "GREEN: sentinel cleared"; fi
  exit 0
fi

sha="$(T sha256sum "$BIN" 2>/dev/null | awk '{print $1}')"; sha="${sha:-unknown}"
sha12="${sha:0:12}"
prev_sha12="$(sed -n 's/^sha12: *//p' "$RED" 2>/dev/null | head -1)"
acked="no"; [ -f "$STATE/PROVENANCE-ACK-$sha12" ] && acked="yes"

{ echo "PROVENANCE INCIDENT — live gt failed coherence"
  echo "detectedAt: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "sha12: $sha12"
  echo "acknowledged: $acked"
  echo "reason: $reason"
  echo "verify: deploy/coherence-check.sh $BIN $MAN   (from the fork checkout)"
  echo "policy: do NOT deploy or trust gt output until resolved; an attested=false live binary is an incident (INSP-GTPROV-SR-01)."
  echo "rollback-ack: touch $STATE/PROVENANCE-ACK-$sha12"
} > "$RED"

# once-per-sha escalation (NFR-02): same offending sha as the existing sentinel => already escalated
if [ "$acked" = "no" ] && [ "$prev_sha12" != "$sha12" ]; then
  T gt escalate -s CRITICAL "Provenance: live gt failed coherence (sha12 $sha12) — see ~/gt/PROVENANCE-RED" >/dev/null 2>&1 || true
fi
echo "RED: $reason"
exit 1
