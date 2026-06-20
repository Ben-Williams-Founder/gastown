package polecat

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// PolecatSliceEnv is the env var that activates cgroup-slice placement of polecat
// sessions. When set (e.g. GT_POLECAT_SLICE=polecat.slice), each polecat's startup
// command is wrapped so its whole process tree runs under that systemd --user slice,
// giving the box-optimizer actuator a real CPUWeight throttle target (wkb-1dy6).
//
// Design (deliberately conservative — this is the daemon spawn path):
//   - INERT BY DEFAULT. Unset => the command is returned byte-identical, so the
//     binary ships with zero behaviour change; activation is a reversible daemon-env
//     flip (add Environment=GT_POLECAT_SLICE=polecat.slice to gastown-daemon.service,
//     restart) — no binary swap needed to enable or disable.
//   - FAIL-OPEN. On a non-Linux host or where systemd-run is absent, the command is
//     returned unchanged (the fork also runs on macOS). So enabling the env on a host
//     without systemd-run can never break spawn.
//   - POLECAT-SCOPED BY CONSTRUCTION. Only the polecat SessionManager calls this, so
//     the control plane (T0: mayor/refinery/Dolt) and witnesses (T1) are never placed
//     in the throttle slice.
//   - FORMAT-INDEPENDENT. The original shell line is run verbatim under
//     `/bin/sh -c`, inside a transient scope in the slice, so it does not matter
//     whether the command begins with `exec env …` or anything else.
const PolecatSliceEnv = "GT_POLECAT_SLICE"

// wrapInSlice optionally wraps a polecat startup command so its process tree runs
// under the cgroup slice named by $GT_POLECAT_SLICE. Returns command unchanged when
// the feature is off or unavailable (see PolecatSliceEnv).
func wrapInSlice(command string) string {
	return wrapInSliceWith(command, os.Getenv(PolecatSliceEnv), runtime.GOOS, exec.LookPath)
}

// wrapInSliceWith is the pure, testable core: all impure inputs (env, GOOS, the
// systemd-run lookup) are injected so the transform can be asserted with no real host.
func wrapInSliceWith(command, slice, goos string, lookPath func(string) (string, error)) string {
	slice = strings.TrimSpace(slice)
	if slice == "" {
		return command // inert: feature not enabled
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
