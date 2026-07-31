#!/usr/bin/env bash
# container-drill.sh — devcontainer verification drill for the provenance contract.
# Runs INSIDE the isolated container (see Dockerfile.provenance). Maps to RVTM
# COMP-gt-provenance-se-rvtm rows FR-01/02/03/05, NFR-01 (+ negative matrix).
#
# Container contract:
#   /mirror   RO  bare mirror of the fork repo (branches: provenance-tooling,
#                 deploy-cp-guard, upstream-main). THE ONLY SOURCE OF TRUTH —
#                 restore drill = everything reconstructs from here alone.
#   /modcache RO  host Go module cache exposed as a file:// proxy (offline-first).
#   /out      RW  evidence: RESULTS.md, attestation, manifest, logs.
#   No $TMUX, no host town state, no network dependency beyond Go proxy fallback.
set -uo pipefail
export GT_MAIN_REF=origin/upstream-main   # mirror branches appear as remote refs in the clone
export GOMODCACHE=/scratch/gomod GOCACHE=/scratch/gocache
export CGO_ENABLED=0   # container tests the MECHANISM cgo-less on musl; live G1 runs against the TRUE live binary with the live (cgo-on) recipe
export GOPROXY="file:///modcache/cache/download,https://proxy.golang.org"
export GOFLAGS=-buildvcs=true
mkdir -p /scratch /out

PASS=0; FAIL=0; declare -a ROWS
ck() { # ck <id> <rvtm-row> <desc> <expected: ok|refuse> <actual-exit>
  local id="$1" row="$2" desc="$3" want="$4" got="$5" verdict
  if { [ "$want" = ok ] && [ "$got" -eq 0 ]; } || { [ "$want" = refuse ] && [ "$got" -ne 0 ]; }
  then verdict=PASS; PASS=$((PASS+1)); else verdict=FAIL; FAIL=$((FAIL+1)); fi
  ROWS+=("| $id | $row | $desc | $want | exit=$got | **$verdict** |")
  echo "[$verdict] $id ($row): $desc (want=$want got=exit:$got)"
}
ckr() { # ckr <id> <rvtm-row> <desc> <actual-exit> <logfile> <marker>
  # SEMANTIC refusal check: exit!=0 alone is NOT a pass (a broken harness also
  # exits nonzero — the false-green trap). The refusal message must be present.
  local id="$1" row="$2" desc="$3" got="$4" log="$5" marker="$6" verdict
  if [ "$got" -ne 0 ] && grep -q "$marker" "$log" 2>/dev/null
  then verdict=PASS; PASS=$((PASS+1)); else verdict=FAIL; FAIL=$((FAIL+1)); fi
  ROWS+=("| $id | $row | $desc | refuse+\"$marker\" | exit=$got | **$verdict** |")
  echo "[$verdict] $id ($row): $desc (want=refuse+'$marker' got=exit:$got)"
}

echo "=== P0 preconditions (headless, isolated) ==="
[ -z "${TMUX:-}" ] || { echo "FATAL: \$TMUX set — not headless"; exit 2; }
# Container runs as root; the RO-mounted mirror is owned by the host uid. Without
# this, git aborts every operation with "dubious ownership" (first-run failure).
git config --global --add safe.directory '*'
# /scratch persists across runs for the Go caches — clean the WORK dirs only
# (a stale /scratch/repo from a prior run must never masquerade as this run's
# restore; run-3 lesson: clone refused a non-empty destination, by design).
rm -rf /scratch/repo /scratch/live-src /scratch/live-sim-gt /scratch/att.bak /scratch/n1 /scratch/n2 /scratch/fr02 /scratch/wd /scratch/wd-state
{ echo "go: $(go version)"; echo "git: $(git --version)"; echo "tmux: $(tmux -V)"; } | tee /out/env.txt

echo "=== T1 (FR-01) restore drill: full chain from /mirror alone ==="
# Fail FAST if the restore itself fails — every later case is meaningless noise
# against a missing repo (lesson from run 1: 18 invalid verdicts).
git clone -q /mirror /scratch/repo        || { echo "FATAL: clone from /mirror failed"; exit 2; }
git -C /scratch/repo checkout -q provenance-tooling || { echo "FATAL: checkout failed"; exit 2; }
LIVE_COMMIT="$(git -C /scratch/repo rev-parse origin/deploy-cp-guard)" || { echo "FATAL: lineage ref missing"; exit 2; }
ls /scratch/repo/deploy/attest.sh /scratch/repo/deploy/deploy-gt.sh \
   /scratch/repo/deploy/verify-fork-patches.sh /scratch/repo/deploy/fork-patch-signatures.tsv \
   /scratch/repo/deploy/coherence-check.sh /scratch/repo/deploy/install-tool.sh \
   /scratch/repo/deploy/gt-idle-check >/dev/null 2>&1
