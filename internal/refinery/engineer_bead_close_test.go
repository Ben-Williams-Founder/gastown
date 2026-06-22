package refinery

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// fakeWorkBeadCloser is an in-memory stand-in for *beads.Beads, exercising the
// merge->bead-closed behaviour without a live Dolt server.
type fakeWorkBeadCloser struct {
	issues     map[string]*beads.Issue
	closeCalls []string // work-bead IDs passed to ForceCloseWithReason, in order
	closeErr   error    // if set, ForceCloseWithReason returns this error
}

func newFakeCloser() *fakeWorkBeadCloser {
	return &fakeWorkBeadCloser{issues: map[string]*beads.Issue{}}
}

func (f *fakeWorkBeadCloser) add(i *beads.Issue) { f.issues[i.ID] = i }

func (f *fakeWorkBeadCloser) Show(id string) (*beads.Issue, error) {
	i, ok := f.issues[id]
	if !ok {
		return nil, fmt.Errorf("issue %s not found", id)
	}
	return i, nil
}

func (f *fakeWorkBeadCloser) ForceCloseWithReason(reason string, ids ...string) error {
	if f.closeErr != nil {
		return f.closeErr
	}
	for _, id := range ids {
		f.closeCalls = append(f.closeCalls, id)
		if i, ok := f.issues[id]; ok {
			i.Status = string(beads.StatusClosed)
		}
	}
	return nil
}

// TestCloseMergedWorkBead_SuccessClosesSourceIssue proves that a successful
// merge closes the source work bead — the core fix for re-dispatch churn.
func TestCloseMergedWorkBead_SuccessClosesSourceIssue(t *testing.T) {
	f := newFakeCloser()
	f.add(&beads.Issue{ID: "gt-work", Status: string(beads.StatusOpen)})

	var out bytes.Buffer
	mr := &MRInfo{ID: "gt-mr", SourceIssue: "gt-work", Target: "main"}

	closeMergedWorkBead(f, &out, mr, "abc123")

	if got := f.issues["gt-work"].Status; got != string(beads.StatusClosed) {
		t.Fatalf("work bead status = %q, want closed", got)
	}
	if len(f.closeCalls) != 1 || f.closeCalls[0] != "gt-work" {
		t.Fatalf("close calls = %v, want [gt-work]", f.closeCalls)
	}
}

// TestCloseMergedWorkBead_FallsBackToAgentActiveMR proves the active_mr -> bead
// fallback: when the MR has no source_issue, the work bead is resolved from the
// worker agent bead's last_source_issue. This is the path that previously left
// the work bead open (nothing to close) and caused re-dispatch.
func TestCloseMergedWorkBead_FallsBackToAgentActiveMR(t *testing.T) {
	f := newFakeCloser()
	f.add(&beads.Issue{ID: "gt-work", Status: string(beads.StatusOpen)})
	f.add(&beads.Issue{
		ID:          "agent-bead",
		Status:      string(beads.StatusOpen),
		Description: "active_mr: gt-mr\nlast_source_issue: gt-work\n",
	})

	var out bytes.Buffer
	mr := &MRInfo{ID: "gt-mr", SourceIssue: "", AgentBead: "agent-bead", Target: "main"}

	closeMergedWorkBead(f, &out, mr, "abc123")

	if got := f.issues["gt-work"].Status; got != string(beads.StatusClosed) {
		t.Fatalf("work bead status = %q, want closed (via agent active_mr fallback)", got)
	}
}

// TestCloseMergedWorkBead_Idempotent proves that an already-closed work bead is
// a no-op — no error, no second close call. Covers the polecat-already-ran-
// gt-done race and re-run of post-merge cleanup.
func TestCloseMergedWorkBead_Idempotent(t *testing.T) {
	f := newFakeCloser()
	f.add(&beads.Issue{ID: "gt-work", Status: string(beads.StatusClosed)})

	var out bytes.Buffer
	mr := &MRInfo{ID: "gt-mr", SourceIssue: "gt-work", Target: "main"}

	closeMergedWorkBead(f, &out, mr, "abc123")

	if len(f.closeCalls) != 0 {
		t.Fatalf("expected no close calls for already-terminal bead, got %v", f.closeCalls)
	}
}

