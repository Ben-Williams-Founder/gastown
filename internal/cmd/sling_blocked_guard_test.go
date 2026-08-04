package cmd

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/scheduler/capacity"
)

// Regression tests for the fail-closed BLOCKED dispatch predicate (hq-vcg3).
//
// THE BUG: no sling path had a `blocked` predicate. The eligibility gates checked
// closed/tombstone/pinned/hooked/in_progress/deferred only, so a bead an operator
// had explicitly BLOCKED (nca-c6d, "do not re-sling") was repeatedly re-dispatched
// to a rig polecat that could only refuse and close — a full agent spawn burned per
// cycle, with a witness containing it by hand each time.
//
// These tests are written to avoid the "green that lies" failure mode:
//
//  1. Every bd stub reports issue_type="task" with NO labels, so the neighbouring
//     control-plane guard CANNOT fire. A refusal here is provably the BLOCKED
//     predicate, not the guard next to it. The assertions check for "BLOCKED" and
//     explicitly reject a "control-plane" refusal.
//  2. The assertion is NOT merely "an error came back". The spawn seam is replaced
//     with one that fails the test if called, so the tests prove no polecat was
//     spawned and no capacity was consumed — i.e. the bead was never dispatched,
//     which is the actual invariant. An error alongside a spawned polecat would be
//     a green that lies.

// newStatusGuardTestEnv builds a town whose bd stub reports every bead with the
// given status, issue_type="task" and no labels. The type/label choice is
// load-bearing: it keeps the control-plane guard out of the picture so these tests
// exercise the BLOCKED predicate in isolation. Reuses the bd stub from
// sling_control_plane_guard_test.go.
func newStatusGuardTestEnv(t *testing.T, status string) string {
	t.Helper()
	return newControlPlaneTestEnv(t, status, "task", nil)
}

// failIfSpawned replaces the polecat spawn seam with one that fails the test.
// A BLOCKED bead must be refused BEFORE anything is spawned — the whole point of
// the guard is that no agent slot is burned.
func failIfSpawned(t *testing.T) {
	t.Helper()
	prev := spawnPolecatForSling
	t.Cleanup(func() { spawnPolecatForSling = prev })
	spawnPolecatForSling = func(rigName string, opts SlingSpawnOptions) (*SpawnedPolecatInfo, error) {
		t.Errorf("polecat spawned for a BLOCKED bead in rig %q — the bead was dispatched despite the guard", rigName)
		return &SpawnedPolecatInfo{RigName: rigName, PolecatName: "should-not-exist"}, nil
	}
}

// assertBlockedRefusal checks that err is the BLOCKED refusal (and not some other
// guard, notably the control-plane one, firing by accident).
func assertBlockedRefusal(t *testing.T, err error, beadID string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a refusal for BLOCKED bead %s, got nil — the bead would be dispatched", beadID)
	}
	msg := err.Error()
	if !strings.Contains(msg, "BLOCKED") {
		t.Errorf("refusal must name the BLOCKED status so an operator can see WHY; got: %v", err)
	}
	if !strings.Contains(msg, beadID) {
		t.Errorf("refusal must name the bead %q so an operator can see WHICH; got: %v", beadID, err)
	}
	if strings.Contains(msg, "control-plane") {
		t.Errorf("refusal came from the control-plane guard, not the BLOCKED predicate — this test proves nothing about hq-vcg3: %v", err)
	}
}

// TestExecuteSling_BlockedBeadNeverDispatched is THE regression test: a BLOCKED
// bead with free capacity (spawn seam available, nothing else refusing) must never
// be dispatched.
func TestExecuteSling_BlockedBeadNeverDispatched(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	townRoot := newStatusGuardTestEnv(t, "blocked")
	failIfSpawned(t)

	result, err := executeSling(SlingParams{BeadID: "test-blocked1", RigName: "testrig", TownRoot: townRoot})
	assertBlockedRefusal(t, err, "test-blocked1")
	if result == nil {
		t.Fatal("expected a SlingResult even on refusal")
	}
	if result.ErrMsg != "blocked" {
		t.Errorf("expected ErrMsg=%q so the scheduler records the real reason, got %q", "blocked", result.ErrMsg)
	}
	if result.Success {
		t.Error("result.Success must be false for a refused BLOCKED bead")
	}
	if result.PolecatName != "" {
		t.Errorf("no polecat may be attached to a refused BLOCKED bead, got %q", result.PolecatName)
	}
}

// TestExecuteSling_BlockedBead_ForceDoesNotBypass: --force must NOT override the
// guard. This is load-bearing rather than stylistic — the deacon redispatch path
// (internal/deacon/redispatch.go slingBead) shells `gt sling <bead> <rig> --force`,
// so a force-overridable gate would not close the hole it exists for.
func TestExecuteSling_BlockedBead_ForceDoesNotBypass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	townRoot := newStatusGuardTestEnv(t, "blocked")
	failIfSpawned(t)

	result, err := executeSling(SlingParams{BeadID: "test-blocked2", RigName: "testrig", TownRoot: townRoot, Force: true})
	assertBlockedRefusal(t, err, "test-blocked2")
	if result != nil && result.ErrMsg != "blocked" {
		t.Errorf("expected ErrMsg=%q under --force, got %q", "blocked", result.ErrMsg)
	}
}