ck T1 FR-01 "tooling + lineage reconstructed from bare mirror alone" ok $?

echo "=== T2 setup: build live-sim reference binary @ deploy-cp-guard (${LIVE_COMMIT:0:9}) ==="
git -C /scratch/repo worktree add -q /scratch/live-src "$LIVE_COMMIT"
( cd /scratch/live-src && CGO_ENABLED=0 go build -o /scratch/live-sim-gt ./cmd/gt )
ck T2 setup "reference (live-sim) binary builds from recorded lineage commit" ok $?

D=/scratch/repo/deploy
echo "=== T3 (FR-05) happy path: attest -> gated build -> stamp self-check ==="
( cd /scratch/repo && GO=go "$D/attest.sh" /scratch/live-sim-gt "$LIVE_COMMIT" ) > /out/attest.log 2>&1
ck T3 FR-05 "attest.sh: G1 superset 0-missing + G2 patches PASS -> attestation emitted" ok $?
( cd /scratch/repo && GO=go "$D/deploy-gt.sh" --dry-run /out/deploy ) > /out/deploy.log 2>&1
ck T4 FR-05 "deploy-gt.sh --dry-run: stamped build + self-check + generated manifest" ok $?
cp "$D/.attest/attestation.json" /out/ 2>/dev/null

echo "=== T5 (FR-05) stamp truth: binary reports the attested facts ==="
PROV="$(/out/deploy/gt version --provenance 2>/dev/null)"
grep -qxF "attested=true" <<<"$PROV" && grep -qxF "verifiedBase=$LIVE_COMMIT" <<<"$PROV"
ck T5 FR-05 "gt version --provenance: attested=true + verifiedBase == live lineage commit" ok $?
echo "$PROV" > /out/provenance.txt

echo "=== T6 (NFR-01) coherence: TSV == binary stamp == manifest, one command ==="
"$D/coherence-check.sh" /out/deploy/gt /out/deploy/PINNED-BUILD.generated.md > /out/coherence.log 2>&1
ck T6 NFR-01 "coherence-check PASS on untampered artifacts" ok $?

echo "=== negative matrix (the sandbox-only destructive cases) ==="
cp "$D/.attest/attestation.json" /scratch/att.bak
rm -f "$D/.attest/attestation.json"
( cd /scratch/repo && GO=go "$D/deploy-gt.sh" --dry-run /scratch/n1 ) > /out/n1.log 2>&1
ckr N1 FR-05 "no attestation -> deploy-gt.sh refuses to build" $? /out/n1.log "REFUSED: no attestation"
cp /scratch/att.bak "$D/.attest/attestation.json"

( cd /scratch/repo \
  && echo "# post-attestation drift" >> README.md && git add README.md \
  && git -c user.email=drill@test -c user.name=drill commit -qm "drift after attestation" \
  && GO=go "$D/deploy-gt.sh" --dry-run /scratch/n2 ) > /out/n2.log 2>&1
ckr N2 FR-05 "tree changed after attestation -> deploy-gt.sh refuses" $? /out/n2.log "REFUSED: attestation is for tree"
git -C /scratch/repo reset -q --hard HEAD~1

( cd /scratch/repo \
  && grep -v "IsControlPlaneBead" deploy/fork-patch-signatures.tsv > /tmp/tsv && cp /tmp/tsv deploy/fork-patch-signatures.tsv \
  && git add deploy/fork-patch-signatures.tsv \
  && git -c user.email=drill@test -c user.name=drill commit -qm "drop a signature row" \
  && GO=go "$D/attest.sh" /scratch/live-sim-gt "$LIVE_COMMIT" ) > /out/n3.log 2>&1
ckr N3 FR-04 "TSV row removed -> attest gate RED (no silent gap)" $? /out/n3.log "UNGUARDED"
git -C /scratch/repo reset -q --hard HEAD~1

( cd /scratch/repo \
  && sed -i 's/IsControlPlaneBead/IsControlPlaneBead_TAMPERED_SIGNATURE/' deploy/fork-patch-signatures.tsv \
  && git add deploy/fork-patch-signatures.tsv \
  && git -c user.email=drill@test -c user.name=drill commit -qm "corrupt a signature" \
  && GO=go "$D/attest.sh" /scratch/live-sim-gt "$LIVE_COMMIT" ) > /out/n4.log 2>&1
ckr N4 FR-04 "signature absent from binary -> attest gate RED (no false green)" $? /out/n4.log "DROPPED"
git -C /scratch/repo reset -q --hard HEAD~1

