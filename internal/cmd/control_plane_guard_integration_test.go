//go:build integration

package cmd

import (
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestControlPlaneGuard_Integration drives the REAL gt binary against a REAL
// isolated town + rig + crew on a private local Dolt (a free port, not the
// production :3307 — no production impact) and verifies the control-plane guard
// end-to-end via `gt assign`, including the gt:<type> LABEL fail-open case and
// the previously-missed `queue` type. `gt assign` checks the requested type/label
// before creating the bead, so it exercises the guard directly with no bead-ID or
// agent-runtime dependency. Modeled on the fresh-setup integration flow.
func TestControlPlaneGuard_Integration(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed")
	}
	if _, err := exec.LookPath("dolt"); err != nil {
		t.Skip("dolt not installed")
	}

	tmpDir := resolveSymlinks(t, t.TempDir())
	hqPath := filepath.Join(tmpDir, "town")
	doltPort := strconv.Itoa(freeTCPPort(t))
	t.Setenv("GT_DOLT_PORT", doltPort)
	t.Setenv("BEADS_DOLT_PORT", doltPort)

	env := freshSetupIntegrationEnv(tmpDir, doltPort)
	configureGitIdentityForEnv(t, env)

	gtBinary := buildGT(t)
	runFreshSetupCmd(t, "", env, gtBinary, "install", hqPath, "--name", "test-town", "--git", "--dolt-port", doltPort)
	t.Cleanup(func() {
		c := exec.Command(gtBinary, "dolt", "stop")
		c.Dir = hqPath
		c.Env = env
		_ = c.Run()
	})

	repoURL := (&url.URL{Scheme: "file", Path: createFreshSetupSourceRepo(t, tmpDir)}).String()
	runFreshSetupCmd(t, hqPath, env, gtBinary, "rig", "add", "testrig", repoURL, "--prefix", "cpg", "--branch", "main")
	runFreshSetupCmd(t, hqPath, env, gtBinary, "crew", "add", "jayne", "--rig", "testrig")

	// gt assign <crew> <title> [--type/--label] — always hooks to a rig worker (crew).
	assign := func(title string, extra ...string) (string, error) {
		args := append([]string{"assign", "jayne", title, "--rig", "testrig"}, extra...)
		c := exec.Command(gtBinary, args...)
		c.Dir = hqPath
		c.Env = env
		out, err := c.CombinedOutput()
		return string(out), err
	}
	mentionsControlPlane := func(out string, err error) bool {
		s := out
		if err != nil {
			s += " " + err.Error()
		}
		return strings.Contains(s, "control-plane")
	}

	// molecule via the type field → refused.
	t.Run("molecule_type_refused", func(t *testing.T) {
		out, err := assign("patrol digest", "--type", "molecule")
		if !mentionsControlPlane(out, err) {
			t.Errorf("--type=molecule must be refused as control-plane; err=%v out=%s", err, out)
		}
	})

	// gt:convoy label fail-open case: type defaults to task, real type is in the label.
	t.Run("convoy_via_label_refused", func(t *testing.T) {
		out, err := assign("convoy work", "--label", "gt:convoy")
		if !mentionsControlPlane(out, err) {
			t.Errorf("--label=gt:convoy must be refused as control-plane; err=%v out=%s", err, out)
		}
	})

	// gt:queue — the type the hand-written denylist missed; caught after deriving
	// the denylist from constants.BeadsCustomTypesList().
	t.Run("queue_via_label_refused", func(t *testing.T) {
		out, err := assign("queue work", "--label", "gt:queue")
		if !mentionsControlPlane(out, err) {
			t.Errorf("--label=gt:queue must be refused (regression fix); err=%v out=%s", err, out)
		}
	})

	// NOTE: the gt sling / gt hook paths share the identical IsControlPlaneBead +
	// isRigWorkerTarget logic exercised here via gt assign, but they can't be driven
	// in this harness because gt's bead-ID validator rejects the test rig's
	// auto-generated prefix (it contains a digit, "cpg1-...", on this base branch)
	// before the guard is reached — a setup artifact, not the guard. Those paths are
	// covered by their unit/component tests plus this assign end-to-end run.

	// A plain work task must NOT be refused — assign creates + hooks it.
	t.Run("work_task_assigned_successfully", func(t *testing.T) {
		out, err := assign("real work", "--type", "task")
		if mentionsControlPlane(out, err) {
			t.Errorf("plain work task wrongly refused as control-plane; err=%v out=%s", err, out)
		}
		if err != nil {
			t.Errorf("plain work task assign failed for a non-control-plane reason: %v\n%s", err, out)
		}
	})
}
