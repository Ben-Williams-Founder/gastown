package polecat

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// CompletionCloseEnv is the env var that activates deterministic, event-driven
// closure of a polecat's own mol-polecat-work step-wisp chain when its
// merge-request (active_mr) is CONFIRMED merged (WS2 Rung-3).
//
// Today those step-wisps (Load-context → … → Submit) are only swept by an
// external systemd timer (~15min lag). Folding the close into the daemon makes
// it event-driven and deterministic: completion is a KNOWN FACT at
// merge-confirm time (AssessActiveMR says the MR is stale/terminal AND the
// source issue is terminal), not a guess from a polling predicate. Once proven,
// the external timer can be retired.
//
// Design (deliberately conservative — this runs inside the live town daemon;
// the whole town depends on it):
//
//   - INERT BY DEFAULT. Unset => EvaluateCompletionClose is a no-op that closes
//     nothing, so the binary ships byte-identical to today. Activation is a
//     reversible daemon-env flip (add Environment=GT_COMPLETION_CLOSE=1 to the
//     daemon unit, restart) — no binary swap needed to enable or disable.
//   - CLOSE ONLY ON CONFIRMED MERGE. We close iff AssessActiveMR reports the MR
//     is NOT pending (stale/terminal + source issue terminal). An in-flight or
//     unverifiable MR is left strictly alone. A false leave-open is free (the
//     external timer still sweeps it); a false close is the thing to never do.
//   - FAIL-OPEN. Any error — missing data, bd failure, lookup error, non-Linux
//     host — logs and leaves wisps OPEN. This function never returns a fatal
//     error and never panics; the daemon must never break because of it.
//   - IDEMPOTENT. The injected close func is expected to skip already-closed
//     issues (the bd-close path does), so re-running is a no-op.
//   - POLECAT-SCOPED BY CONSTRUCTION. We resolve and close ONLY the molecule
//     attached to this polecat's own source issue, plus that molecule's step
//     descendants. No other polecat's chain is ever touched.
const CompletionCloseEnv = "GT_COMPLETION_CLOSE"

// MoleculeCloser closes a molecule root and all of its step descendants.
// Implementations reuse the existing deterministic bd-close path (the same
// `bd list --parent` / `bd close` walk the witness orphan-close uses). It must
// be idempotent (skipping already-closed issues) and must return the count of
// issues it closed. Returning an error leaves the caller to fail open (log only).
type MoleculeCloser func(moleculeID string) (closed int, err error)

// CompletionCloseInput carries the per-polecat facts needed to decide whether
// to close the polecat's mol-polecat-work chain. All impure inputs are injected
// so the decision can be asserted with no real host, bd, or env.
type CompletionCloseInput struct {
	// Env is the raw value of $GT_COMPLETION_CLOSE (injected, not read here, so
	// tests need not mutate process env). Empty/blank => feature inert.
	Env string
	// GOOS is the target OS (injected). Non-linux fails open (the external
	// sweeper + bd live on the linux town host).
	GOOS string

	// PolecatName is for log/return context only.
	PolecatName string
	// ActiveMR is the polecat's active_mr bead ID. Empty => nothing to confirm,
	// no-op (we never close without a confirmed-merged MR).
	ActiveMR string
	// SourceIssueHint is the polecat's work/source issue (hook_bead, or
	// last_source_issue once the hook is cleared). Used both to confirm the MR
	// is terminal (via AssessActiveMR) and to resolve the attached molecule.
	SourceIssueHint string
	// GitSafe reports whether the polecat's git state is clean enough to treat
	// the MR as fully landed. RequireGitSafe is always set, so an unsafe/unknown
	// git state keeps the MR pending => no close.
	GitSafe bool
}

// CompletionCloseResult is the outcome of an evaluation. It is purely
// informational — the daemon logs it. Closed>0 means wisps were closed.
type CompletionCloseResult struct {
	Enabled    bool   // feature flag was on
	Attempted  bool   // we determined the MR was merged and tried to close
	MoleculeID string // resolved mol-polecat-work root (empty if none/unresolved)
	Closed     int    // number of issues closed
	Reason     string // human-readable explanation (always set)
}

