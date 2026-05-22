//go:build !windows

package polecat

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}

func processFingerprintForPID(pid int) (processFingerprint, bool) {
	if pid <= 0 {
		return processFingerprint{}, false
	}

	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return processFingerprint{}, false
	}
	stat := string(data)
	end := strings.LastIndex(stat, ")")
	if end < 0 || end+2 >= len(stat) {
		return processFingerprint{}, false
	}
	fields := strings.Fields(stat[end+2:])
	if len(fields) < 20 {
		return processFingerprint{}, false
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return processFingerprint{}, false
	}
	return processFingerprint{StartTime: fields[19]}, true
}
