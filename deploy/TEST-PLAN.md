# Provenance-contract devcontainer test plan

Maps the isolated-container drill (`container-drill.sh` in `Dockerfile.provenance`)
to `COMP-gt-provenance-se-rvtm` rows. Design principles (adversarial-review
driven): the container **starts empty** (FR-01 is the run itself), the **negative
matrix outweighs the happy path** (the requirement is "cannot silently not work"),
and the container **builds gt from the mounted fork mirror** — never a baked
upstream binary (the recorded container/A1 supply-chain gap).

## Environment

- Image: `golang:1.26-alpine` + git/bash/tmux/coreutils/binutils. No dolt/bd/town
  services, no prod ports (:3307 untouched), container-local tmux (no `$TMUX`,
  no `gt-7f7002` collision), no credentials of any kind (mirror is a read-only
  bind mount; test writes stay in `/scratch`).
- Mounts: `/mirror` RO (bare mirror: `provenance-tooling`, `deploy-cp-guard`,
  `upstream-main`) · `/modcache` RO (host Go module cache as offline `file://`
  proxy; network fallback allowed for public modules only) · `/scratch` RW ·
  `/out` RW (evidence).

## Matrix

| # | RVTM row | Case | Expected |
|---|---|---|---|
| T1 | FR-01 | Clone from bare mirror alone → tooling + lineage present | ok |
| T2 | setup | Reference "live-sim" binary builds @ recorded lineage commit | ok |
| T3 | FR-05 | `attest.sh`: G1 superset 0-missing + G2 patches PASS → attestation | ok |
| T4 | FR-05 | `deploy-gt.sh --dry-run`: attested build + stamp self-check + generated manifest | ok |
| T5 | FR-05 | `gt version --provenance`: `attested=true`, `verifiedBase` == lineage commit | ok |
| T6 | NFR-01 | `coherence-check.sh`: TSV == stamp == manifest, one command | ok |
| N1 | FR-05 | Attestation deleted → `deploy-gt.sh` **refuses** | refuse |
| N2 | FR-05 | Tree changed after attestation → **refuses** (tree-hash mismatch) | refuse |
| N3 | FR-04 | TSV row removed → attest gate **RED** (UNGUARDED) | refuse |
| N4 | FR-04 | Signature corrupted (absent from binary) → **RED** (DROPPED) | refuse |
| N5 | NFR-01 | Manifest hand-edited → coherence **RED**, names mismatch | refuse |
| T7a-e | FR-02 | Installer: fresh ok · current no-op · 3-way divergence **REFUSED** (hotfix intact) · normal update ok | mixed |
| T8a-d | FR-03 | Idle-gate: deacon-only 0 · +worker 1 (busy) · +witness 0 · no-socket 1 (fail-safe) | mixed |

## Out of scope (and why)

- **SR-01** (enforced single deploy path): flips only on the first *live* Stage-1
  deploy — a container cannot prove the live box has no alternate path.
- **`--live` install mode**: exercised only up to staging semantics via FR-02's
  installer tests; the real atomic swap over `~/.local/bin/gt` is the live deploy.
- Town runtime (dolt/bd/scheduler): unaffected by this tooling; covered by the
  existing e2e harness.

## Evidence out

`/out/RESULTS.md` (verdict table) + per-case logs + `attestation.json` +
`PINNED-BUILD.generated.md` + `provenance.txt` — feed T-/DEMO- verification
records for the RVTM rows above.

## Watchdog addendum (DEC-OPS-gt-provenance-watchdog; 2026-07-31)

| # | RVTM row | Case | Expected |
|---|---|---|---|
| T9a/T9a2 | FR-06 | Coherent pair → GREEN, no sentinel, bd baselined | ok |
| N6 | FR-06 | Tampered binary → RED "sha mismatch" (exec-free) | refuse |
| N7 | NFR-02 | Repeat RED same sha → stable sha-keyed sentinel | ok |
| T9b/T9b2 | NFR-02 | Restored coherence → GREEN auto-clears sentinel | ok |
| N8 | FR-06 | Tampered manifest → RED | refuse |
| T9c | NFR-02 | ACK-<sha12> → RED visible, acknowledged, escalation suppressed | ok |
| T9d | FR-08 | Mid-deploy incoherence resolving in debounce → NO false RED | ok |
| N9/N9b | FR-06/NFR-02 | bd digest-pin: unacked change RED; acked change re-pins | mixed |
| T10a/b | FR-07 | Consumer hook injects iff sentinel present | ok |

**Out of container scope** (live-enable verification): systemd unit *wiring*
(no systemd in the drill container — units syntax-reviewed only), real
`gt escalate` emission, PATH guard on the real box (`WD_SKIP_PATH_GUARD=1` in
drill), fork-manifest anchor (needs the fork checkout).
