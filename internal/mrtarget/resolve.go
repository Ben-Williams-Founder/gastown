package mrtarget

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
)

// BranchChecker verifies that target branches exist without depending on a
// concrete git implementation.
type BranchChecker interface {
	BranchExists(name string) (bool, error)
	RemoteBranchExists(remote, name string) (bool, error)
}

// Source describes where the resolved target came from.
type Source string

const (
	SourceDefault   Source = "default"
	SourceExplicit  Source = "explicit"
	SourceFormula   Source = "formula_vars"
	SourceAutoEpic  Source = "auto_epic"
	SourceMRReadback Source = "mr_readback"
)

// Result is the validated target branch and its provenance.
type Result struct {
	Branch string
	Source Source
}

// Options controls target resolution for MR producers.
type Options struct {
	DefaultBranch string
	ExplicitTarget string
	FormulaVars    string
	SourceIssue    *beads.Issue
	IssueID        string

	ResolveIntegration func(issueID string) (string, error)
	Checker            BranchChecker

	CheckRemote           bool
	AllowMissingOrigin    bool
	AllowStaleRCA         bool
	AllowExplicitDefault  bool
}

// Resolve chooses and validates one MR target branch. Precedence is:
// explicit target, formula_vars base_branch, integration auto-detection,
// default branch.
func Resolve(opts Options) (Result, error) {
	defaultBranch := strings.TrimSpace(opts.DefaultBranch)
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	target := strings.TrimSpace(opts.ExplicitTarget)
	source := SourceExplicit
	if target == "" {
		formulaVars := opts.FormulaVars
		if formulaVars == "" && opts.SourceIssue != nil {
			formulaVars = FormulaVarsFromIssue(opts.SourceIssue)
		}
		baseBranch, err := UniqueFormulaBaseBranch(formulaVars)
		if err != nil {
			return Result{}, err
		}
		if baseBranch != "" {
			target = baseBranch
			source = SourceFormula
		}
	}

	if target == "" && opts.ResolveIntegration != nil && opts.IssueID != "" {
		autoTarget, err := opts.ResolveIntegration(opts.IssueID)
		if err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(autoTarget) != "" {
			target = strings.TrimSpace(autoTarget)
			source = SourceAutoEpic
		}
	}

	if target == "" {
		target = defaultBranch
		source = SourceDefault
	}

	return Validate(ValidateOptions{
		Branch:               target,
		DefaultBranch:        defaultBranch,
		Source:               source,
		Checker:              opts.Checker,
		CheckRemote:          opts.CheckRemote,
		AllowMissingOrigin:   opts.AllowMissingOrigin,
		AllowStaleRCA:        opts.AllowStaleRCA,
		AllowExplicitDefault: opts.AllowExplicitDefault,
	})
}

// FormulaVarsFromIssue returns formula vars stored either in the structured
// formula_vars field or as continuation key=value lines in older bead records.
func FormulaVarsFromIssue(issue *beads.Issue) string {
	if issue == nil || issue.Description == "" {
		return ""
	}
	var vars []string
	if af := beads.ParseAttachmentFields(issue); af != nil && af.FormulaVars != "" {
		vars = append(vars, af.FormulaVars)
	}
	for _, line := range strings.Split(issue.Description, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, ":") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		vars = append(vars, line)
	}
	return strings.Join(vars, "\n")
}

// ValidateOptions controls branch validation.
type ValidateOptions struct {
	Branch        string
	DefaultBranch string
	Source        Source
	Checker       BranchChecker

	CheckRemote          bool
	AllowMissingOrigin   bool
	AllowStaleRCA        bool
	AllowExplicitDefault bool
}

