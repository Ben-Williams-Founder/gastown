package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/mrtarget"
)

func resolveCommandMRTarget(townRoot, rigName, defaultBranch, issueID, explicitTarget string, sourceIssue *beads.Issue, bd *beads.Beads, g *git.Git) (mrtarget.Result, error) {
	refineryEnabled := true
	settingsPath := filepath.Join(townRoot, rigName, "settings", "config.json")
	if settings, err := config.LoadRigSettings(settingsPath); err == nil && settings.MergeQueue != nil {
		refineryEnabled = settings.MergeQueue.IsRefineryIntegrationEnabled()
	}

	var resolveIntegration func(string) (string, error)
	if refineryEnabled {
		resolveIntegration = func(id string) (string, error) {
			autoTarget, err := beads.DetectIntegrationBranch(bd, g, id)
			if err != nil {
				return "", fmt.Errorf("auto-detecting integration target: %w", err)
			}
			return autoTarget, nil
		}
	}

	return mrtarget.Resolve(mrtarget.Options{
		DefaultBranch:  defaultBranch,
		ExplicitTarget: explicitTarget,
		SourceIssue:    sourceIssue,
		IssueID:        issueID,
		ResolveIntegration: resolveIntegration,
		Checker:        g,
		CheckRemote:    true,
	})
}

func printResolvedMRTarget(result mrtarget.Result) {
	switch result.Source {
	case mrtarget.SourceExplicit:
		fmt.Printf("  Target branch: %s (from explicit target)\n", result.Branch)
	case mrtarget.SourceFormula:
		fmt.Printf("  Target branch: %s (from formula_vars)\n", result.Branch)
	case mrtarget.SourceAutoEpic:
		fmt.Printf("  Target branch: %s (from integration branch auto-detect)\n", result.Branch)
	}
}