// TestExecuteSling_BlockedStatusCaseVariantsRefused: bd does not normalize the
// status on write, so a casing/whitespace variant must not read as dispatchable.
// Fail-closed means "BLOCKED" and " Blocked " are refused exactly like "blocked".
func TestExecuteSling_BlockedStatusCaseVariantsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	for _, status := range []string{"blocked", "BLOCKED", "Blocked", " blocked "} {
		t.Run(strings.ReplaceAll(status, " ", "_"), func(t *testing.T) {
			townRoot := newStatusGuardTestEnv(t, status)
			failIfSpawned(t)
			_, err := executeSling(SlingParams{BeadID: "test-case1", RigName: "testrig", TownRoot: townRoot})
			assertBlockedRefusal(t, err, "test-case1")
		})
	}
}

// TestExecuteSling_NonBlockedBeadNotRefused is the false-positive guard: the
// dispatchable statuses must sail past the BLOCKED predicate untouched. Without
// this, an over-broad predicate would silently stop the whole town and still look
// green on the refusal tests above.
func TestExecuteSling_NonBlockedBeadNotRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	// "blocker"/"unblocked" are here as substring traps: a predicate written with
	// strings.Contains instead of an exact match would wrongly refuse them.
	for _, status := range []string{"open", "in_progress", "blocker", "unblocked"} {
		t.Run(status, func(t *testing.T) {
			townRoot := newStatusGuardTestEnv(t, status)
			prevSpawn := spawnPolecatForSling
			t.Cleanup(func() { spawnPolecatForSling = prevSpawn })
			spawnPolecatForSling = func(rigName string, opts SlingSpawnOptions) (*SpawnedPolecatInfo, error) {
				return &SpawnedPolecatInfo{RigName: rigName, PolecatName: "toast"}, nil
			}

			result, err := executeSling(SlingParams{BeadID: "test-ok1", RigName: "testrig", TownRoot: townRoot, Force: true})
			if err != nil && strings.Contains(err.Error(), "BLOCKED") {
				t.Errorf("status %q wrongly refused by the BLOCKED guard (false positive): %v", status, err)
			}
			if result != nil && result.ErrMsg == "blocked" {
				t.Errorf("status %q wrongly recorded as blocked: %q", status, result.ErrMsg)
			}
		})
	}
}

// TestRunSling_BlockedBeadRefusedBeforeSpawn drives the CLI path (`gt sling`) —
// the path the deacon redispatch and every operator sling go through — against a
// real bd stub, and proves the refusal happens before resolveTarget()/spawn, so
// zero polecats and zero rollbacks are involved.
func TestRunSling_BlockedBeadRefusedBeforeSpawn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mutable POSIX bd stub")
	}
	_, _, _ = setupMutableBDRawSlingTest(t, "Blocked work body.")
	if err := os.WriteFile(os.Getenv("BD_STATUS_FILE"), []byte("blocked"), 0644); err != nil {
		t.Fatalf("write blocked status: %v", err)
	}

	prevNoConvoy, prevNoBoot, prevHookRaw := slingNoConvoy, slingNoBoot, slingHookRawBead
	prevForce, prevSpawn, prevRollback := slingForce, spawnPolecatForSling, rollbackSlingArtifactsFn
	t.Cleanup(func() {
		slingNoConvoy, slingNoBoot, slingHookRawBead = prevNoConvoy, prevNoBoot, prevHookRaw
		slingForce, spawnPolecatForSling, rollbackSlingArtifactsFn = prevForce, prevSpawn, prevRollback
	})
	slingNoConvoy, slingNoBoot, slingHookRawBead, slingForce = true, true, true, false

	spawnPolecatForSling = func(rigName string, opts SlingSpawnOptions) (*SpawnedPolecatInfo, error) {
		t.Errorf("runSling spawned a polecat for a BLOCKED bead in rig %q", rigName)
		return &SpawnedPolecatInfo{RigName: rigName, PolecatName: "should-not-exist"}, nil
	}
	rollbackSlingArtifactsFn = func(spawnInfo *SpawnedPolecatInfo, beadID, hookWorkDir, convoyID string) {
		t.Errorf("rollback ran for a BLOCKED bead — the guard fired too late (after spawn)")
	}

	err := runSling(nil, []string{"gt-rawrollback", "gastown"})
	assertBlockedRefusal(t, err, "gt-rawrollback")
}