// TestCloseMergedWorkBead_NoResolvableBead proves that an MR with no
// source_issue and no agent bead is a safe no-op (nothing to close, no panic).
func TestCloseMergedWorkBead_NoResolvableBead(t *testing.T) {
	f := newFakeCloser()
	var out bytes.Buffer
	mr := &MRInfo{ID: "gt-mr", Target: "main"}

	closeMergedWorkBead(f, &out, mr, "")

	if len(f.closeCalls) != 0 {
		t.Fatalf("expected no close calls, got %v", f.closeCalls)
	}
}

// TestCloseMergedWorkBead_CloseErrorDoesNotPanic proves that a hard close
// failure (work bead still open afterward) is reported as a warning, not a
// panic or a false "closed". The bead correctly stays open so dispatch state is
// truthful.
func TestCloseMergedWorkBead_CloseErrorDoesNotPanic(t *testing.T) {
	f := newFakeCloser()
	f.add(&beads.Issue{ID: "gt-work", Status: string(beads.StatusOpen)})
	f.closeErr = fmt.Errorf("dolt unavailable")

	var out bytes.Buffer
	mr := &MRInfo{ID: "gt-mr", SourceIssue: "gt-work", Target: "main"}

	closeMergedWorkBead(f, &out, mr, "abc123")

	if got := f.issues["gt-work"].Status; got != string(beads.StatusOpen) {
		t.Fatalf("work bead status = %q, want still open after close error", got)
	}
	if !strings.Contains(out.String(), "Warning: failed to close work bead") {
		t.Fatalf("expected close-failure warning, got: %q", out.String())
	}
}

// TestRejectPath_DoesNotCloseWorkBead proves the negative case structurally:
// the merge-bead close (closeMergedWorkBead) must be reached ONLY from the
// merge-success handler, never from the reject/conflict handler. A reject must
// leave the work bead open so the polecat can fix and resubmit.
//
// We assert this by inspecting the source of HandleMRInfoFailure and verifying
// it contains no call to closeMergedWorkBead. This guards against a future edit
// that wires the close into the failure path (which would close beads on
// reject).
func TestRejectPath_DoesNotCloseWorkBead(t *testing.T) {
	src, err := os.ReadFile("engineer.go")
	if err != nil {
		t.Fatalf("read engineer.go: %v", err)
	}

	// Extract the body of HandleMRInfoFailure.
	body := string(src)
	start := strings.Index(body, "func (e *Engineer) HandleMRInfoFailure(")
	if start == -1 {
		t.Fatal("could not locate HandleMRInfoFailure in engineer.go")
	}
	rest := body[start:]
	// End at the next top-level func declaration.
	nextFunc := regexp.MustCompile(`\nfunc `).FindStringIndex(rest[1:])
	failureBody := rest
	if nextFunc != nil {
		failureBody = rest[:nextFunc[0]+1]
	}

	if strings.Contains(failureBody, "closeMergedWorkBead") {
		t.Fatal("HandleMRInfoFailure must NOT call closeMergedWorkBead — " +
			"rejects/conflicts must leave the work bead open")
	}

	// And confirm the success handler IS the call site, so the test is meaningful.
	successIdx := strings.Index(body, "func (e *Engineer) HandleMRInfoSuccess(")
	if successIdx == -1 {
		t.Fatal("could not locate HandleMRInfoSuccess")
	}
	successBody := body[successIdx:start]
	if !strings.Contains(successBody, "closeMergedWorkBead") {
		t.Fatal("HandleMRInfoSuccess should call closeMergedWorkBead (fix regressed?)")
	}
}
