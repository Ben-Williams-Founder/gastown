//go:build windows

package polecat

import (
	"fmt"
	"math"

	"golang.org/x/sys/windows"
)

func processAlive(pid int) bool {
	if pid <= 0 || pid > math.MaxUint32 {
		return false
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED
	}
	_ = windows.CloseHandle(handle)
	return true
}

func processFingerprintForPID(pid int) (processFingerprint, bool) {
	if pid <= 0 || pid > math.MaxUint32 {
		return processFingerprint{}, false
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return processFingerprint{}, false
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return processFingerprint{}, false
	}
	return processFingerprint{StartTime: fmt.Sprintf("%08x%08x", creation.HighDateTime, creation.LowDateTime)}, true
}
