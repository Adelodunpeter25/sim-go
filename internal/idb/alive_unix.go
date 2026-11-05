//go:build !windows

package idb

import "syscall"

// processAlive probes liveness with signal 0, which checks the pid without
// delivering anything.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
