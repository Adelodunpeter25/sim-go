//go:build windows

package idb

import "golang.org/x/sys/windows"

// stillActive is the exit code GetExitCodeProcess reports for a live
// process (the Win32 STILL_ACTIVE status, not exported by x/sys/windows).
const stillActive = 259

// processAlive probes liveness via an exit-code read on a query-only handle;
// signal 0 does not exist on Windows.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
