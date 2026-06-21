package polecat

import (
	"errors"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// fakeReader is an injected IssueReader: it serves canned issues by ID and can
// be told to error on lookups, so AssessActiveMR can be driven with no real bd.
type fakeReader struct {
	issues map[string]*beads.Issue
	err    error // if set, every Show returns this error
}

func (f *fakeReader) Show(id string) (*beads.Issue, error) {
	if f.err != nil {
		return nil, f.err
	}
	if iss, ok := f.issues[id]; ok {
		return iss, nil
	}
	return nil, beads.ErrNotFound
}

// terminalMR builds a reader where the MR bead is closed (terminal) and the
// source issue is closed (terminal) — the shape AssessActiveMR treats as
// "stale + source terminal" => Pending=false (confirmed merged).
func mergedReader(mrID, sourceID string) *fakeReader {
	return &fakeReader{issues: map[string]*beads.Issue{
		mrID:     {ID: mrID, Status: "closed", Description: "source_issue: " + sourceID},
		sourceID: {ID: sourceID, Status: "closed"},
	}}
}

// pendingReader builds a reader where the MR is still open (non-terminal) =>
// AssessActiveMR keeps Pending=true.
func pendingReader(mrID string) *fakeReader {
	return &fakeReader{issues: map[string]*beads.Issue{
		mrID: {ID: mrID, Status: "open"},
	}}
}

// recordingCloser records calls and returns canned results.
type recordingCloser struct {
	calls  []string
	closed int
	err    error
}

func (r *recordingCloser) close(moleculeID string) (int, error) {
	r.calls = append(r.calls, moleculeID)
	return r.closed, r.err
}

func TestEvaluateCompletionClose(t *testing.T) {
	const mrID = "rig-mr-1"
	const srcID = "rig-work-1"
	const molID = "rig-mol-1"

	staticResolve := func(string) (string, error) { return molID, nil }

	tests := []struct {
		name string

		in           CompletionCloseInput
		reader       IssueReader
		resolve      func(string) (string, error)
		closerClosed int
		closerErr    error

		wantEnabled   bool
		wantAttempted bool
		wantClosed    int
		wantCloserHit bool // closer should have been invoked
	}{
		{
			name: "flag-off => no-op, closes nothing, byte-identical default",
			in: CompletionCloseInput{
				Env:             "", // disabled
				GOOS:            "linux",
				ActiveMR:        mrID,
				SourceIssueHint: srcID,
				GitSafe:         true,
			},
			reader:        mergedReader(mrID, srcID),
			resolve:       staticResolve,
			closerClosed:  3,
			wantEnabled:   false,
			wantAttempted: false,
			wantClosed:    0,
			wantCloserHit: false,
		},
		{
			name: "merged => closes the whole chain",
			in: CompletionCloseInput{
				Env:             "1",
				GOOS:            "linux",
				ActiveMR:        mrID,
				SourceIssueHint: srcID,
				GitSafe:         true,
			},
			reader:        mergedReader(mrID, srcID),
			resolve:       staticResolve,
			closerClosed:  4,
			wantEnabled:   true,
			wantAttempted: true,
			wantClosed:    4,
			wantCloserHit: true,
		},
		{
			name: "not-merged/pending => closes nothing, leaves wisps open",
			in: CompletionCloseInput{
				Env:             "1",
				GOOS:            "linux",
				ActiveMR:        mrID,
				SourceIssueHint: srcID,
				GitSafe:         true,
			},
			reader:        pendingReader(mrID),
			resolve:       staticResolve,
			closerClosed:  4,
			wantEnabled:   true,
			wantAttempted: false,
			wantClosed:    0,
			wantCloserHit: false,
		},
		{
			name: "merged but git-unsafe => MR stays pending => closes nothing",
			in: CompletionCloseInput{
				Env:             "1",
				GOOS:            "linux",
				ActiveMR:        mrID,
				SourceIssueHint: srcID,
				GitSafe:         false, // RequireGitSafe is forced on inside the core
			},
			reader:        mergedReader(mrID, srcID),
			resolve:       staticResolve,
			closerClosed:  4,
			wantEnabled:   true,
			wantAttempted: false,
			wantClosed:    0,
			wantCloserHit: false,
		},
		{
			name: "bd/lookup error => fail-open, closes nothing, no panic",
			in: CompletionCloseInput{
				Env:             "1",
				GOOS:            "linux",
				ActiveMR:        mrID,
				SourceIssueHint: srcID,
				GitSafe:         true,
			},
			reader:        &fakeReader{err: errors.New("dolt connection refused")},
			resolve:       staticResolve,
			closerClosed:  4,
			wantEnabled:   true,
			wantAttempted: false,
			wantClosed:    0,
			wantCloserHit: false,
		},
		{
			name: "molecule resolve error => fail-open, closes nothing",
			in: CompletionCloseInput{
				Env:             "1",
				GOOS:            "linux",
				ActiveMR:        mrID,
				SourceIssueHint: srcID,
				GitSafe:         true,
			},
			reader:        mergedReader(mrID, srcID),
			resolve:       func(string) (string, error) { return "", errors.New("lookup boom") },
			closerClosed:  4,
			wantEnabled:   true,
			wantAttempted: false,
			wantClosed:    0,
			wantCloserHit: false,
		},
		{
			name: "merged but no attached molecule => idempotent no-op",
			in: CompletionCloseInput{
				Env:             "1",
				GOOS:            "linux",
				ActiveMR:        mrID,
				SourceIssueHint: srcID,
				GitSafe:         true,
			},
			reader:        mergedReader(mrID, srcID),
			resolve:       func(string) (string, error) { return "", nil },
			closerClosed:  4,
			wantEnabled:   true,
			wantAttempted: false,
			wantClosed:    0,
			wantCloserHit: false,
		},
		{
			name: "no active_mr => never closes speculatively",
			in: CompletionCloseInput{
				Env:             "1",
				GOOS:            "linux",
				ActiveMR:        "",
				SourceIssueHint: srcID,
				GitSafe:         true,
			},
			reader:        mergedReader(mrID, srcID),
			resolve:       staticResolve,
			closerClosed:  4,
			wantEnabled:   true,
			wantAttempted: false,
			wantClosed:    0,
			wantCloserHit: false,
		},
		{
			name: "non-linux host => fail-open, closes nothing",
			in: CompletionCloseInput{
				Env:             "1",
				GOOS:            "darwin",
				ActiveMR:        mrID,
				SourceIssueHint: srcID,
				GitSafe:         true,
			},
			reader:        mergedReader(mrID, srcID),
			resolve:       staticResolve,
			closerClosed:  4,
			wantEnabled:   true,
			wantAttempted: false,
			wantClosed:    0,
			wantCloserHit: false,
		},
		{
			name: "close error => fail-open with partial count, reports attempt",
			in: CompletionCloseInput{
				Env:             "1",
				GOOS:            "linux",
				ActiveMR:        mrID,
				SourceIssueHint: srcID,
				GitSafe:         true,
			},
			reader:        mergedReader(mrID, srcID),
			resolve:       staticResolve,
			closerClosed:  2, // some closed before the error
			closerErr:     errors.New("bd close failed mid-walk"),
			wantEnabled:   true,
			wantAttempted: true,
			wantClosed:    2,
			wantCloserHit: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			closer := &recordingCloser{closed: tc.closerClosed, err: tc.closerErr}
			got := EvaluateCompletionClose(tc.in, tc.reader, tc.resolve, closer.close)

			if got.Enabled != tc.wantEnabled {
				t.Errorf("Enabled = %v, want %v (reason: %s)", got.Enabled, tc.wantEnabled, got.Reason)
			}
			if got.Attempted != tc.wantAttempted {
				t.Errorf("Attempted = %v, want %v (reason: %s)", got.Attempted, tc.wantAttempted, got.Reason)
			}
			if got.Closed != tc.wantClosed {
				t.Errorf("Closed = %d, want %d (reason: %s)", got.Closed, tc.wantClosed, got.Reason)
			}
			if (len(closer.calls) > 0) != tc.wantCloserHit {
				t.Errorf("closer invoked = %v, want %v (calls=%v)", len(closer.calls) > 0, tc.wantCloserHit, closer.calls)
			}
			if got.Reason == "" {
				t.Errorf("Reason must always be set for observability")
			}
		})
	}
}

