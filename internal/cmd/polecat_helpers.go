package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/style"
)

// polecatTarget represents a polecat to operate on.
type polecatTarget struct {
	rigName     string
	polecatName string
	mgr         *polecat.Manager
	r           *rig.Rig
}

// resolvePolecatTargets builds a list of polecats from command args.
// If useAll is true, the first arg is treated as a rig name and all polecats in it are returned.
// Otherwise, args are parsed as rig/polecat addresses.
func resolvePolecatTargets(args []string, useAll bool) ([]polecatTarget, error) {
	var targets []polecatTarget

	if useAll {
		// --all flag: first arg is just the rig name
		rigName := args[0]
		// Check if it looks like rig/polecat format
		if _, _, err := parseAddress(rigName); err == nil {
			return nil, fmt.Errorf("with --all, provide just the rig name (e.g., 'gt polecat <cmd> %s --all')", strings.Split(rigName, "/")[0])
		}

		mgr, r, err := getPolecatManager(rigName)
		if err != nil {
			return nil, err
		}

		polecats, err := mgr.List()
		if err != nil {
			return nil, fmt.Errorf("listing polecats: %w", err)
		}

		for _, p := range polecats {
			targets = append(targets, polecatTarget{
				rigName:     rigName,
				polecatName: p.Name,
				mgr:         mgr,
				r:           r,
			})
		}
	} else {
		// Multiple rig/polecat arguments - require explicit rig/polecat format
		for _, arg := range args {
			// Validate format: must contain "/" to avoid misinterpreting rig names as polecat names
			if !strings.Contains(arg, "/") {
				return nil, fmt.Errorf("invalid address '%s': must be in 'rig/polecat' format (e.g., 'gastown/Toast')", arg)
			}

			rigName, polecatName, err := parseAddress(arg)
			if err != nil {
				return nil, fmt.Errorf("invalid address '%s': %w", arg, err)
			}

			mgr, r, err := getPolecatManager(rigName)
			if err != nil {
				return nil, err
			}

			targets = append(targets, polecatTarget{
				rigName:     rigName,
				polecatName: polecatName,
				mgr:         mgr,
				r:           r,
			})
		}
	}

	return targets, nil
}

// SafetyCheckResult holds the result of safety checks for a polecat.
type SafetyCheckResult struct {
	Polecat           string
	Blocked           bool
	Reasons           []string
	CleanupStatus     polecat.CleanupStatus
	Disposition       polecat.Disposition
	DispositionReason string
	HookBead          string
	OpenMR            string
	GitState          *GitState
}

// checkPolecatSafety performs safety checks before destructive operations.
// Returns nil if the polecat is safe to operate on, or a SafetyCheckResult with reasons if blocked.
func checkPolecatSafety(target polecatTarget) *SafetyCheckResult {
	result := &SafetyCheckResult{
		Polecat: fmt.Sprintf("%s/%s", target.rigName, target.polecatName),
	}

	polecatInfo, infoErr := target.mgr.Get(target.polecatName)
	if infoErr != nil || polecatInfo == nil {
		result.Reasons = append(result.Reasons, "cannot read polecat state")
		result.Blocked = true
		return result
	}

	bd := beads.New(target.r.Path)
	agentBeadID := polecatBeadIDForRig(target.r, target.rigName, target.polecatName)
	agentIssue, fields, err := bd.GetAgentBead(agentBeadID)
	if err != nil {
		result.Reasons = append(result.Reasons, "cannot read agent metadata")
		result.Blocked = true
		return result
	}

	if fields != nil {
		result.CleanupStatus = polecat.CleanupStatus(fields.CleanupStatus)
	}
	result.HookBead = agentHookBead(agentIssue, fields)

	input := polecat.WorkstateInputFromAgentFields(
		polecatInfo.State,
		result.HookBead,
		fields,
		fields != nil && polecat.ActiveMRBlocksReuse(bd, fields.ActiveMR),
	)
	if fields == nil {
		input.CleanupStatus = polecat.CleanupClean
	}
	applyGitEvidenceToWorkstate(&input, polecatInfo.ClonePath)

	gitState, gitErr := getGitState(polecatInfo.ClonePath)
	result.GitState = gitState
	if polecatInfo.Branch != "" {
		input.HasSubmittableWork = hasSubmittableWorkForRecovery(polecatInfo.ClonePath, gitState, gitErr)
		mr, mrErr := bd.FindMRForBranch(polecatInfo.Branch)
		if mrErr != nil && input.HasSubmittableWork {
			input.MQStatusUnknown = true
		} else if mr != nil {
			result.OpenMR = mr.ID
			input.MQSubmitted = true
		}
	}

	resolved := polecat.ResolveWorkstateDisposition(input)
	result.Disposition = resolved.Disposition
	result.DispositionReason = resolved.Reason
	if !resolved.Disposition.SafeToNuke() {
		result.Reasons = append(result.Reasons, fmt.Sprintf("disposition %s (%s)", resolved.Disposition, resolved.Reason))
	}

	result.Blocked = len(result.Reasons) > 0
	return result
}

