// Package beads provides merge request and gate utilities.
package beads

import (
	"errors"
	"strings"
)

// FindMRForBranch searches for an open merge-request bead for the given branch.
// Returns the MR bead if found, nil if not found.
// This enables idempotent `gt done` - if an MR already exists, we skip creation.
func (b *Beads) FindMRForBranch(branch string) (*Issue, error) {
	return b.findMRForBranch(branch, true)
}

// FindMRForBranchAny searches for a merge-request bead for the given branch
// across all statuses (open and closed). Used by recovery checks to determine
// if work was ever submitted to the merge queue. See #1035.
func (b *Beads) FindMRForBranchAny(branch string) (*Issue, error) {
	return b.findMRForBranch(branch, false)
}

// FindMRForBranchAndSHA searches for an open merge-request bead matching both
// the branch name AND the commit SHA. This is the correct dedup key: two MRs
// from the same branch but with different commit SHAs are distinct submissions
// (e.g., polecat fixed a gate failure and re-pushed). See GH#3032.
//
// Returns nil if no MR matches both branch and SHA. Callers should create a
// new MR in that case and supersede old MRs for the same source issue.
func (b *Beads) FindMRForBranchAndSHA(branch, commitSHA string) (*Issue, error) {
	issues, err := b.ListMergeRequests(ListOptions{
		Status: "all",
		Label:  "gt:merge-request",
	})
	if err != nil {
		return nil, err
	}

	branchPrefix := "branch: " + branch + "\n"
	for _, issue := range issues {
		if issue.Status == "closed" {
			continue
		}
		if !strings.HasPrefix(issue.Description, branchPrefix) {
			continue
		}
		// Branch matches — check commit SHA.
		// If the MR has no commit_sha field (legacy), fall back to branch-only
		// match for backward compatibility.
		fields := ParseMRFields(issue)
		if fields != nil && fields.CommitSHA != "" && commitSHA != "" {
			if fields.CommitSHA != commitSHA {
				// Same branch but different SHA — this is a stale MR.
				// Don't return it; caller will create a new MR and supersede.
				continue
			}
		}
		return issue, nil
	}

	return nil, nil
}

// findMRForBranch searches the wisps table (Dolt) for a merge-request
// bead matching the given branch.
// Uses status=all which includes all issue statuses with full descriptions.
// Ephemeral=true routes to the wisps table where MR beads live (GH#2446).
// When skipClosed is true, closed beads are excluded (for open-MR checks).
func (b *Beads) findMRForBranch(branch string, skipClosed bool) (*Issue, error) {
	branchPrefix := "branch: " + branch + "\n"

	issues, err := b.ListMergeRequests(ListOptions{
		Status: "all",
		Label:  "gt:merge-request",
	})
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if skipClosed && issue.Status == "closed" {
			continue
		}
		if strings.HasPrefix(issue.Description, branchPrefix) {
			return issue, nil
		}
	}

	return nil, nil
}

// FindOpenMRsForIssue returns all open merge-request beads whose source_issue
// matches the given issue ID. Used to find prior attempts when re-dispatching
// an issue and to supersede old MRs when a new one is created.
func (b *Beads) FindOpenMRsForIssue(issueID string) ([]*Issue, error) {
	issues, err := b.ListMergeRequests(ListOptions{
		Status: "open",
		Label:  "gt:merge-request",
	})
	if err != nil {
		return nil, err
	}

	var matches []*Issue
	for _, issue := range issues {
		if MatchesMRSourceIssue(issue.Description, issueID) {
			matches = append(matches, issue)
		}
	}
	return matches, nil
}

// FindPendingMRForAgentOrBranch returns a non-terminal MR that belongs to the
// given agent bead or branch. This is the fallback when active_mr is empty or
// stale but the merge request still exists in the queue.
func (b *Beads) FindPendingMRForAgentOrBranch(agentBeadID, branch string) (*Issue, error) {
	if agentBeadID == "" && branch == "" {
		return nil, nil
	}
	issues, err := b.ListMergeRequests(ListOptions{
		Status: "all",
		Label:  "gt:merge-request",
	})
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if IssueStatus(issue.Status).IsTerminal() {
			continue
		}
		if MRMatchesAgentOrBranch(issue, agentBeadID, branch) {
			return issue, nil
		}
	}
	return nil, nil
}

type pendingMRLookup interface {
	Show(issueID string) (*Issue, error)
	FindPendingMRForAgentOrBranch(agentBeadID, branch string) (*Issue, error)
}

// PendingMRForActiveOrOwnership reconciles active_mr with MR ownership evidence.
// A non-terminal active_mr wins; terminal or missing active_mr falls through to
// agent/branch ownership lookup so stale metadata does not block forever.
func PendingMRForActiveOrOwnership(lookup pendingMRLookup, activeMR, agentBeadID, branch string) (*Issue, error) {
	if lookup == nil {
		return nil, nil
	}
	if activeMR != "" {
		mr, err := lookup.Show(activeMR)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if mr != nil && !IssueStatus(mr.Status).IsTerminal() {
			return mr, nil
		}
	}
	return lookup.FindPendingMRForAgentOrBranch(agentBeadID, branch)
}

// MRMatchesAgentOrBranch reports whether an MR belongs to an agent bead or
// source branch. Branch falls back to the legacy description prefix format.
func MRMatchesAgentOrBranch(issue *Issue, agentBeadID, branch string) bool {
	if issue == nil {
		return false
	}
	fields := ParseMRFields(issue)
	if agentBeadID != "" && fields != nil && fields.AgentBead == agentBeadID {
		return true
	}
	if branch == "" {
		return false
	}
	if fields != nil && fields.Branch == branch {
		return true
	}
	return strings.HasPrefix(issue.Description, "branch: "+branch+"\n")
}

// MatchesMRSourceIssue returns true if the MR description contains a
// source_issue field matching the given issue ID exactly. The trailing
// newline in the needle prevents partial ID matches (e.g., "gt-abc"
// must not match "gt-abcdef").
func MatchesMRSourceIssue(description, issueID string) bool {
	needle := "source_issue: " + issueID + "\n"
	return strings.Contains(description, needle)
}
