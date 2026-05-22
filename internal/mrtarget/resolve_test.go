package mrtarget

import (
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

type fakeChecker struct {
	remote map[string]bool
	err    error
}

func (f fakeChecker) BranchExists(string) (bool, error) { return false, nil }

func (f fakeChecker) RemoteBranchExists(_, name string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.remote[name], nil
}

func TestResolveAcceptedTargets(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want Result
	}{
		{
			name: "direct done defaults to main",
			opts: Options{DefaultBranch: "main"},
			want: Result{Branch: "main", Source: SourceDefault},
		},
		{
			name: "explicit integration target",
			opts: Options{DefaultBranch: "main", ExplicitTarget: "integration/gt-1-2"},
			want: Result{Branch: "integration/gt-1-2", Source: SourceExplicit},
		},
		{
			name: "monorepo formula target",
			opts: Options{DefaultBranch: "main", FormulaVars: "package=cli\nbase_branch=feat/contract-review"},
			want: Result{Branch: "feat/contract-review", Source: SourceFormula},
		},
		{
			name: "mq submit auto epic target",
			opts: Options{
				DefaultBranch: "main",
				IssueID:       "gt-task",
				ResolveIntegration: func(issueID string) (string, error) {
					if issueID != "gt-task" {
						t.Fatalf("IssueID = %q", issueID)
					}
					return "integration/epic", nil
				},
			},
			want: Result{Branch: "integration/epic", Source: SourceAutoEpic},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.opts)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveRejectsConflictingDuplicateBaseBranch(t *testing.T) {
	_, err := Resolve(Options{
		DefaultBranch: "main",
		FormulaVars:    "base_branch=integration/a\nbase_branch=integration/b",
	})
	if err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("Resolve() error = %v, want conflicting duplicate base_branch", err)
	}
}

func TestResolveRejectsStaleRCATarget(t *testing.T) {
	_, err := Resolve(Options{
		DefaultBranch:  "main",
		ExplicitTarget: "integration/rca-cleanup",
	})
	if err == nil || !strings.Contains(err.Error(), "stale RCA") {
		t.Fatalf("Resolve() error = %v, want stale RCA rejection", err)
	}
}

func TestResolveRejectsExplicitDefaultMain(t *testing.T) {
	_, err := Resolve(Options{
		DefaultBranch: "main",
		SourceIssue: &beads.Issue{Description: `attached_formula: mol-polecat-work
formula_vars: base_branch=main`},
	})
	if err == nil || !strings.Contains(err.Error(), "default branch") {
		t.Fatalf("Resolve() error = %v, want explicit default rejection", err)
	}
}

func TestResolveRejectsMissingOriginBranch(t *testing.T) {
	_, err := Resolve(Options{
		DefaultBranch:  "main",
		ExplicitTarget: "integration/missing",
		Checker:        fakeChecker{remote: map[string]bool{"main": true}},
		CheckRemote:    true,
	})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Resolve() error = %v, want missing origin rejection", err)
	}
}

func TestResolveRejectsBadPrefixes(t *testing.T) {
	tests := []string{
		"origin/integration/epic",
		"refs/heads/main",
		"polecat/guzzle/gt-abc",
	}
	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			_, err := Resolve(Options{DefaultBranch: "main", ExplicitTarget: target})
			if err == nil {
				t.Fatalf("Resolve(%q) succeeded, want error", target)
			}
		})
	}
}

func TestValidateReadback(t *testing.T) {
	t.Run("fork PR base target exists", func(t *testing.T) {
		got, err := ValidateReadback("feature/upstream-base", "main", fakeChecker{remote: map[string]bool{"feature/upstream-base": true}}, true)
		if err != nil {
			t.Fatalf("ValidateReadback() error = %v", err)
		}
		if got.Branch != "feature/upstream-base" || got.Source != SourceMRReadback {
			t.Fatalf("ValidateReadback() = %+v", got)
		}
	})

	t.Run("unsafe target readback", func(t *testing.T) {
		_, err := ValidateReadback("origin/main", "main", nil, false)
		if err == nil || !strings.Contains(err.Error(), "unsafe prefix") {
			t.Fatalf("ValidateReadback() error = %v, want unsafe prefix", err)
		}
	})

	t.Run("remote check error", func(t *testing.T) {
		_, err := ValidateReadback("integration/epic", "main", fakeChecker{err: errors.New("network")}, true)
		if err == nil || !strings.Contains(err.Error(), "network") {
			t.Fatalf("ValidateReadback() error = %v, want remote error", err)
		}
	})
}