// TestRunSling_BlockedBead_ForceDoesNotBypass: `gt sling --force` on the CLI path.
// This is the exact shape of the deacon's redispatch invocation.
func TestRunSling_BlockedBead_ForceDoesNotBypass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mutable POSIX bd stub")
	}
	_, _, _ = setupMutableBDRawSlingTest(t, "Blocked work body.")
	if err := os.WriteFile(os.Getenv("BD_STATUS_FILE"), []byte("blocked"), 0644); err != nil {
		t.Fatalf("write blocked status: %v", err)
	}

	prevNoConvoy, prevNoBoot, prevHookRaw := slingNoConvoy, slingNoBoot, slingHookRawBead
	prevForce, prevSpawn := slingForce, spawnPolecatForSling
	t.Cleanup(func() {
		slingNoConvoy, slingNoBoot, slingHookRawBead = prevNoConvoy, prevNoBoot, prevHookRaw
		slingForce, spawnPolecatForSling = prevForce, prevSpawn
	})
	slingNoConvoy, slingNoBoot, slingHookRawBead, slingForce = true, true, true, true

	spawnPolecatForSling = func(rigName string, opts SlingSpawnOptions) (*SpawnedPolecatInfo, error) {
		t.Errorf("--force bypassed the BLOCKED guard and spawned a polecat in rig %q", rigName)
		return &SpawnedPolecatInfo{RigName: rigName, PolecatName: "should-not-exist"}, nil
	}

	err := runSling(nil, []string{"gt-rawrollback", "gastown"})
	assertBlockedRefusal(t, err, "gt-rawrollback")
}

// TestScheduleBead_BlockedBeadRefusedAtEnqueue covers the path that actually runs
// in the live town: with scheduler.max_polecats > 0 the town is in DEFERRED
// dispatch mode, where runSling returns into scheduleBead BEFORE reaching its own
// eligibility gates. Without a predicate here, `gt sling <blocked> <rig>` lands in
// the queue no matter what the runSling guard says.
func TestScheduleBead_BlockedBeadRefusedAtEnqueue(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mutable POSIX bd stub")
	}
	_, _, _ = setupMutableBDRawSlingTest(t, "Blocked work body.")
	if err := os.WriteFile(os.Getenv("BD_STATUS_FILE"), []byte("blocked"), 0644); err != nil {
		t.Fatalf("write blocked status: %v", err)
	}
	failIfSpawned(t)

	// Force + NoConvoy: prove neither the force flag nor the convoy path is what
	// stops it — only the BLOCKED predicate does.
	err := scheduleBead("gt-rawrollback", "gastown", ScheduleOptions{Force: true, NoConvoy: true})
	assertBlockedRefusal(t, err, "gt-rawrollback")
}

// TestScheduleBead_NonBlockedBeadNotRefusedAtEnqueue is the enqueue-side
// false-positive guard: an open bead must still be admitted to the queue.
func TestScheduleBead_NonBlockedBeadNotRefusedAtEnqueue(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mutable POSIX bd stub")
	}
	_, _, _ = setupMutableBDRawSlingTest(t, "Open work body.")
	if err := os.WriteFile(os.Getenv("BD_STATUS_FILE"), []byte("open"), 0644); err != nil {
		t.Fatalf("write open status: %v", err)
	}

	// May still fail for unrelated stub reasons (no real sling-context store); the
	// assertion is only that the BLOCKED predicate is not what refused it.
	err := scheduleBead("gt-rawrollback", "gastown", ScheduleOptions{Force: true, NoConvoy: true})
	if err != nil && strings.Contains(err.Error(), "BLOCKED") {
		t.Errorf("open bead wrongly refused at enqueue by the BLOCKED guard: %v", err)
	}
}

// TestDispatchSingleBead_BlockedBeadRefused covers the scheduler/queue admit path
// (dispatchSingleBead -> executeSling), which is how the daemon feeds ready work.
// The BLOCKED bead must be refused there too, with the reason recorded for the
// scheduler's failure bookkeeping.
func TestDispatchSingleBead_BlockedBeadRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mutable POSIX bd stub")
	}
	townRoot, _, _ := setupMutableBDRawSlingTest(t, "Blocked work body.")
	if err := os.WriteFile(os.Getenv("BD_STATUS_FILE"), []byte("blocked"), 0644); err != nil {
		t.Fatalf("write blocked status: %v", err)
	}
	failIfSpawned(t)

	result, err := dispatchSingleBead(capacity.PendingBead{
		ID:         "gt-context",
		WorkBeadID: "gt-rawrollback",
		TargetRig:  "gastown",
		Context: &capacity.SlingContextFields{
			WorkBeadID: "gt-rawrollback",
			TargetRig:  "gastown",
		},
	}, townRoot, "test")
	assertBlockedRefusal(t, err, "gt-rawrollback")
	if result != nil && result.ErrMsg != "blocked" {
		t.Errorf("scheduler must record ErrMsg=%q for RCA, got %q", "blocked", result.ErrMsg)
	}
}
