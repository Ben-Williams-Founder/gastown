package beads

import (
	"errors"
	"testing"
)

type fakePendingMRLookup struct {
	activeIssue    *Issue
	activeErr      error
	ownershipIssue *Issue
	ownershipErr   error
}

func (f fakePendingMRLookup) Show(issueID string) (*Issue, error) {
	return f.activeIssue, f.activeErr
}

func (f fakePendingMRLookup) FindPendingMRForAgentOrBranch(agentBeadID, branch string) (*Issue, error) {
	return f.ownershipIssue, f.ownershipErr
}

func TestMatchesMRSourceIssue(t *testing.T) {
	tests := []struct {
		name        string
		description string
		issueID     string
		want        bool
	}{
		{
			name:        "exact match",
			description: "branch: polecat/furiosa/gt-abc@mm4heq3e\ntarget: main\nsource_issue: gt-abc\nrig: gastown\n",
			issueID:     "gt-abc",
			want:        true,
		},
		{
			name:        "no match different issue",
			description: "branch: polecat/furiosa/gt-xyz@mm4heq3e\ntarget: main\nsource_issue: gt-xyz\nrig: gastown\n",
			issueID:     "gt-abc",
			want:        false,
		},
		{
			name:        "partial ID must not match — prefix",
			description: "branch: polecat/nux/gt-abcdef@mm4heq3e\ntarget: main\nsource_issue: gt-abcdef\nrig: gastown\n",
			issueID:     "gt-abc",
			want:        false,
		},
		{
			name:        "partial ID must not match — suffix",
			description: "branch: polecat/nux/gt-abc@mm4heq3e\ntarget: main\nsource_issue: gt-abc\nrig: gastown\n",
			issueID:     "gt-abcdef",
			want:        false,
		},
		{
			name:        "match with worker field after source_issue",
			description: "branch: polecat/furiosa/la-cagb2@mm4heq3e\ntarget: main\nsource_issue: la-cagb2\nworker: polecats/furiosa\n",
			issueID:     "la-cagb2",
			want:        true,
		},
		{
			name:        "source_issue at end of description (with trailing newline)",
			description: "branch: fix/thing\nsource_issue: gt-99\n",
			issueID:     "gt-99",
			want:        true,
		},
		{
			name:        "source_issue at end without trailing newline — no match",
			description: "branch: fix/thing\nsource_issue: gt-99",
			issueID:     "gt-99",
			want:        false,
		},
		{
			name:        "empty description",
			description: "",
			issueID:     "gt-abc",
			want:        false,
		},
		{
			name:        "empty issue ID",
			description: "source_issue: gt-abc\n",
			issueID:     "",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesMRSourceIssue(tt.description, tt.issueID)
			if got != tt.want {
				t.Errorf("MatchesMRSourceIssue(%q, %q) = %v, want %v",
					tt.description, tt.issueID, got, tt.want)
			}
		})
	}
}

func TestMRMatchesAgentOrBranch(t *testing.T) {
	issue := &Issue{Description: "branch: polecat/nux/gt-abc\nagent_bead: gt-gastown-polecat-nux\n"}
	tests := []struct {
		name        string
		agentBeadID string
		branch      string
		want        bool
	}{
		{name: "matches agent bead", agentBeadID: "gt-gastown-polecat-nux", want: true},
		{name: "matches branch", branch: "polecat/nux/gt-abc", want: true},
		{name: "does not match other agent", agentBeadID: "gt-gastown-polecat-rust", want: false},
		{name: "does not match other branch", branch: "polecat/rust/gt-abc", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MRMatchesAgentOrBranch(issue, tt.agentBeadID, tt.branch); got != tt.want {
				t.Fatalf("MRMatchesAgentOrBranch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPendingMRForActiveOrOwnership(t *testing.T) {
	openActive := &Issue{ID: "mr-active", Status: "open"}
	closedActive := &Issue{ID: "mr-active", Status: "closed"}
	openOwnership := &Issue{ID: "mr-owned", Status: "open"}
	lookupErr := errors.New("bd exploded")
	tests := []struct {
		name     string
		activeMR string
		lookup   fakePendingMRLookup
		wantID   string
		wantErr  bool
	}{
		{
			name:     "open active mr wins",
			activeMR: "mr-active",
			lookup:   fakePendingMRLookup{activeIssue: openActive, ownershipIssue: openOwnership},
			wantID:   "mr-active",
		},
		{
			name:     "closed active mr falls through to ownership",
			activeMR: "mr-active",
			lookup:   fakePendingMRLookup{activeIssue: closedActive, ownershipIssue: openOwnership},
			wantID:   "mr-owned",
		},
		{
			name:     "missing active mr falls through to ownership",
			activeMR: "mr-active",
			lookup:   fakePendingMRLookup{activeErr: ErrNotFound, ownershipIssue: openOwnership},
			wantID:   "mr-owned",
		},
		{
			name:   "empty active mr can find ownership mr",
			lookup: fakePendingMRLookup{ownershipIssue: openOwnership},
			wantID: "mr-owned",
		},
		{
			name:     "terminal active mr with no ownership does not block",
			activeMR: "mr-active",
			lookup:   fakePendingMRLookup{activeIssue: closedActive},
		},
		{
			name:     "active mr lookup error fails closed",
			activeMR: "mr-active",
			lookup:   fakePendingMRLookup{activeErr: lookupErr},
			wantErr:  true,
		},
		{
			name:     "ownership lookup error fails closed",
			activeMR: "mr-active",
			lookup:   fakePendingMRLookup{activeIssue: closedActive, ownershipErr: lookupErr},
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PendingMRForActiveOrOwnership(tt.lookup, tt.activeMR, "gt-gastown-polecat-nux", "polecat/nux/gt-abc")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			gotID := ""
			if got != nil {
				gotID = got.ID
			}
			if gotID != tt.wantID {
				t.Fatalf("MR ID = %q, want %q", gotID, tt.wantID)
			}
		})
	}
}
