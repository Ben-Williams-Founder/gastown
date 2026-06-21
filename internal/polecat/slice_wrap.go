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

// PolecatSliceEnv is the env var that activates cgroup-slice placement of polecat
// sessions. When set (e.g. GT_POLECAT_SLICE=polecat.slice), each polecat's startup
// command is wrapped so its whole process tree runs under that systemd --user slice,
// giving the box-optimizer actuator a real CPUWeight throttle target (wkb-1dy6).
//
// Placement is resolved from TWO sources so it is INDEPENDENT of the spawning
// process's ambient env (wkb-h468). Before this, placement only happened when
// GT_POLECAT_SLICE was present in the env of whatever spawned the polecat: the
// daemon's systemd unit sets it (so autonomous dispatch placed correctly), but a
// manual `gt scheduler run` / `gt sling` from a shell WITHOUT that var spawned
// polecats into app.slice (unthrottleable). The persisted config key removes that
// dependence on the caller's environment.
//
// Resolution precedence (see resolveSlice):
//  1. GT_POLECAT_SLICE env  — per-invocation override; keeps working unchanged.
//  2. polecat.slice config   — persisted town setting (settings/config.json,
//     PolecatConfig.Slice). Set once with `gt config set polecat.slice polecat.slice`;
//     then BOTH daemon and manual dispatch place consistently.
//  3. empty                  — no placement (today's default).
//
// Design (deliberately conservative — this is the daemon spawn path):
//   - DEPLOY-NEUTRAL BY DEFAULT. With neither the env nor the config key set, the
//     command is returned byte-identical, so the binary ships with zero behaviour
//     change. Activation is a one-time `gt config set polecat.slice polecat.slice`
//     (or the daemon-env flip) — no binary swap needed to enable or disable.
//   - KILL-SWITCH (reversible). Placement is force-disabled — even when configured —
//     by either of:
//       * a config value of "off" / "none" (or empty); OR
//       * a sentinel file `$GT_TOWN_ROOT/NO_POLECAT_PLACEMENT` (NoPlacementSentinel).
//     Either returns the command UNWRAPPED. The sentinel works without touching
//     config and is the fastest way to halt placement town-wide in an incident.
//   - FAIL-OPEN. On a non-Linux host or where systemd-run is absent, the command is
//     returned unchanged (the fork also runs on macOS). So enabling placement on a
//     host without systemd-run can never break spawn.
//   - POLECAT-SCOPED BY CONSTRUCTION. Only the polecat SessionManager calls this, so
//     the control plane (T0: mayor/refinery/Dolt) and witnesses (T1) are never placed
//     in the throttle slice.
//   - FORMAT-INDEPENDENT. The original shell line is run verbatim under
//     `/bin/sh -c`, inside a transient scope in the slice, so it does not matter
//     whether the command begins with `exec env …` or anything else.
const PolecatSliceEnv = "GT_POLECAT_SLICE"

// NoPlacementSentinel is the basename of the kill-switch file under the town root.
// When `$GT_TOWN_ROOT/NO_POLECAT_PLACEMENT` exists, slice placement is force-disabled
// regardless of env or config (fast, reversible: create to disable, remove to restore).
const NoPlacementSentinel = "NO_POLECAT_PLACEMENT"

// sliceDisableValues are config values that explicitly mean "do not place".
// They let an operator disable placement via config alone (`gt config set
// polecat.slice off`) without removing the configured slice name elsewhere.
var sliceDisableValues = map[string]bool{
	"off":  true,
	"none": true,
}

// wrapInSlice optionally wraps a polecat startup command so its process tree runs
// under the resolved cgroup slice. Returns the command unchanged when placement is
// off, disabled, or unavailable (see PolecatSliceEnv / resolveSlice).
//
// townRoot is the active town root; it is used both to load the persisted
// polecat.slice config and to locate the NO_POLECAT_PLACEMENT kill-switch sentinel.
func wrapInSlice(command, townRoot string) string {
	slice := resolveSlice(os.Getenv(PolecatSliceEnv), townRoot, configuredPolecatSlice, sentinelExists)
	return wrapInSliceWith(command, slice, runtime.GOOS, exec.LookPath)
}

// configuredPolecatSlice reads the persisted polecat.slice town setting. It never
// errors or writes: a missing/unreadable settings file yields "" (inert). Injected
// into resolveSlice so the resolver is testable with no real town on disk.
func configuredPolecatSlice(townRoot string) string {
	if townRoot == "" {
		return ""
	}
	ts, err := config.LoadOrCreateTownSettings(config.TownSettingsPath(townRoot))
	if err != nil || ts == nil || ts.Polecat == nil {
		return ""
	}
	return ts.Polecat.Slice
}

// sentinelExists reports whether the NO_POLECAT_PLACEMENT kill-switch file exists
// under townRoot. Injected into resolveSlice for testability.
func sentinelExists(townRoot string) bool {
	if townRoot == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(townRoot, NoPlacementSentinel))
	return err == nil
}

// resolveSlice is the pure, testable placement-source resolver. All impure inputs
// (env value, config lookup, sentinel check) are injected. It returns the slice to
// place into, or "" for no placement.
//
// Precedence: kill-switch sentinel (force-off) → env override → config fallback →
// empty. A config value of "off"/"none"/empty also means no placement.
func resolveSlice(envSlice, townRoot string, lookupConfig func(string) string, sentinel func(string) bool) string {
	// Kill-switch sentinel wins over everything: force-off, reversible.
	if sentinel != nil && sentinel(townRoot) {
		return ""
	}
	// 1. Per-invocation env override (keeps the original behaviour working).
	if s := strings.TrimSpace(envSlice); s != "" {
		if sliceDisableValues[strings.ToLower(s)] {
			return "" // explicit env kill-switch
		}
		return s
	}
	// 2. Persisted config fallback — this is what makes manual dispatch place too.
	if lookupConfig != nil {
		if s := strings.TrimSpace(lookupConfig(townRoot)); s != "" {
			if sliceDisableValues[strings.ToLower(s)] {
				return "" // explicit config kill-switch
			}
			return s
		}
	}
	// 3. Neither set — deploy-neutral default: no placement.
	return ""
}

// wrapInSliceWith is the pure, testable core: all impure inputs (resolved slice,
// GOOS, the systemd-run lookup) are injected so the transform can be asserted with
// no real host.
func wrapInSliceWith(command, slice, goos string, lookPath func(string) (string, error)) string {
	slice = strings.TrimSpace(slice)
	if slice == "" {
		return command // inert: feature not enabled / disabled
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
	return fmt.Sprintf(
		"exec systemd-run --user --scope --slice=%s --quiet --collect -- /bin/sh -c %s",
		slice, quoted,
	)
}
