package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitRun runs a git command in dir and fails the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// setupPolecatRepo creates a git repo with an origin remote and a feature branch
// checked out, mirroring a polecat worktree. Returns the worktree (clone) dir.
func setupPolecatRepo(t *testing.T) string {
	t.Helper()

	// Bare "origin" the clone tracks as origin/main.
	originDir := t.TempDir()
	gitRun(t, originDir, "init", "--bare", "--initial-branch=main")

	cloneDir := t.TempDir()
	gitRun(t, cloneDir, "init", "--initial-branch=main")
	gitRun(t, cloneDir, "config", "user.email", "test@example.com")
	gitRun(t, cloneDir, "config", "user.name", "Test")
	gitRun(t, cloneDir, "remote", "add", "origin", originDir)

	// Seed an initial commit and publish main so origin/main exists.
	psWriteFile(t, cloneDir, "README.md", "seed\n")
	gitRun(t, cloneDir, "add", "README.md")
	gitRun(t, cloneDir, "commit", "-m", "seed")
	gitRun(t, cloneDir, "push", "origin", "main")

	// Move onto a feature branch (what a polecat works on).
	gitRun(t, cloneDir, "checkout", "-b", "polecat/feature")
	return cloneDir
}

func psWriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestPolecatStopPendingWork(t *testing.T) {
	t.Run("clean feature branch — no work", func(t *testing.T) {
		dir := setupPolecatRepo(t)
		d := polecatStopPendingWork(dir)
		if d.HasWork {
			t.Fatalf("expected no work on clean branch, got reason=%q", d.Reason)
		}
	})

	t.Run("uncommitted changes — work detected (the stranding bug)", func(t *testing.T) {
		dir := setupPolecatRepo(t)
		// Agent wrote a file but never committed before the turn ended.
		psWriteFile(t, dir, "impl.go", "package impl\n")
		d := polecatStopPendingWork(dir)
		if !d.HasWork {
			t.Fatal("expected work to be detected for uncommitted changes")
		}
		if d.Reason != "uncommitted changes" {
			t.Fatalf("expected uncommitted-changes reason, got %q", d.Reason)
		}
	})

	t.Run("committed but unpushed — work detected", func(t *testing.T) {
		dir := setupPolecatRepo(t)
		psWriteFile(t, dir, "impl.go", "package impl\n")
		gitRun(t, dir, "add", "impl.go")
		gitRun(t, dir, "commit", "-m", "impl")
		d := polecatStopPendingWork(dir)
		if !d.HasWork {
			t.Fatal("expected work to be detected for committed-but-unpushed branch")
		}
		if d.Reason == "" {
			t.Fatal("expected a non-empty reason for unpushed commits")
		}
	})

	t.Run("bogus dir — fails closed, no work", func(t *testing.T) {
		d := polecatStopPendingWork(filepath.Join(t.TempDir(), "does-not-exist"))
		if d.HasWork {
			t.Fatal("expected fail-closed (no work) for a non-repo dir")
		}
	})
}
