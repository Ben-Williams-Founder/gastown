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
