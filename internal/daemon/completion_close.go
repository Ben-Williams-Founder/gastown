package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/util"
)

// completionCloseCycle is the daemon pass that drives WS2 Rung-3. For every
// known polecat it asks maybeCompletionClose to close the polecat's
// mol-polecat-work chain iff its active_mr is CONFIRMED merged.
//
// The whole pass is inert unless $GT_COMPLETION_CLOSE is set: we check the flag
// once up front and return immediately when off, so a disabled daemon does zero
// extra per-rig/per-polecat work (default behaviour is byte-identical).
//
// It is event-driven on a known fact rather than a push event — running once per
// heartbeat is still deterministic and far tighter than the external ~15min
// sweeper. It scans ALL polecats (not just dead-session ones) so the cleared-
// hook completion case (gt done clears hook_bead) is covered too.
func (d *Daemon) completionCloseCycle() {
	if strings.TrimSpace(polecat.CompletionCloseEnvValue()) == "" {
		return // feature off — no work
	}
	d.rigPool.runPerRig(d.ctx, d.getKnownRigs(), func(_ context.Context, rigName string) error {
		// Belt-and-suspenders: the per-polecat path already recovers, but guard
		// the per-rig goroutine too so no panic from this feature can ever escape
		// into the daemon heartbeat (fail-open — a panic just skips this rig).
		defer func() {
			if r := recover(); r != nil {
				d.logger.Printf("completion-close: recovered rig-level panic for %s (fail-open): %v", rigName, r)
			}
		}()
		d.completionCloseRig(rigName)
		return nil
	})
}

// completionCloseRig evaluates every polecat in a rig.
func (d *Daemon) completionCloseRig(rigName string) {
	polecatsDir := filepath.Join(d.config.TownRoot, rigName, "polecats")
	polecats, err := listPolecatWorktrees(polecatsDir)
	if err != nil {
		return // no polecats directory for this rig
	}
	for _, polecatName := range polecats {
		prefix := beads.GetPrefixForRig(d.config.TownRoot, rigName)
		agentBeadID := beads.PolecatBeadIDWithPrefix(prefix, rigName, polecatName)
		info, err := d.getAgentBeadInfo(agentBeadID)
		if err != nil {
			continue // not registered / lookup failed — skip (fail-open)
		}
		d.maybeCompletionClose(rigName, polecatName, info)
	}
}

// maybeCompletionClose deterministically closes a polecat's own
// mol-polecat-work step-wisp chain when that polecat's active_mr is CONFIRMED
// merged (WS2 Rung-3). It is a thin, fail-open wrapper that wires real bd CLI
// access into the pure polecat.EvaluateCompletionClose decision core.
//
// Gated by $GT_COMPLETION_CLOSE (default OFF — see polecat.CompletionCloseEnv):
// when unset this returns immediately having done nothing, so daemon behaviour
// is byte-identical to today. The mayor flips it on deliberately later, then the
// external ~15min sweeper timer can be retired.
//
// SAFETY: this runs in the live town daemon. It must never panic, never return
// a fatal error, and never close an in-flight polecat's steps. Every error path
// leaves wisps OPEN (the external sweeper remains the safety net). Closing is
// idempotent (the bd-close walk skips already-closed issues).
//
// Called from checkPolecatHealth only after the daemon has established the
// polecat's session is dead and (per agent_state/hook guards) it is not a crash
// — i.e. a completed polecat whose merge may or may not have landed yet.
func (d *Daemon) maybeCompletionClose(rigName, polecatName string, info *AgentBeadInfo) {
	// Cheapest possible gate first: if the feature is off, do nothing at all.
	env := polecat.CompletionCloseEnvValue()
	if strings.TrimSpace(env) == "" {
		return
	}

	// Guard against any panic in the bd/lookup path — the daemon must never die
	// because of this feature. A recovered panic is logged and treated as a
	// fail-open no-op (wisps left open).
	defer func() {
		if r := recover(); r != nil {
			d.logger.Printf("completion-close: recovered panic for %s/%s (fail-open, wisps left open): %v",
				rigName, polecatName, r)
		}
	}()

	if info == nil {
		return
	}

	// Resolve the polecat's source/work issue. After gt done the hook_bead is
	// cleared, so fall back to last_source_issue (read from the agent bead
	// description). AssessActiveMR also needs this to confirm the MR is terminal.
	sourceIssue := strings.TrimSpace(info.HookBead)
	activeMR := ""
	if af := d.agentFieldsFor(rigName, polecatName); af != nil {
		if sourceIssue == "" {
			sourceIssue = strings.TrimSpace(af.HookBead)
		}
		if sourceIssue == "" {
			sourceIssue = strings.TrimSpace(af.LastSourceIssue)
		}
		activeMR = strings.TrimSpace(af.ActiveMR)
	}
	if activeMR == "" {
		// No active_mr → nothing to confirm. Never close speculatively.
		return
	}

	// Route bd to the rig's beads DB (where the polecat's wisps + MR live).
	rigDir := beads.GetRigDirForName(d.config.TownRoot, rigName)
	if rigDir == "" {
		rigDir = d.config.TownRoot
	}
	rigBeadsDir := beads.ResolveBeadsDir(rigDir)

	// IssueReader for AssessActiveMR — *beads.Beads satisfies polecat.IssueReader.
	reader := beads.New(rigBeadsDir)

	// git-safe: only treat the MR as fully landed when the polecat worktree is
	// clean (no uncommitted/stash/unpushed). On any check failure, GitSafe stays
	// false → RequireGitSafe keeps the MR pending → we leave the chain open.
	gitSafe := d.polecatGitSafe(rigName, polecatName)

	result := polecat.EvaluateCompletionClose(
		polecat.CompletionCloseInput{
			Env:             env,
			GOOS:            polecat.HostGOOS(),
			PolecatName:     polecatName,
			ActiveMR:        activeMR,
			SourceIssueHint: sourceIssue,
			GitSafe:         gitSafe,
		},
		reader,
		func(src string) (string, error) { return d.resolveAttachedMolecule(rigBeadsDir, src) },
		func(moleculeID string) (int, error) { return d.closeMoleculeChain(rigBeadsDir, moleculeID) },
	)

	// Only log when we actually did (or tried to do) something — a "MR not
	// merged yet" verdict is the common case and would be log spam every cycle.
	if result.Attempted || result.Closed > 0 {
		d.logger.Printf("completion-close: %s/%s: %s", rigName, polecatName, result.Reason)
	}
}