sed -i 's/^binarySha256: .*/binarySha256: 0000000000000000000000000000000000000000000000000000000000000000/' /out/deploy/PINNED-BUILD.generated.md
"$D/coherence-check.sh" /out/deploy/gt /out/deploy/PINNED-BUILD.generated.md > /out/n5.log 2>&1
ckr N5 NFR-01 "hand-edited manifest -> coherence RED, names the mismatch" $? /out/n5.log "MISMATCH: manifest binarySha256"
"$D/coherence-check.sh" /out/deploy/gt <(sed 's/^binarySha256: .*/binarySha256: RESTORED/' /out/deploy/PINNED-BUILD.generated.md) >/dev/null 2>&1 || true
# restore manifest for the evidence bundle
( cd /scratch/repo && GO=go "$D/deploy-gt.sh" --dry-run /out/deploy ) >/dev/null 2>&1

echo "=== T7 (FR-02) no-clobber installer semantics ==="
F=/scratch/fr02; mkdir -p "$F"
printf 'v1\n' > "$F/src"; "$D/install-tool.sh" "$F/src" "$F/dest" >/dev/null 2>&1
ck T7a FR-02 "fresh install: atomic + hash recorded" ok $?
"$D/install-tool.sh" "$F/src" "$F/dest" >/dev/null 2>&1
ck T7b FR-02 "re-install same content: no-op CURRENT" ok $?
printf 'v2-repo\n' > "$F/src"; printf 'hotfix-live\n' > "$F/dest"
"$D/install-tool.sh" "$F/src" "$F/dest" > /out/fr02-refuse.log 2>&1
ckr T7c FR-02 "3-way divergence (live hotfixed out-of-band) -> refused, no clobber" $? /out/fr02-refuse.log "REFUSED (3-way)"
grep -q 'hotfix-live' "$F/dest"; ck T7d FR-02 "refused install left the live hotfix intact" ok $?
printf 'v1\n' > "$F/dest"; printf "$(sha256sum "$F/dest" | awk '{print $1}')\n" > "$F/dest.installed-sha256"
"$D/install-tool.sh" "$F/src" "$F/dest" >/dev/null 2>&1
ck T7e FR-02 "normal update (live == last-installed) -> atomic UPDATE" ok $?

echo "=== T8 (FR-03) idle-gate busy path (closes the provisional) ==="
IC="$D/gt-idle-check"
tmux new-session -d -s hq-deacon 'sleep 600'
env -u TMUX "$IC" >/dev/null 2>&1
ck T8a FR-03 "deacon only -> exit 0 (idle; mechanical boot)" ok $?
tmux new-session -d -s wkb-test 'sleep 600'
env -u TMUX "$IC" > /out/fr03-busy.log 2>&1
ckr T8b FR-03 "rig-worker session present -> busy (AI Boot preserved)" $? /out/fr03-busy.log "rig-worker session"
tmux kill-session -t wkb-test
tmux new-session -d -s wkb-witness 'sleep 600'
env -u TMUX "$IC" >/dev/null 2>&1
ck T8c FR-03 "witness (infra) session -> still exit 0 (not a worker)" ok $?
tmux kill-server 2>/dev/null; sleep 1
env -u TMUX "$IC" > /out/fr03-failsafe.log 2>&1
ckr T8d FR-03 "no deacon-hosting socket -> fail-safe full Boot" $? /out/fr03-failsafe.log "no live town socket"

