//go:build !windows

package util

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSetProcessGroup_KillsGrandchildOnContextCancel verifies the bug fix for
// gt-m7jc: when a CommandContext-based subprocess spawns its own children
// (grandchildren of the gt process), context cancellation must kill the entire
// process group, not just the direct child.
func TestSetProcessGroup_KillsGrandchildOnContextCancel(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "grandchild.pid")

	// Shell script that backgrounds a sleep and writes its PID to a file,
	// then waits. SIGKILL on the shell alone leaves the sleep orphaned;
	// SIGKILL on the process group kills both.
	script := fmt.Sprintf("sleep 30 & echo $! > %s; wait", pidFile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	SetProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting cmd: %v", err)
	}

	// Wait for the grandchild to spawn and write its PID.
	var grandchildPID int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil && len(strings.TrimSpace(string(data))) > 0 {
			pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
			if perr == nil && pid > 0 {
				grandchildPID = pid
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if grandchildPID == 0 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("grandchild PID never appeared")
	}

	// Cancel the context — this must kill the whole process group, including
	// the grandchild sleep.
	cancel()
	_ = cmd.Wait()

	// Poll for the grandchild's death. signal 0 returns ESRCH once reaped.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(grandchildPID, 0)
		if err == syscall.ESRCH {
			return // grandchild gone — fix works
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Still alive: defuse the test pollution before failing.
	_ = syscall.Kill(grandchildPID, syscall.SIGKILL)
	t.Fatalf("grandchild pid %d survived context cancel — process group not killed", grandchildPID)
}

// TestSetDetachedProcessGroup_NoCancelHook documents that the detached variant
// is intentionally fire-and-forget: it sets the process group but installs no
// cancellation hook. Use SetProcessGroup for any CommandContext-based command.
func TestSetDetachedProcessGroup_NoCancelHook(t *testing.T) {
	cmd := exec.Command("true")
	SetDetachedProcessGroup(cmd)
	if cmd.Cancel != nil {
		t.Fatal("SetDetachedProcessGroup() must not install cmd.Cancel — that is SetProcessGroup's job")
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("SetDetachedProcessGroup() must set Setpgid=true")
	}
}