// agentFieldsFor reads the polecat's agent bead and parses its description
// fields (active_mr, last_source_issue, hook_bead). Returns nil on any error
// (caller treats nil as "no extra info" and bails out fail-open).
func (d *Daemon) agentFieldsFor(rigName, polecatName string) *beads.AgentFields {
	prefix := beads.GetPrefixForRig(d.config.TownRoot, rigName)
	agentBeadID := beads.PolecatBeadIDWithPrefix(prefix, rigName, polecatName)

	cmd := exec.Command(d.bdPath, "show", agentBeadID, "--json") //nolint:gosec // G204: args constructed internally
	cmd.Dir = d.config.TownRoot
	cmd.Env = bdReadOnlyPinnedEnv(filepath.Join(d.config.TownRoot, ".beads"))
	util.SetDetachedProcessGroup(cmd)

	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	var issues []struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(output, &issues); err != nil || len(issues) == 0 {
		return nil
	}
	return beads.ParseAgentFields(issues[0].Description)
}

// resolveAttachedMolecule reads a source/work bead's attached_molecule field
// (the mol-polecat-work root) via bd CLI. Returns "" when there is no attached
// molecule. Routes to rigBeadsDir (where the work bead + wisps live).
func (d *Daemon) resolveAttachedMolecule(rigBeadsDir, sourceIssue string) (string, error) {
	sourceIssue = strings.TrimSpace(sourceIssue)
	if sourceIssue == "" {
		return "", nil
	}
	cmd := exec.Command(d.bdPath, "show", sourceIssue, "--json") //nolint:gosec // G204: args constructed internally
	cmd.Dir = d.config.TownRoot
	cmd.Env = beads.BuildReadOnlyPinnedBDEnv(os.Environ(), rigBeadsDir)
	util.SetDetachedProcessGroup(cmd)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("bd show %s: %w", sourceIssue, err)
	}
	var issues []struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(output, &issues); err != nil {
		return "", fmt.Errorf("parsing bd show %s: %w", sourceIssue, err)
	}
	if len(issues) == 0 {
		return "", nil
	}
	fields := beads.ParseAttachmentFields(&beads.Issue{Description: issues[0].Description})
	if fields == nil {
		return "", nil
	}
	return fields.AttachedMolecule, nil
}

// closeMoleculeChain closes a mol-polecat-work molecule root and all of its
// step descendants via the deterministic bd-close walk (the same
// `bd list --parent` / `bd close` pattern the witness orphan-close uses). It is
// idempotent: already-closed issues are skipped (not re-closed). Returns the
// number of issues actually closed.
func (d *Daemon) closeMoleculeChain(rigBeadsDir, moleculeID string) (int, error) {
	moleculeID = strings.TrimSpace(moleculeID)
	if moleculeID == "" {
		return 0, nil
	}
	reason := "WS2 Rung-3: mol-polecat-work complete — active_mr confirmed merged"

	// Bottom-up: close step descendants first, then the molecule root, so the
	// root is never "blocked by open issues".
	closed, descErr := d.closeDescendants(rigBeadsDir, moleculeID, reason)

	status, found := d.beadStatus(rigBeadsDir, moleculeID)
	if found && status != "closed" {
		if err := d.runBDClose(rigBeadsDir, []string{moleculeID}, reason); err != nil {
			if descErr != nil {
				return closed, fmt.Errorf("closing molecule %s: %w; also: %v", moleculeID, err, descErr)
			}
			return closed, fmt.Errorf("closing molecule %s: %w", moleculeID, err)
		}
		closed++
	}
	return closed, descErr
}

