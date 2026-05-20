package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
)

var tapGuardPRWorkflowOperation string

var tapGuardCmd = &cobra.Command{
	Use:   "guard",
	Short: "Block forbidden operations (PreToolUse hook)",
	Long: `Block forbidden operations via Claude Code PreToolUse hooks.

Guard commands exit with code 2 to BLOCK tool execution when a policy
is violated. They're called before the tool runs, preventing the
forbidden operation entirely.

Available guards:
  pr-workflow        - Block PR creation/branching when disallowed by workflow policy
  bd-init            - Block bd init in wrong directories
  mol-patrol         - Block mol patrol from agent contexts
  dangerous-command  - Block rm -rf, force push, hard reset, git clean

External guards (standalone scripts, not compiled into gt):
  context-budget   - scripts/guards/context-budget-guard.sh

Example hook configuration:
  {
    "PreToolUse": [{
      "matcher": "Bash(gh pr create*)",
      "hooks": [{"command": "gt tap guard pr-workflow --operation pr-create"}]
    }]
  }`,
}

var tapGuardPRWorkflowCmd = &cobra.Command{
	Use:   "pr-workflow",
	Short: "Block PR operations that conflict with repo workflow policy",
	Long: `Block PR workflow operations in Gas Town.

Gas Town supports multiple landing policies: crew may direct-push in maintainer
repos, polecats submit to the Refinery merge queue, and some rigs use native
GitHub PRs when merge_queue.merge_strategy is "pr".

This guard blocks:
  - gh pr create, unless the rig is configured for PR merge strategy
  - git checkout -b / git switch -c, except for polecat merge-queue branches

Exit codes:
  0 - Operation allowed by current workflow policy
  2 - Operation blocked by current workflow policy

Humans running outside Gas Town with a fork origin can still use PRs.`,
	RunE: runTapGuardPRWorkflow,
}

func init() {
	tapGuardPRWorkflowCmd.Flags().StringVar(&tapGuardPRWorkflowOperation, "operation", "", "operation being guarded: pr-create or branch-create")
	tapCmd.AddCommand(tapGuardCmd)
	tapGuardCmd.AddCommand(tapGuardPRWorkflowCmd)
}

func runTapGuardPRWorkflow(cmd *cobra.Command, args []string) error {
	operation := normalizePRWorkflowOperation(tapGuardPRWorkflowOperation)
	if shouldAllowPRWorkflowOperation(operation) {
		return nil
	}

	// Check if we're in a Gas Town agent context
	if isGasTownAgentContext() {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "╔══════════════════════════════════════════════════════════════════╗")
		fmt.Fprintln(os.Stderr, "║  ❌ PR WORKFLOW BLOCKED                                          ║")
		fmt.Fprintln(os.Stderr, "╠══════════════════════════════════════════════════════════════════╣")
		fmt.Fprintln(os.Stderr, "║  This operation conflicts with the current Gas Town workflow.   ║")
		fmt.Fprintln(os.Stderr, "║                                                                  ║")
		fmt.Fprintln(os.Stderr, "║  Polecats: use gt done to submit to the Refinery merge queue.   ║")
		fmt.Fprintln(os.Stderr, "║  Crew: use the repo's direct-push workflow when authorized.     ║")
		fmt.Fprintln(os.Stderr, "║  External contributors: open PRs from a fork, not this repo.    ║")
		fmt.Fprintln(os.Stderr, "║                                                                  ║")
		fmt.Fprintln(os.Stderr, "║  If this rig requires PRs, set merge_queue.merge_strategy=pr.   ║")
		fmt.Fprintln(os.Stderr, "╚══════════════════════════════════════════════════════════════════╝")
		fmt.Fprintln(os.Stderr, "")
		return NewSilentExit(2) // Exit 2 = BLOCK in Claude Code hooks
	}

	// Check if origin is a maintainer repo.
	if isMaintainerOrigin() {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "╔══════════════════════════════════════════════════════════════════╗")
		fmt.Fprintln(os.Stderr, "║  ❌ PR BLOCKED - MAINTAINER ORIGIN                               ║")
		fmt.Fprintln(os.Stderr, "╠══════════════════════════════════════════════════════════════════╣")
		fmt.Fprintln(os.Stderr, "║  Same-repository PRs are blocked for this maintainer repo.      ║")
		fmt.Fprintln(os.Stderr, "║  Use the repo's configured landing path instead.                ║")
		fmt.Fprintln(os.Stderr, "║                                                                  ║")
		fmt.Fprintln(os.Stderr, "║  Polecats: gt done. Crew: direct push only when authorized.     ║")
		fmt.Fprintln(os.Stderr, "╚══════════════════════════════════════════════════════════════════╝")
		fmt.Fprintln(os.Stderr, "")
		return NewSilentExit(2) // Exit 2 = BLOCK in Claude Code hooks
	}

	// Not in Gas Town context and not maintainer origin - allow PRs
	return nil
}

const (
	prWorkflowOperationPRCreate     = "pr-create"
	prWorkflowOperationBranchCreate = "branch-create"
)

func normalizePRWorkflowOperation(operation string) string {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "pr", "pr-create", "gh-pr-create":
		return prWorkflowOperationPRCreate
	case "branch", "branch-create", "feature-branch":
		return prWorkflowOperationBranchCreate
	default:
		return ""
	}
}

func shouldAllowPRWorkflowOperation(operation string) bool {
	if currentRigUsesPRMergeStrategy() {
		return true
	}
	return operation == prWorkflowOperationBranchCreate && isPolecatContext()
}

func currentRigUsesPRMergeStrategy() bool {
	if strategy := os.Getenv("GT_MERGE_STRATEGY"); strings.EqualFold(strings.TrimSpace(strategy), "pr") {
		return true
	}

	townRoot, err := findTownRoot()
	if err != nil {
		return false
	}

	rigName := os.Getenv("GT_RIG")
	if rigName == "" {
		rigName, _ = inferRigFromCwd(townRoot)
	}
	if rigName == "" {
		return false
	}

	settings, err := config.LoadRigSettings(config.RigSettingsPath(filepath.Join(townRoot, rigName)))
	if err != nil || settings == nil || settings.MergeQueue == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(settings.MergeQueue.MergeStrategy), "pr")
}

// isGasTownAgentContext returns true if we're running as a Gas Town managed agent.
func isGasTownAgentContext() bool {
	// Check environment variables set by Gas Town session management
	envVars := []string{
		"GT_POLECAT",
		"GT_CREW",
		"GT_WITNESS",
		"GT_REFINERY",
		"GT_MAYOR",
		"GT_DEACON",
	}
	for _, env := range envVars {
		if os.Getenv(env) != "" {
			return true
		}
	}

	// Also check if we're in a crew or polecat worktree by path
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}

	agentPaths := []string{"/crew/", "/polecats/"}
	for _, path := range agentPaths {
		if strings.Contains(cwd, path) {
			return true
		}
	}

	return false
}

func isPolecatContext() bool {
	if os.Getenv("GT_POLECAT") != "" || strings.EqualFold(os.Getenv("GT_ROLE"), "polecat") {
		return true
	}
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	return strings.Contains(cwd, "/polecats/")
}

// isMaintainerOrigin returns true if the origin remote points to the maintainer's repo.
// This prevents the maintainer from accidentally creating PRs in their own repo.
func isMaintainerOrigin() bool {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	url := strings.TrimSpace(string(output))
	canonicalRepos := []string{
		"gastownhall/gastown",
		"steveyegge/gastown",
	}
	for _, repo := range canonicalRepos {
		if strings.Contains(url, repo) {
			return true
		}
	}
	return false
}