echo "=== T9/N6-N9 (FR-06/FR-08/NFR-02) provenance watchdog — producer logic ==="
W=/scratch/wd; WS=/scratch/wd-state; mkdir -p "$W" "$WS"
cp /out/deploy/gt "$W/gt"; cp /out/deploy/PINNED-BUILD.generated.md "$W/man"
printf 'fake-bd-v1\n' > "$W/bd"
wd() { WD_BIN="$W/gt" WD_MANIFEST="$W/man" WD_STATE_DIR="$WS" WD_DEBOUNCE=2 WD_BD="$W/bd" WD_SKIP_PATH_GUARD=1 "$D/provenance-watch.sh"; }
wd > /out/wd-t9a.log 2>&1
ck T9a FR-06 "coherent binary+manifest -> GREEN (exit 0, no sentinel; bd baselined)" ok $?
[ ! -f "$WS/PROVENANCE-RED" ]; ck T9a2 FR-06 "no sentinel after GREEN" ok $?
printf 'X' >> "$W/gt"
wd > /out/wd-n6.log 2>&1
ckr N6 FR-06 "tampered binary -> RED sentinel, exec-free detection" $? /out/wd-n6.log "sha mismatch"
S1="$(sed -n 's/^sha12: *//p' "$WS/PROVENANCE-RED")"
wd > /out/wd-n7.log 2>&1; rc=$?
S2="$(sed -n 's/^sha12: *//p' "$WS/PROVENANCE-RED")"
[ $rc -ne 0 ] && [ "$S1" = "$S2" ] && [ -n "$S1" ]; ck N7 NFR-02 "repeat RED same sha -> stable sha-keyed sentinel (once-per-sha)" ok $?
cp /out/deploy/gt "$W/gt"
wd > /out/wd-t9b.log 2>&1
ck T9b NFR-02 "restored coherence -> GREEN auto-clears sentinel" ok $?
[ ! -f "$WS/PROVENANCE-RED" ]; ck T9b2 NFR-02 "sentinel actually removed" ok $?
sed -i 's/^binarySha256: .*/binarySha256: 1111111111111111111111111111111111111111111111111111111111111111/' "$W/man"
wd > /out/wd-n8.log 2>&1
ckr N8 FR-06 "tampered manifest -> RED (timer-class detection)" $? /out/wd-n8.log "sha mismatch"
cp /out/deploy/PINNED-BUILD.generated.md "$W/man"; wd >/dev/null 2>&1   # clear
printf 'X' >> "$W/gt"
ACK12="$(sha256sum "$W/gt" | cut -c1-12)"; touch "$WS/PROVENANCE-ACK-$ACK12"
wd > /out/wd-t9c.log 2>&1; rc=$?
[ $rc -ne 0 ] && grep -q "acknowledged: yes" "$WS/PROVENANCE-RED"; ck T9c NFR-02 "ack-<sha12> -> incident visible but acknowledged (escalation suppressed)" ok $?
cp /out/deploy/gt "$W/gt"; rm -f "$WS/PROVENANCE-ACK-$ACK12"; wd >/dev/null 2>&1
sed -i 's/^binarySha256: .*/binarySha256: 2222222222222222222222222222222222222222222222222222222222222222/' "$W/man"
( sleep 1; cp /out/deploy/PINNED-BUILD.generated.md "$W/man" ) &
WD_BIN="$W/gt" WD_MANIFEST="$W/man" WD_STATE_DIR="$WS" WD_DEBOUNCE=3 WD_BD="$W/bd" WD_SKIP_PATH_GUARD=1 "$D/provenance-watch.sh" > /out/wd-t9d.log 2>&1
ck T9d FR-08 "mid-deploy incoherence resolving within debounce -> NO false RED" ok $?
wait
printf 'fake-bd-v2\n' > "$W/bd"
wd > /out/wd-n9.log 2>&1
ckr N9 FR-06 "bd changed without ack -> RED (digest-pin)" $? /out/wd-n9.log "bd changed without ack"
BDACK="$(sha256sum "$W/bd" | cut -c1-12)"; touch "$WS/PROVENANCE-ACK-$BDACK"
wd > /out/wd-n9b.log 2>&1
ck N9b NFR-02 "acked bd change -> GREEN + re-pinned" ok $?

echo "=== T10 (FR-07) provenance watchdog — consumer injection ==="
printf 'PROVENANCE INCIDENT — drill\nsha12: deadbeef0000\n' > "$WS/PROVENANCE-RED"
WD_STATE_DIR="$WS" "$D/hooks/provenance-inject.sh" > /out/wd-t10.log 2>&1
grep -q "PROVENANCE INCIDENT" /out/wd-t10.log; ck T10a FR-07 "sentinel present -> hook injects system-reminder" ok $?
rm -f "$WS/PROVENANCE-RED"
OUT10="$(WD_STATE_DIR="$WS" "$D/hooks/provenance-inject.sh")"
[ -z "$OUT10" ]; ck T10b FR-07 "sentinel absent -> hook silent" ok $?

echo "=== results ==="
TOTAL=$((PASS+FAIL))
{
  echo "# Provenance-contract devcontainer drill — RESULTS"
  echo ""
  echo "Date: $(date -u +%Y-%m-%dT%H:%M:%SZ) · live-sim base: \`${LIVE_COMMIT}\` · verdict: **$PASS/$TOTAL PASS**"
  echo ""
  echo "| # | RVTM row | Case | Expected | Actual | Verdict |"
  echo "|---|---|---|---|---|---|"
  printf '%s\n' "${ROWS[@]}"
  echo ""
  echo "Evidence: attest.log deploy.log provenance.txt coherence.log n1-n5.log fr02-refuse.log fr03-busy.log attestation.json deploy/PINNED-BUILD.generated.md"
} > /out/RESULTS.md
cat /out/RESULTS.md
[ "$FAIL" -eq 0 ]
