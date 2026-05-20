package refinery

import (
	"errors"
	"fmt"

	"github.com/steveyegge/gastown/internal/beads"
)

func closeAttachedMoleculeForSource(b *beads.Beads, sourceIssueID string) (string, int, error) {
	if sourceIssueID == "" {
		return "", 0, nil
	}

	sourceIssue, err := b.Show(sourceIssueID)
	if err != nil {
		return "", 0, fmt.Errorf("loading source issue %s: %w", sourceIssueID, err)
	}

	attachment := beads.ParseAttachmentFields(sourceIssue)
	if attachment == nil || attachment.AttachedMolecule == "" {
		return "", 0, nil
	}

	closed, err := forceCloseMoleculeTree(b, attachment.AttachedMolecule)
	if err != nil {
		return attachment.AttachedMolecule, closed, err
	}
	return attachment.AttachedMolecule, closed, nil
}

func forceCloseMoleculeTree(b *beads.Beads, moleculeID string) (int, error) {
	closed, err := forceCloseMoleculeDescendants(b, moleculeID)
	if err != nil {
		return closed, err
	}

	if err := b.ForceCloseWithReason("merged: close attached molecule", moleculeID); err != nil {
		return closed, fmt.Errorf("closing attached molecule %s: %w", moleculeID, err)
	}
	return closed + 1, nil
}

func forceCloseMoleculeDescendants(b *beads.Beads, parentID string) (int, error) {
	children, err := b.List(beads.ListOptions{
		Parent: parentID,
		Status: "all",
	})
	if err != nil {
		return 0, fmt.Errorf("listing children of %s: %w", parentID, err)
	}

	totalClosed := 0
	var errs []error
	for _, child := range children {
		closed, childErr := forceCloseMoleculeDescendants(b, child.ID)
		totalClosed += closed
		if childErr != nil {
			errs = append(errs, childErr)
		}
	}

	var idsToClose []string
	for _, child := range children {
		if child.Status != "closed" {
			idsToClose = append(idsToClose, child.ID)
		}
	}

	if len(idsToClose) > 0 {
		if err := b.ForceCloseWithReason("merged: close attached molecule step", idsToClose...); err != nil {
			errs = append(errs, fmt.Errorf("closing children of %s: %w", parentID, err))
		} else {
			totalClosed += len(idsToClose)
		}
	}

	return totalClosed, errors.Join(errs...)
}