// EvaluateCompletionClose is the pure, testable core of WS2 Rung-3.
//
// It decides — from injected facts only — whether the polecat's active_mr is
// CONFIRMED merged, and if so closes that polecat's own mol-polecat-work
// molecule + step descendants via the injected closer. It never reads process
// env, never touches a real host, never panics, and never returns an error:
// every failure path leaves wisps OPEN and is reported in Result.Reason.
//
// reader classifies the active_mr (same AssessActiveMR used by recovery/reuse/
// witness). resolveMolecule maps the source issue to its attached mol-polecat-
// work root (empty string => unresolved => no-op). closer performs the
// idempotent bd-close walk.
func EvaluateCompletionClose(
	in CompletionCloseInput,
	reader IssueReader,
	resolveMolecule func(sourceIssue string) (string, error),
	closer MoleculeCloser,
) CompletionCloseResult {
	// 1. Feature gate — inert by default.
	if strings.TrimSpace(in.Env) == "" {
		return CompletionCloseResult{Reason: "disabled: " + CompletionCloseEnv + " unset"}
	}

	// 2. Fail open on non-linux: the bd/sweeper world is the linux town host.
	if in.GOOS != "" && in.GOOS != "linux" {
		return CompletionCloseResult{Enabled: true, Reason: "fail-open: non-linux host (" + in.GOOS + ")"}
	}

	// 3. Nothing to confirm without an active_mr — never close speculatively.
	if strings.TrimSpace(in.ActiveMR) == "" {
		return CompletionCloseResult{Enabled: true, Reason: "no active_mr — nothing to confirm"}
	}

	// 4. Confirm the MR is merged (a KNOWN FACT), not in-flight.
	// RequireGitSafe is always on: an unsafe/unknown git state keeps the MR
	// pending, so we leave the chain open.
	assessment := AssessActiveMR(reader, ActiveMRInput{
		ActiveMR:        in.ActiveMR,
		SourceIssueHint: in.SourceIssueHint,
		RequireGitSafe:  true,
		GitSafe:         in.GitSafe,
	})
	if assessment.Pending {
		// Pending is fail-closed inside AssessActiveMR (lookup errors, unverified
		// source, non-terminal MR all stay pending) — exactly the "when uncertain,
		// leave open" guarantee. Reason carries the specific blocker.
		return CompletionCloseResult{
			Enabled: true,
			Reason:  "MR not confirmed merged — leaving open: " + assessment.Reason,
		}
	}

	// 5. MR is CONFIRMED merged. Resolve THIS polecat's own molecule.
	if resolveMolecule == nil {
		return CompletionCloseResult{Enabled: true, Reason: "fail-open: no molecule resolver"}
	}
	moleculeID, err := resolveMolecule(strings.TrimSpace(in.SourceIssueHint))
	if err != nil {
		return CompletionCloseResult{Enabled: true, Reason: fmt.Sprintf("fail-open: molecule lookup error: %v", err)}
	}
	moleculeID = strings.TrimSpace(moleculeID)
	if moleculeID == "" {
		// No attached molecule (e.g. already swept, or a no-molecule dispatch).
		// Idempotent no-op — there is nothing to close.
		return CompletionCloseResult{Enabled: true, MoleculeID: "", Reason: "merge confirmed but no attached molecule to close"}
	}

	// 6. Close the molecule + its step descendants (idempotent bd-close walk).
	if closer == nil {
		return CompletionCloseResult{Enabled: true, MoleculeID: moleculeID, Reason: "fail-open: no closer"}
	}
	closed, err := closer(moleculeID)
	if err != nil {
		// Partial closes are fine (idempotent); report and fail open. The next
		// cycle (or the external timer) finishes the rest.
		return CompletionCloseResult{
			Enabled:    true,
			Attempted:  true,
			MoleculeID: moleculeID,
			Closed:     closed,
			Reason:     fmt.Sprintf("fail-open: close error after %d closed: %v", closed, err),
		}
	}
	return CompletionCloseResult{
		Enabled:    true,
		Attempted:  true,
		MoleculeID: moleculeID,
		Closed:     closed,
		Reason:     fmt.Sprintf("merge confirmed — closed %d issue(s) in mol-polecat-work chain %s", closed, moleculeID),
	}
}

// CompletionCloseEnvValue reads the live $GT_COMPLETION_CLOSE. Kept tiny and
// separate so EvaluateCompletionClose stays pure (env injected via Input.Env).
func CompletionCloseEnvValue() string { return os.Getenv(CompletionCloseEnv) }

// HostGOOS returns the build/runtime OS for injection into CompletionCloseInput.
func HostGOOS() string { return runtime.GOOS }