// closeDescendants recursively closes the open descendants of parentID.
// Mirrors witness.closeDescendantsViaCLI: lists children, recurses into
// grandchildren first, then closes the open direct children in one batch.
func (d *Daemon) closeDescendants(rigBeadsDir, parentID, reason string) (int, error) {
	cmd := exec.Command(d.bdPath, beads.InjectFlatForListJSON([]string{"list", "--parent=" + parentID, "--json"})...) //nolint:gosec // G204: args constructed internally
	cmd.Dir = d.config.TownRoot
	cmd.Env = beads.BuildReadOnlyPinnedBDEnv(os.Environ(), rigBeadsDir)
	util.SetDetachedProcessGroup(cmd)

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("listing children of %s: %w", parentID, err)
	}
	if len(strings.TrimSpace(string(output))) == 0 {
		return 0, nil
	}
	var children []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(output, &children); err != nil {
		return 0, fmt.Errorf("parsing children of %s: %w", parentID, err)
	}
	if len(children) == 0 {
		return 0, nil
	}

	total := 0
	var firstErr error
	for _, child := range children {
		n, err := d.closeDescendants(rigBeadsDir, child.ID, reason)
		total += n
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	var idsToClose []string
	for _, child := range children {
		if child.Status != "closed" {
			idsToClose = append(idsToClose, child.ID)
		}
	}
	if len(idsToClose) > 0 {
		if err := d.runBDClose(rigBeadsDir, idsToClose, reason); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("closing children of %s: %w", parentID, err)
			}
		} else {
			total += len(idsToClose)
		}
	}
	return total, firstErr
}

// beadStatus returns a bead's status and whether it was found.
func (d *Daemon) beadStatus(rigBeadsDir, beadID string) (string, bool) {
	cmd := exec.Command(d.bdPath, "show", beadID, "--json") //nolint:gosec // G204: args constructed internally
	cmd.Dir = d.config.TownRoot
	cmd.Env = beads.BuildReadOnlyPinnedBDEnv(os.Environ(), rigBeadsDir)
	util.SetDetachedProcessGroup(cmd)

	output, err := cmd.Output()
	if err != nil {
		return "", false
	}
	var issues []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(output, &issues); err != nil || len(issues) == 0 {
		return "", false
	}
	return issues[0].Status, true
}

// runBDClose closes one or more beads with a reason, using the mutation env so
// the close is committed to the rig DB (not stranded as a read-only no-op).
func (d *Daemon) runBDClose(rigBeadsDir string, ids []string, reason string) error {
	if len(ids) == 0 {
		return nil
	}
	args := append([]string{"close"}, ids...)
	args = append(args, "-r", reason)
	cmd := exec.Command(d.bdPath, args...) //nolint:gosec // G204: args constructed internally
	cmd.Dir = d.config.TownRoot
	cmd.Env = beads.BuildMutationPinnedBDEnv(os.Environ(), rigBeadsDir)
	util.SetDetachedProcessGroup(cmd)
	return cmd.Run()
}

// polecatGitSafe reports whether the polecat's worktree is clean enough to treat
// its merged MR as fully landed (no uncommitted work, stash, or unpushed
// commits). On ANY error it returns false (fail-closed for git-safety →
// RequireGitSafe keeps the MR pending → chain left open).
func (d *Daemon) polecatGitSafe(rigName, polecatName string) bool {
	clonePath := filepath.Join(d.config.TownRoot, rigName, "polecats", polecatName, rigName)
	// git status --porcelain: empty (ignoring runtime noise handled upstream)
	// is the conservative "clean" signal. Any output or error → not safe.
	cmd := exec.Command("git", "status", "--porcelain") //nolint:gosec // fixed args
	cmd.Dir = clonePath
	util.SetDetachedProcessGroup(cmd)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	if strings.TrimSpace(string(output)) != "" {
		return false
	}
	// Unpushed commits: HEAD must not be ahead of its upstream.
	ahead := exec.Command("git", "rev-list", "--count", "@{u}..HEAD") //nolint:gosec // fixed args
	ahead.Dir = clonePath
	util.SetDetachedProcessGroup(ahead)
	out, err := ahead.Output()
	if err != nil {
		// No upstream / detached → can't prove safe. Fail-closed.
		return false
	}
	return strings.TrimSpace(string(out)) == "0"
}
