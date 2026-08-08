package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
	gitpkg "github.com/steveyegge/gastown/internal/git"
)

func TestResolveMQSubmitCommitSHAUsesSubmittedBranch(t *testing.T) {
	repo := t.TempDir()
	runGitForMQSubmitTest(t, repo, "init")
	runGitForMQSubmitTest(t, repo, "config", "user.email", "test@example.com")
	runGitForMQSubmitTest(t, repo, "config", "user.name", "Test User")

	writeMQSubmitTestFile(t, repo, "file.txt", "main\n")
	runGitForMQSubmitTest(t, repo, "add", "file.txt")
	runGitForMQSubmitTest(t, repo, "commit", "-m", "main")
	runGitForMQSubmitTest(t, repo, "branch", "-M", "main")
	mainSHA := runGitForMQSubmitTest(t, repo, "rev-parse", "HEAD")

	runGitForMQSubmitTest(t, repo, "checkout", "-b", "feature/pr-target")
	writeMQSubmitTestFile(t, repo, "file.txt", "feature\n")
	runGitForMQSubmitTest(t, repo, "commit", "-am", "feature")
	featureSHA := runGitForMQSubmitTest(t, repo, "rev-parse", "HEAD")
	runGitForMQSubmitTest(t, repo, "tag", "feature/pr-target", mainSHA)

	runGitForMQSubmitTest(t, repo, "checkout", "main")
	g := gitpkg.NewGit(repo)
	got, err := resolveMQSubmitCommitSHA(g, "feature/pr-target")
	if err != nil {
		t.Fatalf("resolveMQSubmitCommitSHA: %v", err)
	}
	if got != featureSHA {
		t.Fatalf("resolveMQSubmitCommitSHA() = %s, want submitted branch tip %s", got, featureSHA)
	}
	if got == mainSHA {
		t.Fatalf("resolveMQSubmitCommitSHA() used HEAD %s instead of submitted branch tip", mainSHA)
	}
}

func TestVerifyMQSubmitPushedBranchRequiresRemoteBranch(t *testing.T) {
	repo := t.TempDir()
	remote := t.TempDir()
	runGitForMQSubmitTest(t, remote, "init", "--bare")

	runGitForMQSubmitTest(t, repo, "init")
	runGitForMQSubmitTest(t, repo, "config", "user.email", "test@example.com")
	runGitForMQSubmitTest(t, repo, "config", "user.name", "Test User")
	runGitForMQSubmitTest(t, repo, "remote", "add", "origin", remote)

	writeMQSubmitTestFile(t, repo, "file.txt", "main\n")
	runGitForMQSubmitTest(t, repo, "add", "file.txt")
	runGitForMQSubmitTest(t, repo, "commit", "-m", "main")
	runGitForMQSubmitTest(t, repo, "branch", "-M", "main")
	runGitForMQSubmitTest(t, repo, "push", "-u", "origin", "main")

	runGitForMQSubmitTest(t, repo, "checkout", "-b", "feature/pr-target")
	writeMQSubmitTestFile(t, repo, "file.txt", "feature\n")
	runGitForMQSubmitTest(t, repo, "commit", "-am", "feature")
	featureSHA := runGitForMQSubmitTest(t, repo, "rev-parse", "HEAD")

	g := gitpkg.NewGit(repo)
	err := verifyMQSubmitPushedBranch(g, "feature/pr-target", featureSHA)
	if err == nil {
		t.Fatal("verifyMQSubmitPushedBranch() = nil, want missing remote branch error")
	}
	for _, want := range []string{"git push origin feature/pr-target", "gt done"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("verifyMQSubmitPushedBranch() error missing %q: %v", want, err)
		}
	}

	runGitForMQSubmitTest(t, repo, "push", "origin", "feature/pr-target")
	if err := verifyMQSubmitPushedBranch(g, "feature/pr-target", featureSHA); err != nil {
		t.Fatalf("verifyMQSubmitPushedBranch() after push: %v", err)
	}
}

func runGitForMQSubmitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeMQSubmitTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMoleculePrereqs(t *testing.T) {
	tests := []struct {
		name      string
		children  []*beads.Issue
		wantErr   bool
		wantInErr []string // Substrings expected in error message
	}{
		{
			name:     "nil children",
			children: nil,
			wantErr:  false,
		},
		{
			name:     "empty children",
			children: []*beads.Issue{},
			wantErr:  false,
		},
		{
			name: "all prereqs closed",
			children: []*beads.Issue{
				{ID: "gt-mol.1", Title: "Load context", Status: "closed"},
				{ID: "gt-mol.2", Title: "Set up branch", Status: "closed"},
				{ID: "gt-mol.3", Title: "Implement", Status: "closed"},
				{ID: "gt-mol.4", Title: "Self-review", Status: "closed"},
				{ID: "gt-mol.5", Title: "Build check", Status: "closed"},
				{ID: "gt-mol.6", Title: "Commit changes", Status: "closed"},
				{ID: "gt-mol.7", Title: "Rebase verify", Status: "closed"},
				{ID: "gt-mol.8", Title: "Submit MR", Status: "open"},
				{ID: "gt-mol.9", Title: "Wait for verdict", Status: "open"},
				{ID: "gt-mol.10", Title: "Self-clean", Status: "open"},
			},
			wantErr: false,
		},
		{
			name: "missing self-review step",
			children: []*beads.Issue{
				{ID: "gt-mol.1", Title: "Load context", Status: "closed"},
				{ID: "gt-mol.2", Title: "Set up branch", Status: "closed"},
				{ID: "gt-mol.3", Title: "Implement", Status: "closed"},
				{ID: "gt-mol.4", Title: "Self-review", Status: "open"},
				{ID: "gt-mol.5", Title: "Build check", Status: "closed"},
				{ID: "gt-mol.6", Title: "Commit changes", Status: "closed"},
				{ID: "gt-mol.7", Title: "Rebase verify", Status: "closed"},
				{ID: "gt-mol.8", Title: "Submit MR", Status: "open"},
			},
			wantErr:   true,
			wantInErr: []string{"gt-mol.4", "Self-review", "--skip-deps"},
		},
		{
			name: "multiple incomplete steps",
			children: []*beads.Issue{
				{ID: "gt-mol.1", Title: "Load context", Status: "closed"},
				{ID: "gt-mol.2", Title: "Set up branch", Status: "open"},
				{ID: "gt-mol.3", Title: "Implement", Status: "in_progress"},
				{ID: "gt-mol.4", Title: "Self-review", Status: "open"},
				{ID: "gt-mol.5", Title: "Submit MR", Status: "open"},
			},
			wantErr:   true,
			wantInErr: []string{"gt-mol.2", "gt-mol.3", "gt-mol.4"},
		},
		{
			name: "no submit step found — checks all steps",
			children: []*beads.Issue{
				{ID: "gt-mol.1", Title: "Load context", Status: "closed"},
				{ID: "gt-mol.2", Title: "Implement", Status: "open"},
				{ID: "gt-mol.3", Title: "Build check", Status: "open"},
			},
			wantErr:   true,
			wantInErr: []string{"gt-mol.2", "gt-mol.3"},
		},
		{
			name: "post-submit steps open is OK",
			children: []*beads.Issue{
				{ID: "gt-mol.1", Title: "Load context", Status: "closed"},
				{ID: "gt-mol.2", Title: "Submit MR", Status: "open"},
				{ID: "gt-mol.3", Title: "Wait for verdict", Status: "open"},
			},
			wantErr: false,
		},
		{
			name: "case insensitive submit detection",
			children: []*beads.Issue{
				{ID: "gt-mol.1", Title: "Implement", Status: "closed"},
				{ID: "gt-mol.2", Title: "SUBMIT MR and enter awaiting_verdict", Status: "open"},
				{ID: "gt-mol.3", Title: "Self-clean", Status: "open"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMoleculePrereqs(tt.children)
			if tt.wantErr && err == nil {
				t.Errorf("validateMoleculePrereqs() = nil, want error")
				return
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateMoleculePrereqs() = %v, want nil", err)
				return
			}
			if err != nil {
				errMsg := err.Error()
				for _, want := range tt.wantInErr {
					if !strings.Contains(errMsg, want) {
						t.Errorf("error message missing %q, got: %s", want, errMsg)
					}
				}
			}
		})
	}
}

// The L1/L1b guards of DEC-OPS-mq-cycle-branch-binding, exercised against the
// three mis-submits that motivated them (2026-08-07/08). The inputs are the
// real branch names from experiments/mq-branch-selection-probe/.
//
// I2 is the honest negative: two non-empty branches for one bead are
// indistinguishable at this boundary, and L1 is not claimed to catch it. That
// class needs the cycle pointer (L2). If a later change makes this case refuse,
// the assertion below should be revisited deliberately, not deleted.
func TestMQSubmitL1GuardsAgainstHistoricalIncidents(t *testing.T) {
	tests := []struct {
		name          string
		branch        string
		parsedIssue   string
		explicitIssue string
		aheadOfTarget int
		wantRefusal   mqRefusalReason
	}{
		{
			name:          "I1 wkb-dmfa: foreign bead's branch submitted under --issue",
			branch:        "polecat/furiosa/wkb-03lk+msdy2crb",
			parsedIssue:   "wkb-03lk",
			explicitIssue: "wkb-dmfa",
			aheadOfTarget: 3,
			wantRefusal:   mqRefuseBranchIssueMismatch,
		},
		{
			name:          "I3 wkb-rs0a: dead cycle with nothing against target",
			branch:        "polecat/furiosa/wkb-rs0a+mskhyay7",
			parsedIssue:   "wkb-rs0a",
			explicitIssue: "wkb-rs0a",
			aheadOfTarget: 0,
			wantRefusal:   mqRefuseEmptyBranch,
		},
		{
			name:          "I2 wkb-rtxe: pre-review branch, non-empty and correctly named - L1 cannot see it",
			branch:        "polecat/furiosa/wkb-rtxe+mskdatv6",
			parsedIssue:   "wkb-rtxe",
			explicitIssue: "wkb-rtxe",
			aheadOfTarget: 2,
			wantRefusal:   "",
		},
		{
			name:          "live cycle submitted normally is accepted",
			branch:        "polecat/furiosa/wkb-rs0a+mskn62qd",
			parsedIssue:   "wkb-rs0a",
			explicitIssue: "wkb-rs0a",
			aheadOfTarget: 1,
			wantRefusal:   "",
		},
		{
			name:          "no --issue given: branch id is authoritative, not a disagreement",
			branch:        "polecat/furiosa/wkb-rs0a+mskn62qd",
			parsedIssue:   "wkb-rs0a",
			explicitIssue: "",
			aheadOfTarget: 1,
			wantRefusal:   "",
		},
		{
			name:          "unparsable branch with explicit issue is not a disagreement",
			branch:        "hotfix/manual-patch",
			parsedIssue:   "",
			explicitIssue: "wkb-rs0a",
			aheadOfTarget: 1,
			wantRefusal:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got mqRefusalReason
			switch {
			case mqBranchIssueDisagreement(tt.parsedIssue, tt.explicitIssue):
				got = mqRefuseBranchIssueMismatch
			case mqBranchIsEmpty(tt.aheadOfTarget):
				got = mqRefuseEmptyBranch
			}
			if got != tt.wantRefusal {
				t.Errorf("branch %s: got refusal %q, want %q", tt.branch, got, tt.wantRefusal)
			}
		})
	}
}

