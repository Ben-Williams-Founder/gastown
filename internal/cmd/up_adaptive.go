package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// Adaptive-boot integration for `gt up` (hq-lgay).
//
// `gt up` optionally invokes the machine-adaptive boot script, which detects
// host facts (CPU/RAM) and derives scheduler.max_polecats + governor thresholds.
//
// Design constraints (from hq-lgay):
//   - Gate on an EXPLICIT role flag/env — NEVER silent auto-detect-and-apply.
//   - When a role IS provided: run the script in apply mode (mayor-gated inside
//     the script; --yes added automatically when gt up is headless/non-interactive).
//   - When NO role is provided: run the script in --print mode (detect + propose,
//     apply nothing) and tell the operator to re-run with --role to apply. This
//     preserves the historical `gt up` behavior by default.
//   - FULLY fail-open + additive: a missing script, a non-town-host, a read-only
//     devcontainer, or macOS must never make `gt up` fail.

// validAdaptiveRoles are the explicit roles accepted by --role / GT_TOWN_HOST_ROLE.
// Only "town-host" actually wants the adaptive scheduler/governor tuning applied;
// "sandbox" and "dev" are accepted as explicit acknowledgements that produce a
// print-only proposal (never apply) so the operator stays in control on
// non-production hosts.
var validAdaptiveRoles = map[string]bool{
	"town-host": true,
	"sandbox":   true,
	"dev":       true,
}

// adaptiveApplyRoles are the roles for which we actually apply tuning. Only the
// dedicated town-host applies; other explicit roles get a print-only proposal.
var adaptiveApplyRoles = map[string]bool{
	"town-host": true,
}

// resolveAdaptiveRole returns the explicit role for the adaptive-boot step, the
// source it came from (for logging), and whether a role was explicitly provided.
//
// Precedence: the --role flag wins, then GT_TOWN_HOST_ROLE, then GT_ROLE. We do
// NOT auto-detect: if nothing is set we return ("", "", false) and the caller
// runs the script in print-only mode.
//
// An unrecognized role value is treated as "no explicit role" (returns false)
// so a typo can never trigger a silent apply.
func resolveAdaptiveRole(flagRole, envTownHostRole, envRole string) (role, source string, explicit bool) {
	candidates := []struct {
		val, src string
	}{
		{strings.TrimSpace(flagRole), "--role flag"},
		{strings.TrimSpace(envTownHostRole), "GT_TOWN_HOST_ROLE env"},
		{strings.TrimSpace(envRole), "GT_ROLE env"},
	}
	for _, c := range candidates {
		if c.val == "" {
			continue
		}
		if validAdaptiveRoles[c.val] {
			return c.val, c.src, true
		}
		// A non-empty but unrecognized value: ignore it (fail-safe → print-only).
		// Keep scanning lower-precedence sources in case one is valid.
	}
	return "", "", false
}

// adaptiveScriptPath resolves the adaptive-boot script location, honoring
// GOV_TOOLS (falling back to $HOME/gt/.gov-tools). Returns the path and whether
// it exists on disk.
func adaptiveScriptPath() (path string, exists bool) {
	govTools := strings.TrimSpace(os.Getenv("GOV_TOOLS"))
	if govTools == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			// No home dir and no GOV_TOOLS: cannot resolve. Fail-open.
			return "", false
		}
		govTools = filepath.Join(home, "gt", ".gov-tools")
	}
	path = filepath.Join(govTools, "tools", "ops", "adaptive-boot.sh")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, true
	}
	return path, false
}

// adaptiveStdoutIsInteractive reports whether gt up is attached to a TTY. When
// false (headless/piped/CI), we pass --yes to the apply path so the script does
// not block on a confirmation prompt that no human can answer.
func adaptiveStdoutIsInteractive() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stdin.Fd()))
}

// adaptiveBootDecision captures the resolved plan for the adaptive-boot step so
// it can be unit-tested without executing anything.
type adaptiveBootDecision struct {
	// run is false only when the script is absent (fail-open skip).
	run bool
	// mode is the operating mode: "apply" or "print".
	mode string
	// role is the explicit role (empty in print-only/no-role mode).
	role string
	// args is the full argument list passed to the script.
	args []string
	// note is an operator-facing message (e.g. why we skipped, or how to apply).
	note string
}

// decideAdaptiveBoot computes the adaptive-boot plan from inputs. Pure function:
// no side effects, fully unit-testable.
//
//   - scriptExists=false  → run=false, with a note (fail-open skip).
//   - explicit apply role → mode=apply, --role <role>; --yes if non-interactive.
//   - explicit non-apply  → mode=print, --role <role> --print (acknowledged host,
//     but we still never apply outside town-host).
//   - no explicit role    → mode=print, --print, with a "re-run with --role" note.
func decideAdaptiveBoot(scriptExists bool, role, roleSource string, explicit, interactive bool) adaptiveBootDecision {
	if !scriptExists {
		return adaptiveBootDecision{
			run:  false,
			mode: "skip",
			note: "adaptive-boot script not found; skipping host tuning (gt up unaffected)",
		}
	}

	if explicit && adaptiveApplyRoles[role] {
		args := []string{"--role", role, "--apply"}
		note := fmt.Sprintf("applying host tuning for role %q (from %s)", role, roleSource)
		if !interactive {
			args = append(args, "--yes")
			note += " [non-interactive: --yes]"
		}
		return adaptiveBootDecision{run: true, mode: "apply", role: role, args: args, note: note}
	}

	if explicit {
		// Explicit but non-applying role (sandbox/dev): propose only, never apply.
		return adaptiveBootDecision{
			run:  true,
			mode: "print",
			role: role,
			args: []string{"--role", role, "--print"},
			note: fmt.Sprintf("role %q (from %s) is print-only; proposing host tuning without applying", role, roleSource),
		}
	}

	// No explicit role: preserve historical gt up behavior — detect + propose,
	// apply nothing.
	return adaptiveBootDecision{
		run:  true,
		mode: "print",
		args: []string{"--print"},
		note: "no --role/GT_TOWN_HOST_ROLE set; proposing host tuning only. Re-run 'gt up --role town-host' to apply.",
	}
}

// runAdaptiveBoot performs the adaptive-boot integration step for gt up. It is
// fully fail-open: any error (missing script, non-zero exit, exec failure) is
// logged as a warning and never propagated, so gt up always succeeds regardless.
//
// out is where operator-facing notes/script output are written (os.Stdout in
// production; a buffer in tests). quiet suppresses the informational notes.
func runAdaptiveBoot(townRoot string, out io.Writer, quiet bool) {
	scriptPath, exists := adaptiveScriptPath()

	role, roleSource, explicit := resolveAdaptiveRole(
		adaptiveRole,
		os.Getenv("GT_TOWN_HOST_ROLE"),
		os.Getenv("GT_ROLE"),
	)

	decision := decideAdaptiveBoot(exists, role, roleSource, explicit, adaptiveStdoutIsInteractive())

	if !decision.run {
		if !quiet && decision.note != "" {
			fmt.Fprintf(out, "  adaptive-boot: %s\n", decision.note)
		}
		return
	}

	if !quiet && decision.note != "" {
		fmt.Fprintf(out, "  adaptive-boot: %s\n", decision.note)
	}

	cmd := exec.Command(scriptPath, decision.args...)
	cmd.Dir = townRoot
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		// Fail-open: never break gt up. Surface as a warning only.
		fmt.Fprintf(os.Stderr, "Warning: adaptive-boot step failed (non-fatal): %v\n", err)
	}
}
