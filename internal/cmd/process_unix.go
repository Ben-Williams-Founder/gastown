//go:build !windows

package cmd

import (
	"os"
	"syscall"
)

// isProcessRunning checks if a process with the given PID exists.
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}

	// EPERM means process exists but we don't have permission to signal it.
	return err == syscall.EPERM
}
