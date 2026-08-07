//go:build integration

package doltserver_test

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/steveyegge/gastown/internal/testutil"

	"github.com/steveyegge/gastown/internal/doltserver"
)

// startIsolatedDoltContainer starts a containerized Dolt server and returns
// a townRoot directory suitable for DefaultConfig. GT_DOLT_PORT is set
// automatically by the container helper.
func startIsolatedDoltContainer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("dolt"); err != nil {
		t.Skip("dolt not found in PATH — skipping integration test")
	}
	testutil.StartIsolatedDoltContainer(t)
	townRoot := t.TempDir()
	// Create the data dir on the host so InitRig doesn't mistake the
	// containerized server for an orphaned process.
	if err := os.MkdirAll(doltserver.DefaultConfig(townRoot).DataDir, 0755); err != nil {
		t.Fatalf("creating data dir: %v", err)
	}
	return townRoot
}

// TestRealWLCommonsStore_Conformance runs the conformance suite against a real Dolt server.
func TestRealWLCommonsStore_Conformance(t *testing.T) {
	townRoot := startIsolatedDoltContainer(t)

	// Run subtests sequentially (parallel=false) to prevent concurrent
	// DOLT_COMMIT calls from racing on the shared wl_commons working set.
	// Each subtest's newStore call ensures the DB exists (idempotent).
	doltserver.WLCommonsConformanceForTest(t, func(t *testing.T) doltserver.WLCommonsStore {
		t.Helper()
		store := doltserver.NewWLCommons(townRoot)
		if err := store.EnsureDB(); err != nil {
			t.Fatalf("EnsureDB() error: %v", err)
		}
		return store
	}, false)
}

// TestIsNothingToCommit_RealDolt verifies that isNothingToCommit correctly detects
// the error produced by DOLT_COMMIT when no changes exist. This pins the detection
// logic against the actual Dolt error text so that Dolt upgrades that change the
// message wording are caught immediately.
func TestIsNothingToCommit_RealDolt(t *testing.T) {
	townRoot := startIsolatedDoltContainer(t)

	// Create a database and table so we have a valid context for DOLT_COMMIT.
	initScript := fmt.Sprintf(`CREATE DATABASE IF NOT EXISTS %s;
USE %s;
CREATE TABLE IF NOT EXISTS _ping (id INT PRIMARY KEY);
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('-m', 'init ping table');
`, doltserver.WLCommonsDB, doltserver.WLCommonsDB)
	if err := doltserver.DoltSQLScriptForTest(townRoot, initScript); err != nil {
		t.Fatalf("init script error: %v", err)
	}

	// Now try to commit with no changes — this should produce the "nothing to commit" error.
	noopScript := fmt.Sprintf(`USE %s;
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('-m', 'noop');
`, doltserver.WLCommonsDB)
	err := doltserver.DoltSQLScriptForTest(townRoot, noopScript)
	if err == nil {
		t.Fatal("expected error from DOLT_COMMIT with no changes, got nil")
	}

	if !doltserver.IsNothingToCommitForTest(err) {
		t.Errorf("doltserver.IsNothingToCommitForTest(%q) = false, want true — Dolt error text may have changed", err)
	}
}
