package cmd

import "testing"

// TestShouldSyncIdlePolecatWorktree_CapSemanticsGuard — pre-registered from wkb-9688.
// Per RPT-polecat-ci-fail-death-mechanism (2026-07-19): destroying the polecat branch
// at submit-time makes MERGE_FAILED nudges dead letters. For "pr" MQ-strategy work,
// the destructive sync + branch-delete MUST be deferred until refinery confirms merge.
func TestShouldSyncIdlePolecatWorktree_CapSemanticsGuard(t *testing.T) {
	cases := []struct {
		name          string
		exitType      string
		mergeStrategy string
		pushFailed    bool
		mrFailed      bool
		syncSafe      bool
		expected      bool
	}{
		// Baseline: unchanged behavior for non-pr strategies
		{"local strategy: no sync (unchanged)", ExitCompleted, "local", false, false, true, false},
		{"direct strategy: sync (unchanged)", ExitCompleted, "direct", false, false, true, true},
		{"empty strategy: sync (unchanged default)", ExitCompleted, "", false, false, true, true},

		// The wkb-9688 fix: pr strategy = no sync (preserve branch for CI-fail recovery)
		{"pr strategy: NO sync (wkb-9688 fix — preserve for CI-fail)", ExitCompleted, "pr", false, false, true, false},

		// Regression guards: existing gates still block sync
		{"pr strategy + push failed: no sync", ExitCompleted, "pr", true, false, true, false},
		{"pr strategy + mr failed: no sync", ExitCompleted, "pr", false, true, true, false},
		{"pr strategy + not sync-safe: no sync", ExitCompleted, "pr", false, false, false, false},
		{"pr strategy + non-completed exit: no sync", "escalated", "pr", false, false, true, false},

		// direct still fires
		{"direct + push failed: no sync (guard fires)", ExitCompleted, "direct", true, false, true, false},
	}
	for _, c := range cases {
		got := shouldSyncIdlePolecatWorktree(c.exitType, c.mergeStrategy, c.pushFailed, c.mrFailed, c.syncSafe)
		if got != c.expected {
			t.Errorf("%s: shouldSyncIdlePolecatWorktree(exit=%q strat=%q push=%v mr=%v safe=%v) = %v, want %v",
				c.name, c.exitType, c.mergeStrategy, c.pushFailed, c.mrFailed, c.syncSafe, got, c.expected)
		}
	}
}