// TestEvaluateCompletionClose_Idempotent proves re-running on an
// already-closed chain is a harmless no-op: the second pass closes 0 issues
// (the injected closer reports 0, as the real bd-close walk does for
// already-closed issues) and never errors.
func TestEvaluateCompletionClose_Idempotent(t *testing.T) {
	const mrID, srcID, molID = "rig-mr-1", "rig-work-1", "rig-mol-1"
	in := CompletionCloseInput{
		Env:             "1",
		GOOS:            "linux",
		ActiveMR:        mrID,
		SourceIssueHint: srcID,
		GitSafe:         true,
	}
	reader := mergedReader(mrID, srcID)
	resolve := func(string) (string, error) { return molID, nil }

	// First pass: 4 issues closed.
	first := &recordingCloser{closed: 4}
	r1 := EvaluateCompletionClose(in, reader, resolve, first.close)
	if !r1.Attempted || r1.Closed != 4 {
		t.Fatalf("first pass: Attempted=%v Closed=%d, want true/4 (%s)", r1.Attempted, r1.Closed, r1.Reason)
	}

	// Second pass: nothing left to close — closer reports 0, no error.
	second := &recordingCloser{closed: 0}
	r2 := EvaluateCompletionClose(in, reader, resolve, second.close)
	if !r2.Attempted {
		t.Fatalf("second pass should still attempt (MR still merged): %s", r2.Reason)
	}
	if r2.Closed != 0 {
		t.Fatalf("second pass Closed = %d, want 0 (idempotent)", r2.Closed)
	}
}

// TestEvaluateCompletionClose_NoPanicOnNilDeps proves the core never panics
// even when injected dependencies are nil — it fails open instead.
func TestEvaluateCompletionClose_NoPanicOnNilDeps(t *testing.T) {
	in := CompletionCloseInput{
		Env:             "1",
		GOOS:            "linux",
		ActiveMR:        "rig-mr-1",
		SourceIssueHint: "rig-work-1",
		GitSafe:         true,
	}
	// nil reader: AssessActiveMR returns Pending (unverified) => leave open.
	if got := EvaluateCompletionClose(in, nil, func(string) (string, error) { return "m", nil }, func(string) (int, error) { return 0, nil }); got.Attempted {
		t.Errorf("nil reader must not attempt close; reason=%s", got.Reason)
	}
	// nil resolver with a merged reader => fail-open, no panic.
	merged := mergedReader("rig-mr-1", "rig-work-1")
	if got := EvaluateCompletionClose(in, merged, nil, func(string) (int, error) { return 0, nil }); got.Attempted {
		t.Errorf("nil resolver must fail open; reason=%s", got.Reason)
	}
	// nil closer with a merged reader + resolved molecule => fail-open, no panic.
	if got := EvaluateCompletionClose(in, merged, func(string) (string, error) { return "rig-mol-1", nil }, nil); got.Attempted {
		t.Errorf("nil closer must fail open; reason=%s", got.Reason)
	}
}
