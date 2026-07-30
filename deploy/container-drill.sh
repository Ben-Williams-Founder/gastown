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

echo "=== P0 preconditions (headless, isolated) ==="
[ -z "${TMUX:-}" ] || { echo "FATAL: \$TMUX set — not headless"; exit 2; }
{ echo "go: $(go version)"; echo "git: $(git --version)"; echo "tmux: $(tmux -V)"; } | tee /out/env.txt

echo "=== T1 (FR-01) restore drill: full chain from /mirror alone ==="
git clone -q /mirror /scratch/repo
git -C /scratch/repo checkout -q provenance-tooling
LIVE_COMMIT="$(git -C /scratch/repo rev-parse origin/deploy-cp-guard)"
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
ck N1 FR-05 "no attestation -> deploy-gt.sh REFUSES to build" refuse $?
cp /scratch/att.bak "$D/.attest/attestation.json"

( cd /scratch/repo \
  && echo "# post-attestation drift" >> README.md && git add README.md \
  && git -c user.email=drill@test -c user.name=drill commit -qm "drift after attestation" \
  && GO=go "$D/deploy-gt.sh" --dry-run /scratch/n2 ) > /out/n2.log 2>&1
ck N2 FR-05 "tree changed after attestation -> deploy-gt.sh REFUSES (tree-hash mismatch)" refuse $?
git -C /scratch/repo reset -q --hard HEAD~1

( cd /scratch/repo \
  && grep -v "IsControlPlaneBead" deploy/fork-patch-signatures.tsv > /tmp/tsv && cp /tmp/tsv deploy/fork-patch-signatures.tsv \
  && git add deploy/fork-patch-signatures.tsv \
  && git -c user.email=drill@test -c user.name=drill commit -qm "drop a signature row" \
  && GO=go "$D/attest.sh" /scratch/live-sim-gt "$LIVE_COMMIT" ) > /out/n3.log 2>&1
ck N3 FR-04 "TSV row removed -> attest gate RED (UNGUARDED commit, no silent gap)" refuse $?
git -C /scratch/repo reset -q --hard HEAD~1

( cd /scratch/repo \
  && sed -i 's/IsControlPlaneBead/IsControlPlaneBead_TAMPERED_SIGNATURE/' deploy/fork-patch-signatures.tsv \
  && git add deploy/fork-patch-signatures.tsv \
  && git -c user.email=drill@test -c user.name=drill commit -qm "corrupt a signature" \
  && GO=go "$D/attest.sh" /scratch/live-sim-gt "$LIVE_COMMIT" ) > /out/n4.log 2>&1
ck N4 FR-04 "signature absent from binary -> attest gate RED (DROPPED, no false green)" refuse $?
git -C /scratch/repo reset -q --hard HEAD~1

sed -i 's/^binarySha256: .*/binarySha256: 0000000000000000000000000000000000000000000000000000000000000000/' /out/deploy/PINNED-BUILD.generated.md
"$D/coherence-check.sh" /out/deploy/gt /out/deploy/PINNED-BUILD.generated.md > /out/n5.log 2>&1
ck N5 NFR-01 "hand-edited manifest -> coherence-check RED, names the mismatch" refuse $?
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
ck T7c FR-02 "3-way divergence (live hotfixed out-of-band) -> REFUSED, no clobber" refuse $?
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
ck T8b FR-03 "rig-worker session present -> exit 1 (busy; AI Boot preserved)" refuse $?
tmux kill-session -t wkb-test
tmux new-session -d -s wkb-witness 'sleep 600'
env -u TMUX "$IC" >/dev/null 2>&1
ck T8c FR-03 "witness (infra) session -> still exit 0 (not a worker)" ok $?
tmux kill-server 2>/dev/null; sleep 1
env -u TMUX "$IC" >/dev/null 2>&1
ck T8d FR-03 "no deacon-hosting socket -> exit 1 (fail-safe: full Boot)" refuse $?

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
