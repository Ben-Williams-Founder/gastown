package polecat

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/tmux"
)

// Regression test for the fail-closed BLOCKED predicate on the `gt session start
// --issue` bypass (hq-vcg3).
//
// SessionManager.Start() hooks the issue by shelling `bd update <id>
// --status=hooked --assignee=<rig>/polecats/<name>` directly (hookIssue), so it
// never passes through gt sling / gt hook and inherits none of their eligibility
// guards. validateIssue() is the pre-hook gate Start() already calls, so the
// predicate lives there.

// newBlockedGuardSessionManager builds a SessionManager over a temp town whose bd
// stub reports every issue with the given status, and chdirs nowhere — validateIssue
// resolves its own bd working dir from the rig path.
func newBlockedGuardSessionManager(t *testing.T, status string) (*SessionManager, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bd stub")
	}

	townRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(townRoot, ".beads"), 0o755); err != nil {
		t.Fatalf("mkdir town beads: %v", err)
	}
	rigPath := filepath.Join(townRoot, "gastown")
	workDir := filepath.Join(rigPath, "polecats", "Toast")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir polecat workdir: %v", err)
	}

	binDir := filepath.Join(townRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	stub := `#!/bin/sh
case "$1" in
  show) echo '[{"id":"gt-x1","title":"Held work","status":"` + status + `","assignee":"","description":""}]' ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write bd stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := NewSessionManager(tmux.NewTmux(), &rig.Rig{Name: "gastown", Path: rigPath})
	return m, workDir
}

// TestValidateIssue_BlockedBeadRefused: a BLOCKED bead must not be hookable onto a
// polecat via `gt session start --issue`.
func TestValidateIssue_BlockedBeadRefused(t *testing.T) {
	for _, status := range []string{"blocked", "BLOCKED", " Blocked "} {
		t.Run(strings.ReplaceAll(status, " ", "_"), func(t *testing.T) {
			m, workDir := newBlockedGuardSessionManager(t, status)
			err := m.validateIssue("gt-x1", workDir)
			if err == nil {
				t.Fatal("expected refusal for a BLOCKED bead — gt session start would hook it onto the polecat")
			}
			if !strings.Contains(err.Error(), "BLOCKED") {
				t.Errorf("refusal must name the BLOCKED status so the operator sees why: %v", err)
			}
			if !strings.Contains(err.Error(), "gt-x1") {
				t.Errorf("refusal must name the bead: %v", err)
			}
		})
	}
}

// TestValidateIssue_NonBlockedBeadStillValid is the false-positive guard: normal
// work must still start a session.
func TestValidateIssue_NonBlockedBeadStillValid(t *testing.T) {
	for _, status := range []string{"open", "in_progress", "hooked", "unblocked"} {
		t.Run(status, func(t *testing.T) {
			m, workDir := newBlockedGuardSessionManager(t, status)
			if err := m.validateIssue("gt-x1", workDir); err != nil {
				t.Errorf("status %q wrongly refused (false positive): %v", status, err)
			}
		})
	}
}