// L1b: a superseded branch holding commits the superseding branch lacks must
// survive. This is the fail-dangerous path the guard removes — a stale submit
// superseding the live MR and deleting the live cycle's only remote copy.
func TestSupersededBranchDeletionGuard(t *testing.T) {
	tests := []struct {
		name        string
		oldAhead    int
		wantKeep    bool
		description string
	}{
		{"superseded branch is ahead - keep it", 2, true, "stale submit superseding the live MR"},
		{"superseded branch has nothing extra - safe to delete", 0, false, "ordinary redispatch cleanup"},
		{"superseded branch far ahead - keep it", 17, true, "long-running cycle superseded by an empty one"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := supersededBranchIsAhead(tt.oldAhead); got != tt.wantKeep {
				t.Errorf("%s: supersededBranchIsAhead(%d) = %v, want %v", tt.description, tt.oldAhead, got, tt.wantKeep)
			}
		})
	}
}

// The escalation must stay auditable: --force without a reason is itself a
// refusal, so an override can never be silent.
func TestMQSubmitForceRequiresReason(t *testing.T) {
	origForce, origReason := mqSubmitForce, mqSubmitForceReason
	t.Cleanup(func() { mqSubmitForce, mqSubmitForceReason = origForce, origReason })

	tests := []struct {
		name    string
		force   bool
		reason  string
		wantErr bool
	}{
		{"force with reason is accepted", true, "live cycle unpushed, verified by hand", false},
		{"force without reason is refused", true, "", true},
		{"force with blank reason is refused", true, "   ", true},
		{"reason without force is refused", false, "why", true},
		{"neither flag is the normal path", false, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mqSubmitForce, mqSubmitForceReason = tt.force, tt.reason
			err := mqSubmitCheckForceUsage()
			if (err != nil) != tt.wantErr {
				t.Errorf("mqSubmitCheckForceUsage() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Refusals lead with a stable key=value prefix so callers can match on the
// reason without parsing prose, and they name the sibling branches.
func TestMQRefusalMessageIsMachineReadable(t *testing.T) {
	r := &mqRefusal{
		Reason: mqRefuseEmptyBranch,
		Branch: "polecat/furiosa/wkb-rs0a+mskhyay7",
		Detail: "nothing to merge",
		Candidates: []mqBranchCandidate{
			{Branch: "polecat/furiosa/wkb-rs0a+mskn62qd", Ahead: 1},
		},
	}
	msg := r.Error()
	for _, want := range []string{
		"mq-submit-refused: reason=empty-branch",
		"branch=polecat/furiosa/wkb-rs0a+mskhyay7",
		"polecat/furiosa/wkb-rs0a+mskn62qd",
		"1 commit(s) ahead",
		"--force-reason",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message missing %q\ngot: %s", want, msg)
		}
	}
}
