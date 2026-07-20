package cmd

import "testing"

// TestShouldRetirePolecatSessionAfterDone_CapSemanticsGuard — preserved semantic
// from wkb-9688 across the upstream sync (upstream renamed the function from
// shouldSyncIdlePolecatWorktree → shouldRetirePolecatSessionAfterDone; our pr-strategy
// guard was auto-preserved by git merge). Per RPT-polecat-ci-fail-death-mechanism
// (2026-07-19): destroying the polecat branch/session at submit-time makes MERGE_FAILED
// nudges dead letters. For "pr" MQ-strategy work, retirement MUST be deferred until
// refinery confirms merge.
func TestShouldRetirePolecatSessionAfterDone_CapSemanticsGuard(t *testing.T) {
	cases := []struct {
		name          string
		exitType      string
		mergeStrategy string
		pushFailed    bool
		mrFailed      bool
		expected      bool
	}{
		// Baseline: unchanged behavior for non-pr strategies
		{"local strategy: no retire (unchanged)", ExitCompleted, "local", false, false, false},
		{"direct strategy: retire (unchanged)", ExitCompleted, "direct", false, false, true},
		{"empty strategy: retire (unchanged default)", ExitCompleted, "", false, false, true},

		// The wkb-9688 fix: pr strategy = no retire (preserve session/branch for CI-fail recovery)
		{"pr strategy: NO retire (wkb-9688 fix)", ExitCompleted, "pr", false, false, false},

		// Regression guards: existing gates still block retire
		{"pr strategy + push failed: no retire", ExitCompleted, "pr", true, false, false},
		{"pr strategy + mr failed: no retire", ExitCompleted, "pr", false, true, false},
		{"pr strategy + non-completed exit: no retire", "escalated", "pr", false, false, false},
		{"direct + push failed: no retire (guard fires)", ExitCompleted, "direct", true, false, false},
	}
	for _, c := range cases {
		got := shouldRetirePolecatSessionAfterDone(c.exitType, c.mergeStrategy, c.pushFailed, c.mrFailed)
		if got != c.expected {
			t.Errorf("%s: shouldRetirePolecatSessionAfterDone(exit=%q strat=%q push=%v mr=%v) = %v, want %v",
				c.name, c.exitType, c.mergeStrategy, c.pushFailed, c.mrFailed, got, c.expected)
		}
	}
}