func rigPrefix(r *rig.Rig) string {
	townRoot := filepath.Dir(r.Path)
	return beads.GetPrefixForRig(townRoot, r.Name)
}

func polecatBeadIDForRig(r *rig.Rig, rigName, polecatName string) string {
	return beads.PolecatBeadIDWithPrefix(rigPrefix(r), rigName, polecatName)
}

// displaySafetyCheckBlocked prints blocked polecats and guidance.
func displaySafetyCheckBlocked(blocked []*SafetyCheckResult) {
	fmt.Printf("%s Cannot nuke the following polecats:\n\n", style.Error.Render("Error:"))
	var polecatList []string
	for _, b := range blocked {
		fmt.Printf("  %s:\n", style.Bold.Render(b.Polecat))
		for _, r := range b.Reasons {
			fmt.Printf("    - %s\n", r)
		}
		polecatList = append(polecatList, b.Polecat)
	}
	fmt.Println()
	fmt.Println("Safety checks failed. Resolve issues before nuking, or use --force.")
	fmt.Println("Options:")
	fmt.Printf("  1. Complete work: gt done (from polecat session)\n")
	fmt.Printf("  2. Push changes: git push (from polecat worktree)\n")
	fmt.Printf("  3. Escalate: gt mail send mayor/ -s \"RECOVERY_NEEDED\" -m \"...\"\n")
	fmt.Printf("  4. Force nuke (LOSES WORK): gt polecat nuke --force %s\n", strings.Join(polecatList, " "))
	fmt.Println()
}

// displayDryRunSafetyCheck shows safety check status for dry-run mode.
func displayDryRunSafetyCheck(target polecatTarget) {
	fmt.Printf("\n  Safety checks:\n")
	polecatInfo, infoErr := target.mgr.Get(target.polecatName)
	bd := beads.New(target.r.Path)
	agentBeadID := polecatBeadIDForRig(target.r, target.rigName, target.polecatName)
	agentIssue, fields, err := bd.GetAgentBead(agentBeadID)

	// Check 1: Git state
	if err != nil || fields == nil {
		if infoErr == nil && polecatInfo != nil {
			gitState, gitErr := getGitState(polecatInfo.ClonePath)
			if gitErr != nil {
				fmt.Printf("    - Git state: %s\n", style.Warning.Render("cannot check"))
			} else if gitState.Clean {
				fmt.Printf("    - Git state: %s\n", style.Success.Render("clean"))
			} else {
				fmt.Printf("    - Git state: %s\n", style.Error.Render("dirty"))
			}
		} else {
			fmt.Printf("    - Git state: %s\n", style.Dim.Render("unknown (no polecat info)"))
		}
		fmt.Printf("    - Hook: %s\n", style.Dim.Render("unknown (no agent bead)"))
	} else {
		cleanupStatus := polecat.CleanupStatus(fields.CleanupStatus)
		if cleanupStatus.IsSafe() {
			fmt.Printf("    - Git state: %s\n", style.Success.Render("clean"))
		} else if cleanupStatus.RequiresRecovery() {
			fmt.Printf("    - Git state: %s (%s)\n", style.Error.Render("dirty"), cleanupStatus)
		} else {
			fmt.Printf("    - Git state: %s\n", style.Warning.Render("unknown"))
		}

		hookBead := agentIssue.HookBead
		if hookBead == "" {
			hookBead = fields.HookBead
		}
		if hookBead != "" {
			fmt.Printf("    - Hook: %s (%s)\n", style.Warning.Render("set"), hookBead)
		} else {
			fmt.Printf("    - Hook: %s\n", style.Success.Render("empty"))
		}
	}

	result := checkPolecatSafety(target)
	if result.Disposition != "" {
		status := style.Success.Render(string(result.Disposition))
		if !result.Disposition.SafeToNuke() {
			status = style.Error.Render(string(result.Disposition))
		}
		fmt.Printf("    - Disposition: %s (%s)\n", status, result.DispositionReason)
	}

	// Check 2: Open MR
	if infoErr == nil && polecatInfo != nil && polecatInfo.Branch != "" {
		mr, mrErr := bd.FindMRForBranch(polecatInfo.Branch)
		if mrErr == nil && mr != nil {
			fmt.Printf("    - Open MR: %s (%s)\n", style.Error.Render("yes"), mr.ID)
		} else {
			fmt.Printf("    - Open MR: %s\n", style.Success.Render("none"))
		}
	} else {
		fmt.Printf("    - Open MR: %s\n", style.Dim.Render("unknown (no branch info)"))
	}
}