// Validate checks that a resolved target is safe to write to an MR bead or use
// as a refinery merge destination.
func Validate(opts ValidateOptions) (Result, error) {
	defaultBranch := strings.TrimSpace(opts.DefaultBranch)
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		return Result{}, fmt.Errorf("MR target is empty")
	}
	if branch != opts.Branch {
		return Result{}, fmt.Errorf("MR target %q has surrounding whitespace", opts.Branch)
	}
	if err := validateBranchName(branch); err != nil {
		return Result{}, err
	}
	if strings.HasPrefix(branch, "integration/rca-cleanup") && !opts.AllowStaleRCA {
		return Result{}, fmt.Errorf("MR target %q is a stale RCA cleanup integration branch", branch)
	}
	if branch == "main" && defaultBranch != "main" {
		return Result{}, fmt.Errorf("MR target %q is unexpected for rig default branch %q", branch, defaultBranch)
	}
	if !opts.AllowExplicitDefault && opts.Source != SourceDefault && branch == defaultBranch {
		return Result{}, fmt.Errorf("MR target %q duplicates the default branch via %s", branch, opts.Source)
	}
	if err := validateBranchPrefix(branch, defaultBranch); err != nil {
		return Result{}, err
	}
	if opts.CheckRemote && opts.Checker != nil {
		exists, err := opts.Checker.RemoteBranchExists("origin", branch)
		if err != nil {
			return Result{}, fmt.Errorf("checking origin/%s: %w", branch, err)
		}
		if !exists && !opts.AllowMissingOrigin {
			return Result{}, fmt.Errorf("MR target origin/%s does not exist", branch)
		}
	}
	return Result{Branch: branch, Source: opts.Source}, nil
}

// ValidateReadback validates a target read from an existing MR bead. Empty
// readback falls back to the rig default branch.
func ValidateReadback(target, defaultBranch string, checker BranchChecker, checkRemote bool) (Result, error) {
	source := SourceMRReadback
	if strings.TrimSpace(target) == "" {
		target = defaultBranch
		source = SourceDefault
	}
	return Validate(ValidateOptions{
		Branch:               target,
		DefaultBranch:        defaultBranch,
		Source:               source,
		Checker:              checker,
		CheckRemote:          checkRemote,
		AllowExplicitDefault: true,
	})
}

// UniqueFormulaBaseBranch extracts formula_vars base_branch and rejects
// conflicting duplicate values.
func UniqueFormulaBaseBranch(formulaVars string) (string, error) {
	var found string
	for _, line := range strings.Split(formulaVars, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "base_branch" {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if found != "" && found != value {
			return "", fmt.Errorf("conflicting formula_vars base_branch values: %q and %q", found, value)
		}
		found = value
	}
	return found, nil
}

func validateBranchName(branch string) error {
	if strings.HasPrefix(branch, "origin/") || strings.HasPrefix(branch, "refs/") {
		return fmt.Errorf("MR target %q uses an unsafe prefix", branch)
	}
	if strings.HasPrefix(branch, "-") || strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".") {
		return fmt.Errorf("MR target %q is not a valid branch name", branch)
	}
	if strings.Contains(branch, "..") || strings.Contains(branch, "@{") || strings.Contains(branch, "//") {
		return fmt.Errorf("MR target %q is not a valid branch name", branch)
	}
	if strings.ContainsAny(branch, " ~^:?*[\\") {
		return fmt.Errorf("MR target %q contains invalid branch characters", branch)
	}
	if strings.HasSuffix(branch, ".lock") || strings.Contains(branch, "/.lock/") {
		return fmt.Errorf("MR target %q is not a valid branch name", branch)
	}
	return nil
}

func validateBranchPrefix(branch, defaultBranch string) error {
	if branch == defaultBranch || branch == "master" {
		return nil
	}
	allowedPrefixes := []string{
		"integration/",
		"feat/",
		"feature/",
		"fix/",
		"bugfix/",
		"hotfix/",
		"release/",
		"chore/",
		"test/",
		"tests/",
		"dev/",
		"adam/",
	}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(branch, prefix) {
			return nil
		}
	}
	return fmt.Errorf("MR target %q has an unsupported branch prefix", branch)
}
