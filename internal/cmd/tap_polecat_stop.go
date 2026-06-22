package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/workspace"
)

var tapPolecatStopCmd = &cobra.Command{
	Use:   "polecat-stop-check",
	Short: "Auto-run gt done on session Stop if polecat has pending work",
	Long: `Safety net for the "idle polecat" problem: polecats that finish work
but forget to call gt done before the session ends.

This command is designed to run from a Claude Code Stop hook. It checks:
1. Whether this is a polecat session (GT_POLECAT env var)
2. Whether gt done has already run (heartbeat state is "exiting" or "idle")
3. Whether the polecat has commits on its branch

If the polecat has pending work that wasn't submitted, this command
runs gt done to submit it. If gt done already ran or there's nothing
to submit, it exits silently.

Exit codes:
  0 - No action needed (not a polecat, already done, or gt done succeeded)
  1 - gt done was attempted but failed`,
	RunE:         runTapPolecatStop,
	SilenceUsage: true,
}

func init() {
	tapCmd.AddCommand(tapPolecatStopCmd)
}

func runTapPolecatStop(cmd *cobra.Command, args []string) error {
	// Only applies to polecats
	polecatName := os.Getenv("GT_POLECAT")
	if polecatName == "" {
		return nil // Not a polecat session — nothing to do
	}

	sessionName := os.Getenv("GT_SESSION")
	if sessionName == "" {
		return nil // No session tracking — can't check state
	}

	// Find town root for heartbeat check
	townRoot, _, _ := workspace.FindFromCwdWithFallback()
	if townRoot == "" {
		townRoot = os.Getenv("GT_TOWN_ROOT")
	}
	if townRoot == "" {
		return nil // Can't find workspace — exit quietly
	}

	// Check heartbeat state: if already "exiting" or "idle", gt done already ran
	hb := polecat.ReadSessionHeartbeat(townRoot, sessionName)
	if hb != nil {
		state := hb.EffectiveState()
		if state == polecat.HeartbeatExiting || state == polecat.HeartbeatIdle {
			return nil // gt done already ran or polecat is idle — nothing to do
		}
	}

	// Check if the polecat is on a feature branch with commits
	rigName := os.Getenv("GT_RIG")
	if rigName == "" {
		return nil
	}

	// Reconstruct polecat worktree path
	polecatDir := filepath.Join(townRoot, rigName, "polecats", polecatName)
	// Try the nested clone layout first (polecats/<name>/<rig>/)
	cloneDir := filepath.Join(polecatDir, rigName)
	if _, err := os.Stat(filepath.Join(cloneDir, ".git")); err != nil {
		// Fall back to flat layout
		cloneDir = polecatDir
		if _, err := os.Stat(filepath.Join(cloneDir, ".git")); err != nil {
			return nil // No git repo found — exit quietly
		}
	}

	// Check current branch — skip if on main/master
	branchCmd := exec.Command("git", "-C", cloneDir, "rev-parse", "--abbrev-ref", "HEAD")
	branchOut, err := branchCmd.Output()
	if err != nil {
		return nil // Can't determine branch — exit quietly
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "main" || branch == "master" || branch == "HEAD" {
		return nil // On default branch — nothing to submit
	}

	// Decide whether the polecat has work to submit. This covers BOTH:
	//   - committed-but-unpushed work (commits ahead of origin/main), and
	//   - uncommitted work in the working tree (files written but never committed).
	// The second case is the stranding bug this fix targets: the agent wrote
	// implementation files, finished its turn (firing Stop), but never ran
	// `git commit && gt done` — so there are 0 commits ahead and the old check
	// bailed, leaving the work to rot into NEEDS_RECOVERY. gt done's gt-pvx
	// auto-commit safety net commits dirty work before submitting, so handing
	// the uncommitted case to gt done lands the work instead of stranding it.
	decision := polecatStopPendingWork(cloneDir)
	if !decision.HasWork {
		return nil // Nothing to submit — exit quietly
	}

	// Polecat has pending work! Run gt done as a safety net.
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "⚠️  Polecat %s has unsubmitted work on branch %s (%s)\n", polecatName, branch, decision.Reason)
	fmt.Fprintf(os.Stderr, "   Auto-running gt done as safety net...\n")
	fmt.Fprintf(os.Stderr, "\n")

	// Find gt binary path
	gtBin, err := os.Executable()
	if err != nil {
		gtBin = "gt"
	}

	// Run gt done in the polecat's worktree context
	doneCmd := exec.Command(gtBin, "done")
	doneCmd.Dir = cloneDir
	doneCmd.Stdout = os.Stdout
	doneCmd.Stderr = os.Stderr
	// Inherit environment (GT_POLECAT, GT_RIG, etc. are already set)
	doneCmd.Env = os.Environ()

	if err := doneCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Auto gt done failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Witness will handle cleanup.\n")
		// Don't return error — don't block session stop
		return nil
	}

	return nil
}

// polecatStopDecision is the pure result of inspecting a polecat worktree for
// unsubmitted work at session-Stop time.
type polecatStopDecision struct {
	HasWork bool
	Reason  string // human-readable description of what triggered the submit
}

// polecatStopPendingWork inspects a polecat's git worktree and reports whether
// it holds work that should be submitted via gt done.
//
// It returns HasWork=true when EITHER:
//   - there are commits ahead of origin/main (committed-but-unpushed work), OR
//   - the working tree is dirty (uncommitted work — files written but never
//     committed before the session's turn ended).
//
// The uncommitted-work case is the core of the submit-reliability fix: gt done
// auto-commits dirty work (gt-pvx) before submitting, so routing it through gt
// done lands the work instead of letting it strand into NEEDS_RECOVERY. This is
// safe to do on Stop because the Claude Code Stop hook fires only when the agent
// finishes its turn normally — it does NOT fire on context-limit (PreCompact),
// crash, or API error — so a Stop is a genuine completion signal, not a snapshot
// of an in-flight edit. Git errors fail closed (HasWork=false) so a transient
// git failure never blocks session stop.
func polecatStopPendingWork(cloneDir string) polecatStopDecision {
	// Committed-but-unpushed: commits ahead of origin/main.
	aheadOut, err := exec.Command("git", "-C", cloneDir, "rev-list", "--count", "origin/main..HEAD").Output()
	if err == nil {
		ahead := strings.TrimSpace(string(aheadOut))
		if ahead != "" && ahead != "0" {
			return polecatStopDecision{
				HasWork: true,
				Reason:  ahead + " unpushed commit(s)",
			}
		}
	}

	// Uncommitted work: dirty working tree. `git status --porcelain` prints one
	// line per changed/untracked path; any output means there is work that gt
	// done will auto-commit and submit. gt done itself filters out runtime/overlay
	// artifacts (CLAUDE.local.md, .runtime, etc.) before committing, so a strict
	// emptiness check here is intentionally conservative: if anything is dirty we
	// hand off to gt done, which makes the authoritative include/exclude decision.
	statusOut, err := exec.Command("git", "-C", cloneDir, "status", "--porcelain").Output()
	if err == nil && strings.TrimSpace(string(statusOut)) != "" {
		return polecatStopDecision{
			HasWork: true,
			Reason:  "uncommitted changes",
		}
	}

	return polecatStopDecision{HasWork: false}
}
