# Gas Town role-images — automated pin-propagation (step 1)

Three runtime images, each baking the **same pinned, gated fork `gt`** so a
container is byte-identical to the VM (per `DEC-OPS-gt-supply-chain-build`):

| Image | Posture | Role |
|---|---|---|
| `gastown-sandbox` | writable root + sudo, no systemd | experiment box (mirrors dev-linux-01) |
| `gastown-ci` | non-root, no passwordless sudo, no writable system | most-restrictive test target |
| `gastown-townhost` | systemd as PID 1, OCI A1 arm64 | full self-contained town (B-hosting) |

## How propagation works
`.github/workflows/role-images.yml` runs on every gt version tag:
1. **build-gt** — static CGO build (portable like prod) + `govulncheck` gate.
2. **rebake** (matrix: sandbox/ci/townhost) — bake the new gt + pinned bd/dolt,
   **smoke-test** (version match; ci lockdown; town-host systemd-PID1 boot),
   push **digest-pinned** to GHCR.

The BUILD is automated; the **VM digest-bump stays human-gated** via Renovate
(`renovate.json` → propose digest-bump PR → you merge → `systemctl restart`).
No silent auto-update on the metal — matches `DEC-OPS-town-host-container-deployment`.

## One-time setup before first run
- **secret `GHCR_PAT`** — PAT with `write:packages` on the **Whiz-Digital-VC** org
  (images live there; this fork is a different owner, so the default `GITHUB_TOKEN`
  can't push to the org registry).
- **bd/dolt pins** — `BD_VERSION`/`DOLT_VERSION` env. Sources confirmed:
  dolt = `dolthub/dolt` releases; bd = **`gastownhall/beads`** releases
  (`beads_<ver>_linux_<arch>.tar.gz`, checksum-verified against `checksums.txt`).
  ⚠️ The live VM runs bd **1.0.5** (a dev build); latest **published** release is
  **1.0.4** — the workflow pins the published one. Align the VM to a published
  release (or bump `BD_VERSION` when gastownhall cuts 1.0.5) to keep container ≡ VM.
- **arm64 for the A1 town-host** — today's build is amd64; add `GOARCH=arm64` +
  `--platform linux/arm64` (buildx/QEMU) before deploying town-host to OCI A1.
