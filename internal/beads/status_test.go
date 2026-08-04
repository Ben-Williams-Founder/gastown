package beads

import "testing"

func TestAgentStateProtectsFromCleanup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state AgentState
		want  bool
	}{
		{AgentStateStuck, true},
		{AgentStateAwaitingGate, true},
		{AgentStatePaused, true},
		{AgentStateWorking, false},
		{AgentStateIdle, false},
		{AgentStateDone, false},
		{AgentStateSpawning, false},
		{AgentStateNuked, false},
		{AgentStateRunning, false},
		{AgentStateEscalated, false},
		{AgentStatePatrolling, false},
		{AgentState(""), false},
	}
	for _, tt := range tests {
		if got := tt.state.ProtectsFromCleanup(); got != tt.want {
			t.Errorf("AgentState(%q).ProtectsFromCleanup() = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestAgentStateIsActive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state AgentState
		want  bool
	}{
		{AgentStateWorking, true},
		{AgentStateRunning, true},
		{AgentStateSpawning, true},
		{AgentStatePatrolling, true},
		{AgentStateIdle, false},
		{AgentStateDone, false},
		{AgentStateStuck, false},
		{AgentStateNuked, false},
		{AgentStatePaused, false},
	}
	for _, tt := range tests {
		if got := tt.state.IsActive(); got != tt.want {
			t.Errorf("AgentState(%q).IsActive() = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestIssueStatusBlocksRemoval(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status IssueStatus
		want   bool
	}{
		{StatusOpen, true},
		{StatusClosed, false},
		{IssueStatusHooked, false},
		{IssueStatusPinned, false},
		{StatusInProgress, false},
		{StatusTombstone, false},
		{StatusDeferred, false},
	}
	for _, tt := range tests {
		if got := tt.status.BlocksRemoval(); got != tt.want {
			t.Errorf("IssueStatus(%q).BlocksRemoval() = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestIssueStatusIsTerminal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status IssueStatus
		want   bool
	}{
		{StatusClosed, true},
		{StatusTombstone, true},
		{StatusOpen, false},
		{IssueStatusHooked, false},
		{StatusInProgress, false},
		{IssueStatusPinned, false},
		{StatusDeferred, false},
	}
	for _, tt := range tests {
		if got := tt.status.IsTerminal(); got != tt.want {
			t.Errorf("IssueStatus(%q).IsTerminal() = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestIssueStatusIsAssigned(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status IssueStatus
		want   bool
	}{
		{IssueStatusHooked, true},
		{StatusInProgress, true},
		{StatusOpen, false},
		{StatusClosed, false},
		{IssueStatusPinned, false},
		{StatusDeferred, false},
	}
	for _, tt := range tests {
		if got := tt.status.IsAssigned(); got != tt.want {
			t.Errorf("IssueStatus(%q).IsAssigned() = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestAgentStateConstants(t *testing.T) {
	t.Parallel()
	// Verify all expected agent states match their string values
	states := map[AgentState]string{
		AgentStateSpawning:     "spawning",
		AgentStateWorking:      "working",
		AgentStateDone:         "done",
		AgentStateStuck:        "stuck",
		AgentStateEscalated:    "escalated",
		AgentStateIdle:         "idle",
		AgentStateRunning:      "running",
		AgentStateNuked:        "nuked",
		AgentStateAwaitingGate: "awaiting-gate",
		AgentStatePatrolling:   "patrolling",
		AgentStatePaused:       "paused",
	}
	for state, expected := range states {
		if string(state) != expected {
			t.Errorf("AgentState constant %q has value %q, want %q", expected, string(state), expected)
		}
	}
}

func TestIssueStatusConstants(t *testing.T) {
	t.Parallel()
	statuses := map[IssueStatus]string{
		StatusOpen:        "open",
		StatusClosed:      "closed",
		StatusInProgress:  "in_progress",
		StatusTombstone:   "tombstone",
		StatusBlocked:     "blocked",
		StatusDeferred:    "deferred",
		IssueStatusPinned: "pinned",
		IssueStatusHooked: "hooked",
	}
	for status, expected := range statuses {
		if string(status) != expected {
			t.Errorf("IssueStatus constant %q has value %q, want %q", expected, string(status), expected)
		}
	}
}

// TestIsBlockedStatus verifies the fail-closed BLOCKED dispatch predicate
// (hq-vcg3). bd does NOT normalize the status on write, so casing/whitespace
// variants must all classify as blocked; and the match must be exact so that
// look-alike statuses are not swept up (a strings.Contains implementation would
// wrongly refuse "unblocked").
func TestIsBlockedStatus(t *testing.T) {
	t.Parallel()
	blocked := []string{
		"blocked",
		"BLOCKED",
		"Blocked",
		"  blocked  ",
		"\tblocked\n",
	}
	notBlocked := []string{
		"", // unset status must not read as blocked here — callers fail closed on unreadable info separately
		"open",
		"closed",
		"in_progress",
		"tombstone",
		"deferred",
		"pinned",
		"hooked",
		"unblocked", // substring trap
		"blocker",   // substring trap
		"blocked_by",
	}
	for _, s := range blocked {
		if !IsBlockedStatus(s) {
			t.Errorf("IsBlockedStatus(%q) = false, want true (fail-open: a BLOCKED bead would be dispatched)", s)
		}
	}
	for _, s := range notBlocked {
		if IsBlockedStatus(s) {
			t.Errorf("IsBlockedStatus(%q) = true, want false (over-block: dispatchable work would be refused)", s)
		}
	}
}
