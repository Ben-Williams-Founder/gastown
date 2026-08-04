package cmd

import (
	"os"
	"testing"
)

// Regression tests for the dispatched_by observability defect (hq-vcg3).
//
// detectActor() feeds both the activity-feed `actor` and the bead's
// `dispatched_by:` field. It used to return a bare "unknown" whenever GetRole()
// failed — and GetRole() resolves the town by walking UP FROM THE CWD only, so any
// dispatcher whose cwd is outside the town tree (daemon/systemd services, a
// detached redispatch, a worktree outside the town root) silently lost its
// identity even though GT_ROLE was exported. The live nca-c6d bead carries exactly
// that damage: `dispatched_by: unknown`, which makes mis-dispatch RCA guesswork.

// clearRoleEnv removes every identity env var so each case starts from a known
// state (t.Setenv restores whatever was there afterwards).
func clearRoleEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{EnvGTRole, "GT_RIG", "GT_CREW", "GT_POLECAT"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

// TestActorFromEnv covers the identity reconstruction used when the cwd cannot
// resolve a town.
func TestActorFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"deacon", map[string]string{"GT_ROLE": "deacon"}, "deacon"},
		{"mayor", map[string]string{"GT_ROLE": "mayor"}, "mayor"},
		{"compound witness", map[string]string{"GT_ROLE": "gastown/witness"}, "gastown/witness"},
		{"compound polecat", map[string]string{"GT_ROLE": "gastown/polecats/Toast"}, "gastown/polecats/Toast"},
		{"polecat from split env", map[string]string{"GT_ROLE": "polecat", "GT_RIG": "gastown", "GT_POLECAT": "Toast"}, "gastown/polecats/Toast"},
		{"crew from split env", map[string]string{"GT_ROLE": "crew", "GT_RIG": "whiz_kb", "GT_CREW": "mel"}, "whiz_kb/crew/mel"},
		{"boot", map[string]string{"GT_ROLE": "deacon/boot"}, "deacon-boot"},
		{"no identity", map[string]string{}, ""},
		{"blank role", map[string]string{"GT_ROLE": "   "}, ""},
		{"literal unknown is not an identity", map[string]string{"GT_ROLE": "unknown"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearRoleEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if got := actorFromEnv(); got != tt.want {
				t.Errorf("actorFromEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDetectActorUsesEnvIdentityOutsideTown is the actual regression: with a cwd
// that resolves to no town (so GetRole() fails), the exported GT_ROLE identity must
// still be reported instead of "unknown".
func TestDetectActorUsesEnvIdentityOutsideTown(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	// A bare temp dir is not a Gas Town workspace, so GetRole() fails here — the
	// exact condition the daemon/service dispatchers hit.
	outside := t.TempDir()
	if err := os.Chdir(outside); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	clearRoleEnv(t)
	t.Setenv(EnvGTRole, "deacon")
	if got := detectActor(); got != "deacon" {
		t.Errorf("detectActor() = %q outside a town with GT_ROLE=deacon, want %q — dispatched_by would be attributed wrongly", got, "deacon")
	}
}

// TestDetectActorFallsBackToUnknownWithNoIdentity: when there is genuinely nothing
// to report, "unknown" is still the answer (no invented identity).
func TestDetectActorFallsBackToUnknownWithNoIdentity(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	outside := t.TempDir()
	if err := os.Chdir(outside); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	clearRoleEnv(t)
	if got := detectActor(); got != "unknown" {
		t.Errorf("detectActor() = %q with no identity at all, want %q", got, "unknown")
	}
}
