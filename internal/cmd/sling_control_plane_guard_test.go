package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// bdShowStub returns a /bin/sh bd stub whose `show` subcommand emits a single
// issue with the given status, issue_type, and labels. Mirrors the stub style
// used by sling_closed_guard_test.go.
func bdShowStub(status, issueType string, labels []string) string {
	lbls := "[]"
	if len(labels) > 0 {
		q := make([]string, len(labels))
		for i, l := range labels {
			q[i] = `"` + l + `"`
		}
		lbls = "[" + strings.Join(q, ",") + "]"
	}
	return `#!/bin/sh
case "$1" in
  show)
    echo '[{"title":"Patrol","status":"` + status + `","assignee":"","description":"","issue_type":"` + issueType + `","labels":` + lbls + `}]'
    ;;
esac
exit 0
`
}

func newControlPlaneTestEnv(t *testing.T, status, issueType string, labels []string) string {
	t.Helper()
	townRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(townRoot, ".beads"), 0o755); err != nil {
		t.Fatalf("failed to create .beads: %v", err)
	}
	binDir := filepath.Join(townRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	writeBDStub(t, binDir, bdShowStub(status, issueType, labels), "")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return townRoot
}

// TestIsRigWorkerTarget verifies the rig-worker predicate: polecat/crew targets
// are rig workers (control-plane beads must be refused). Dogs are EXCLUDED (they
// are the Deacon's control-plane executors and run operational molecules by
// design), as are control-plane agents (mayor/deacon/witness/refinery).
func TestIsRigWorkerTarget(t *testing.T) {
	rigWorkers := []string{
		"gastown/polecats/Toast",
		"whiz_kb/crew/mel",
	}
	notRigWorkers := []string{
		"deacon/dogs/alpha", // dogs run control-plane molecules by design
		"mayor",
		"mayor/",
		"deacon",
		"gastown/witness",
		"whiz_kb/refinery",
		"",
	}
	for _, w := range rigWorkers {
		if !isRigWorkerTarget(w) {
			t.Errorf("isRigWorkerTarget(%q) = false, want true (rig worker)", w)
		}
	}
	for _, n := range notRigWorkers {
		if isRigWorkerTarget(n) {
			t.Errorf("isRigWorkerTarget(%q) = true, want false", n)
		}
	}
}

// TestExecuteSling_ControlPlaneMoleculeField: molecule via the issue_type field is refused.
func TestExecuteSling_ControlPlaneMoleculeField(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	townRoot := newControlPlaneTestEnv(t, "open", "molecule", nil)
	result, err := executeSling(SlingParams{BeadID: "test-mol1", RigName: "testrig", TownRoot: townRoot})
	if err == nil {
		t.Fatal("expected error slinging a molecule (field) to a rig, got nil")
	}
	if !strings.Contains(err.Error(), "control-plane") {
		t.Errorf("error should refuse the control-plane molecule: %v", err)
	}
	if !strings.Contains(result.ErrMsg, "control-plane") {
		t.Errorf("ErrMsg should mention control-plane, got %q", result.ErrMsg)
	}
}

// TestExecuteSling_ControlPlaneConvoyViaLabel: THE fail-open fix — a convoy reads
// issue_type="task" with its real type in a gt:convoy label; it must be refused.
func TestExecuteSling_ControlPlaneConvoyViaLabel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	townRoot := newControlPlaneTestEnv(t, "open", "task", []string{"gt:convoy"})
	_, err := executeSling(SlingParams{BeadID: "test-cv1", RigName: "testrig", TownRoot: townRoot})
	if err == nil {
		t.Fatal("expected error: a gt:convoy-labelled bead (issue_type=task) must be refused, got nil (FAIL-OPEN)")
	}
	if !strings.Contains(err.Error(), "control-plane") {
		t.Errorf("label-typed convoy should be refused as control-plane: %v", err)
	}
}

// TestExecuteSling_ControlPlaneMolecule_ForceDoesNotBypass: --force must not bypass.
func TestExecuteSling_ControlPlaneMolecule_ForceDoesNotBypass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	townRoot := newControlPlaneTestEnv(t, "open", "molecule", nil)
	_, err := executeSling(SlingParams{BeadID: "test-mol2", RigName: "testrig", TownRoot: townRoot, Force: true})
	if err == nil {
		t.Fatal("expected error force-slinging a molecule, got nil")
	}
	if !strings.Contains(err.Error(), "control-plane") {
		t.Errorf("--force should not bypass the control-plane guard: %v", err)
	}
}

// TestExecuteSling_SlingableTaskNotRejected: a plain task is not refused by the guard.
func TestExecuteSling_SlingableTaskNotRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	townRoot := newControlPlaneTestEnv(t, "open", "task", nil)
	result, err := executeSling(SlingParams{BeadID: "test-task1", RigName: "testrig", TownRoot: townRoot})
	if err != nil && strings.Contains(err.Error(), "control-plane") {
		t.Errorf("slingable task wrongly refused by control-plane guard: %v", err)
	}
	if result != nil && strings.Contains(result.ErrMsg, "control-plane") {
		t.Errorf("slingable task ErrMsg wrongly mentions control-plane: %q", result.ErrMsg)
	}
}

// TestExecuteSling_CustomLeafTypeNotRejected: the over-block fix — a custom/aliased
// leaf work type (e.g. "spike") must NOT be refused as control-plane.
func TestExecuteSling_CustomLeafTypeNotRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	for _, typ := range []string{"spike", "story", "research", "docs"} {
		t.Run(typ, func(t *testing.T) {
			townRoot := newControlPlaneTestEnv(t, "open", typ, nil)
			result, err := executeSling(SlingParams{BeadID: "test-" + typ, RigName: "testrig", TownRoot: townRoot})
			if err != nil && strings.Contains(err.Error(), "control-plane") {
				t.Errorf("custom leaf type %q wrongly refused as control-plane: %v", typ, err)
			}
			if result != nil && strings.Contains(result.ErrMsg, "control-plane") {
				t.Errorf("custom leaf type %q ErrMsg wrongly mentions control-plane: %q", typ, result.ErrMsg)
			}
		})
	}
}
