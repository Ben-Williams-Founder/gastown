package cmd

import "testing"

// TestHookedBeadDoneAction is the deterministic V&V for the close-on-merge fix:
// when a hooked source bead has an MR awaiting the refinery, `gt done` must HOLD
// it merge-pending (block) rather than close it at submit — otherwise bd-dependents
// release onto a base whose code PR hasn't merged yet (the stale-base bug).
func TestHookedBeadDoneAction(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		unchecked int
		hasOpenMR bool
		want      string
	}{
		// The fix: open work with an MR in the queue is held, NOT closed.
		{"open + MR awaiting → block (close-on-merge)", "open", 0, true, hookedDoneBlock},
		{"in_progress + MR awaiting → block", "in_progress", 0, true, hookedDoneBlock},
		{"hooked + MR awaiting → block", "hooked", 0, true, hookedDoneBlock},
		// Preserved behavior: no MR awaiting → close at done (no-merge/direct cases).
		{"open + no MR → close", "open", 0, false, hookedDoneClose},
		{"in_progress + no MR → close", "in_progress", 0, false, hookedDoneClose},
		// Criteria gate wins over everything (existing behavior).
		{"unchecked criteria + MR → skip", "open", 2, true, hookedDoneSkip},
		// Terminal beads are never re-touched.
		{"terminal closed → skip", "closed", 0, true, hookedDoneSkip},
		{"terminal tombstone → skip", "tombstone", 0, false, hookedDoneSkip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hookedBeadDoneAction(tt.status, tt.unchecked, tt.hasOpenMR); got != tt.want {
				t.Errorf("hookedBeadDoneAction(%q, %d, %v) = %q, want %q",
					tt.status, tt.unchecked, tt.hasOpenMR, got, tt.want)
			}
		})
	}
}
