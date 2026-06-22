package polecat

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
)

// PolecatSliceEnv is the env var that activates (or overrides) cgroup-slice
// placement of polecat sessions. When set (e.g. GT_POLECAT_SLICE=polecat.slice),
// each polecat's startup command is wrapped so its whole process tree runs under
// that systemd --user slice, giving the box-optimizer actuator a real CPUWeight
// throttle target (wkb-1dy6).
//
// Resolution precedence (wkb-h468 — close the manual-dispatch placement gap):
//
//  1. ENV WINS IF PRESENT. If GT_POLECAT_SLICE is present in the environment it is
//     authoritative — including an explicit empty value, which DISABLES placement.
//     This preserves the original reversible "flip the daemon env" control AND
//     gives an operator an escape hatch (GT_POLECAT_SLICE= gt sling … spawns
//     unplaced). Presence is checked with os.LookupEnv so "set-but-empty" is
//     distinct from "unset".
//  2. ELSE TOWN CONFIG. The `polecat.cgroup_slice` key in town-settings.json
//     (config.PolecatConfig.CgroupSlice). This is the durable source that makes
//     placement apply regardless of the spawning shell's env, so a MANUAL
//     `gt sling` / `gt scheduler run` from an operator shell that lacks the env
//     still lands the polecat in the slice (the gap this fix closes).
//  3. ELSE A SAFE DEFAULT of DefaultPolecatSlice ("polecat.slice"). Placement is
//     always best-effort and FAIL-OPEN at spawn time: on a non-Linux host, where
//     systemd-run is absent, or where the user bus / slice is unreachable, the
//     command runs UNPLACED (the pre-feature default) rather than not at all. So
//     defaulting the slice on can never block a spawn — worst case is an
//     unthrottled polecat, exactly as before the feature existed.
//
// Properties preserved from the original design:
//   - FAIL-OPEN. Never blocks a spawn on placement (see wrapInSliceWith).
//   - POLECAT-SCOPED BY CONSTRUCTION. Only the polecat SessionManager calls this,
//     so the control plane (T0: mayor/refinery/Dolt) and witnesses (T1) are never
//     placed in the throttle slice.
//   - FORMAT-INDEPENDENT. The original shell line is run verbatim under
//     `/bin/sh -c` inside a transient scope in the slice.
const PolecatSliceEnv = "GT_POLECAT_SLICE"

// DefaultPolecatSlice is the safe hardcoded fallback slice used when neither the
// GT_POLECAT_SLICE env var nor the polecat.cgroup_slice config key is set. It is
// only ever materialised by systemd-run when the user bus + slice are available;
// otherwise placement fails open and the polecat runs unplaced.
const DefaultPolecatSlice = "polecat.slice"

// resolveSlice computes the target slice name using the precedence documented on
// PolecatSliceEnv. It is the pure, testable core of placement resolution: the
// environment lookup and the config value are both injected.
//
//   - envVal/envSet model os.LookupEnv: envSet=true means the var is present
//     (envVal is its value, possibly ""); envSet=false means it is unset.
//   - configSlice is the town-config value (PolecatConfig.CgroupSlice), "" if unset.
//
// A returned "" means "do not place" (placement disabled) — wrapInSliceWith then
// returns the command unchanged.
func resolveSlice(envVal string, envSet bool, configSlice string) string {
	if envSet {
		// Env is authoritative when present, INCLUDING an explicit empty value
		// (operator escape hatch to disable placement for a one-off spawn).
		return strings.TrimSpace(envVal)
	}
	if s := strings.TrimSpace(configSlice); s != "" {
		return s
	}
	return DefaultPolecatSlice
}

// wrapInSlice optionally wraps a polecat startup command so its process tree runs
// under the resolved cgroup slice (see PolecatSliceEnv for precedence). Returns
// the command unchanged when placement is disabled or unavailable.
func (m *SessionManager) wrapInSlice(command string) string {
	envVal, envSet := os.LookupEnv(PolecatSliceEnv)
	slice := resolveSlice(envVal, envSet, m.configSlice())
	return wrapInSliceWith(command, slice, runtime.GOOS, exec.LookPath)
}

// configSlice reads the durable `polecat.cgroup_slice` town-config value for this
// rig's town. Mirrors Manager.targetCleanPolicy: any load error or missing config
// yields "" (no override) so spawn is never blocked on config IO.
func (m *SessionManager) configSlice() string {
	if m == nil || m.rig == nil || m.rig.Path == "" {
		return ""
	}
	townRoot := filepath.Dir(m.rig.Path)
	settings, err := config.LoadOrCreateTownSettings(config.TownSettingsPath(townRoot))
	if err != nil || settings == nil || settings.Polecat == nil {
		return ""
	}
	return strings.TrimSpace(settings.Polecat.CgroupSlice)
}

// wrapInSliceWith is the pure, testable core of the spawn-time wrap: all impure
// inputs (the resolved slice, GOOS, the systemd-run lookup) are injected so the
// transform can be asserted with no real host. An empty slice is a no-op.
func wrapInSliceWith(command, slice, goos string, lookPath func(string) (string, error)) string {
	slice = strings.TrimSpace(slice)
	if slice == "" {
		return command // placement disabled
	}
	if goos != "linux" {
		return command // fail-open: cgroup slices are a systemd/linux concept
	}
	if lookPath != nil {
		if _, err := lookPath("systemd-run"); err != nil {
			return command // fail-open: no systemd-run on PATH
		}
	}
	// Run the original shell line verbatim under a transient --user scope in the slice.
	// Single-quote the line so its own quoting/exec/env survive intact.
	quoted := "'" + strings.ReplaceAll(command, "'", `'\''`) + "'"
	// RUNTIME FAIL-OPEN (wkb-h468 regression fix). `systemd-run --user` connects to
	// the per-user systemd manager via $XDG_RUNTIME_DIR / $DBUS_SESSION_BUS_ADDRESS.
	// Those are NOT propagated into the polecat's tmux pane on the daemon dispatch
	// path, so systemd-run exits 1 there and — when the line begins with `exec` — the
	// pane dies and the polecat never starts (spawn fails → dispatch fails → the sling
	// context circuit-breaks → "Scheduled: 0", a town-wide dispatch outage).
	//
	// Two guards make placement strictly best-effort and NEVER able to block spawn:
	//   1. NO leading `exec`: systemd-run runs as a child, not a process replacement.
	//   2. `|| exec /bin/sh -c <cmd>`: if placement fails for ANY reason (no user bus,
	//      slice missing, transient scope-name clash), fall through and run the polecat
	//      UNPLACED rather than not at all. Worst case = an unthrottled polecat (the
	//      pre-feature default), never a dead one.
	// On success the inner command is exec-replaced inside the scope, so the `||`
	// branch is never reached — placement still works exactly as before where the bus
	// is reachable.
	return fmt.Sprintf(
		"systemd-run --user --scope --slice=%s --quiet --collect -- /bin/sh -c %s || exec /bin/sh -c %s",
		slice, quoted, quoted,
	)
}
